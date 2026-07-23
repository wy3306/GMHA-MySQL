import { computed, onMounted, onUnmounted, ref, watch } from 'vue/dist/vue.esm-bundler.js'

const request = async (path, options = {}) => {
  const response = await fetch(`/api/v1${path}`, {
    ...options,
    headers: { 'Content-Type': 'application/json', ...(options.headers || {}) }
  })
  const raw = await response.text()
  let payload = {}
  try { payload = raw ? JSON.parse(raw) : {} } catch (_) {}
  if (!response.ok) throw new Error(payload.error || raw || `HTTP ${response.status}`)
  return payload
}

const initialConfig = () => ({
  enabled: true,
  collection_interval_seconds: 5,
  slow_threshold_ms: 1000,
  retention_hours: 24,
  max_sql_text_bytes: 65536,
  capture_sql_text: true,
  redact_literals: false
})

export default {
  name: 'SQLDiagnostics',
  props: {
    mysqlInstances: { type: Array, default: () => [] },
    clusters: { type: Array, default: () => [] },
    fixedCluster: { type: String, default: '' }
  },
  setup(props) {
    const tab = ref('current')
    const loading = ref(false)
    const error = ref('')
    const notice = ref('')
    const current = ref({ items: [], statuses: [], complete: true, warnings: [] })
    const top = ref({ items: [], coverage: { complete: true, statuses: [], warnings: [] } })
    const slow = ref({ items: [], coverage: { complete: true, statuses: [], warnings: [] }, total: 0 })
    const history = ref({ items: [], coverage: { complete: true, statuses: [], warnings: [] }, total: 0 })
    const audits = ref([])
    const config = ref(initialConfig())
    const configDraft = ref(initialConfig())
    const range = ref('60')
    const customStart = ref('')
    const customEnd = ref('')
    const cluster = ref(props.fixedCluster || '')
    const instanceKey = ref('')
    const keyword = ref('')
    const topSortKey = ref('total_latency_ms')
    const topSortDirection = ref('desc')
    const slowSortKey = ref('duration_ms')
    const slowSortDirection = ref('desc')
    const expandedTopSQL = ref(new Set())
    const slowThreshold = ref(1000)
    const selected = ref(null)
    const killDialog = ref(null)
    const killReason = ref('')
    const killConfirmation = ref('')
    const killBusy = ref(false)
    const autoRefresh = ref(true)
    const refreshedAt = ref('')
    let timer = null

    const effectiveCluster = computed(() => props.fixedCluster || cluster.value)
    const instances = computed(() => (props.mysqlInstances || []).map(item => ({
      machine_id: item.MachineID || item.machine_id,
      machine_name: item.MachineName || item.machine_name || item.MachineIP || item.machine_ip,
      machine_ip: item.MachineIP || item.machine_ip,
      cluster: item.Cluster || item.cluster || '',
      port: Number(item.Port || item.port || 3306)
    })).filter(item => !effectiveCluster.value || item.cluster === effectiveCluster.value))
    const selectedInstance = computed(() => instances.value.find(item => `${item.machine_id}:${item.port}` === instanceKey.value))
    const activeCoverage = computed(() => {
      if (tab.value === 'current') return current.value
      if (tab.value === 'top') return top.value.coverage || {}
      if (tab.value === 'slow') return slow.value.coverage || {}
      if (tab.value === 'history') return history.value.coverage || {}
      return { complete: true, statuses: [], warnings: [] }
    })
    const healthyCount = computed(() => (activeCoverage.value.statuses || []).filter(item => item.status === 'ok').length)
    const statusCount = computed(() => (activeCoverage.value.statuses || []).length)

    function timeRange() {
      const end = range.value === 'custom' && customEnd.value ? new Date(customEnd.value) : new Date()
      const start = range.value === 'custom' && customStart.value
        ? new Date(customStart.value)
        : new Date(end.getTime() - Number(range.value || 60) * 60000)
      return { start: start.toISOString(), end: end.toISOString() }
    }
    function queryString(extra = {}) {
      const params = new URLSearchParams({ ...timeRange(), limit: '200', ...extra })
      if (effectiveCluster.value) params.set('cluster', effectiveCluster.value)
      if (selectedInstance.value) {
        params.set('machine', selectedInstance.value.machine_id)
        params.set('port', String(selectedInstance.value.port))
      }
      if (keyword.value) params.set('keyword', keyword.value)
      return params.toString()
    }
    function currentQuery() {
      const params = new URLSearchParams()
      if (effectiveCluster.value) params.set('cluster', effectiveCluster.value)
      if (selectedInstance.value) {
        params.set('machine', selectedInstance.value.machine_id)
        params.set('port', String(selectedInstance.value.port))
      }
      const value = params.toString()
      return value ? `?${value}` : ''
    }
    async function loadCurrent(silent = false) {
      if (!silent) loading.value = true
      error.value = ''
      try {
        current.value = await request(`/sql-diagnostics/current${currentQuery()}`)
        refreshedAt.value = current.value.collected_at || new Date().toISOString()
      } catch (err) { error.value = err.message }
      finally { if (!silent) loading.value = false }
    }
    async function loadTop() {
      loading.value = true; error.value = ''
      try {
        top.value = await request(`/sql-diagnostics/top?${queryString({ order_by: topSortKey.value, direction: topSortDirection.value, limit: '50' })}`)
        refreshedAt.value = new Date().toISOString()
      } catch (err) { error.value = err.message }
      finally { loading.value = false }
    }
    async function loadSlow() {
      loading.value = true; error.value = ''
      try {
        slow.value = await request(`/sql-diagnostics/slow?${queryString({
          threshold_ms: String(slowThreshold.value || config.value.slow_threshold_ms),
          sort_by: slowSortKey.value,
          direction: slowSortDirection.value
        })}`)
        refreshedAt.value = new Date().toISOString()
      } catch (err) { error.value = err.message }
      finally { loading.value = false }
    }
    async function loadHistory() {
      loading.value = true; error.value = ''
      try {
        history.value = await request(`/sql-diagnostics/history?${queryString()}`)
        refreshedAt.value = new Date().toISOString()
      } catch (err) { error.value = err.message }
      finally { loading.value = false }
    }
    async function loadConfig() {
      try {
        config.value = await request('/sql-diagnostics/config')
        configDraft.value = JSON.parse(JSON.stringify(config.value))
        slowThreshold.value = Number(config.value.slow_threshold_ms || 1000)
      } catch (err) { error.value = err.message }
    }
    async function saveConfig() {
      loading.value = true; error.value = ''
      try {
        config.value = await request('/sql-diagnostics/config', { method: 'PUT', body: JSON.stringify(configDraft.value) })
        configDraft.value = JSON.parse(JSON.stringify(config.value))
        notice.value = 'SQL 诊断采集配置已保存。'
      } catch (err) { error.value = err.message }
      finally { loading.value = false }
    }
    async function loadAudits() {
      loading.value = true; error.value = ''
      try { audits.value = (await request(`/sql-diagnostics/kill-audits?${queryString({ limit: '200' })}`)).items || [] }
      catch (err) { error.value = err.message }
      finally { loading.value = false }
    }
    async function refreshActive(silent = false) {
      if (tab.value === 'current') return loadCurrent(silent)
      if (tab.value === 'top') return loadTop()
      if (tab.value === 'slow') return loadSlow()
      if (tab.value === 'history') return loadHistory()
      if (tab.value === 'audit') return loadAudits()
    }
    function chooseTab(value) {
      tab.value = value
      selected.value = null
      if (value === 'settings') loadConfig()
      else refreshActive()
      resetTimer()
    }
    function resetTimer() {
      if (timer) clearInterval(timer)
      timer = null
      if (tab.value === 'current' && autoRefresh.value) timer = setInterval(() => loadCurrent(true), 5000)
    }
    function applyFilters() {
      if (range.value === 'custom' && (!customStart.value || !customEnd.value)) {
        error.value = '请选择完整的开始和结束时间。'
        return
      }
      refreshActive()
    }
    function openKill(item) {
      killDialog.value = item
      killReason.value = ''
      killConfirmation.value = ''
    }
    async function killSQL() {
      if (!killDialog.value) return
      killBusy.value = true; error.value = ''
      const item = killDialog.value
      try {
        await request('/sql-diagnostics/kill', {
          method: 'POST',
          body: JSON.stringify({
            machine_id: item.instance.machine_id,
            port: item.instance.port,
            process_id: item.process_id,
            expected_digest: item.digest,
            expected_started_at: item.query_started_at,
            confirmation: killConfirmation.value,
            reason: killReason.value
          })
        })
        notice.value = `已向进程 ${item.process_id} 发送 KILL QUERY；连接本身保留。`
        killDialog.value = null
        await loadCurrent()
      } catch (err) { error.value = err.message }
      finally { killBusy.value = false }
    }
    async function copySQL(text) {
      try {
        await navigator.clipboard.writeText(text || '')
        notice.value = 'SQL 已复制。'
      } catch (_) { error.value = '浏览器未允许复制，请在详情中手动选择 SQL。' }
    }
    function duration(ms) {
      const value = Number(ms || 0)
      if (value < 1000) return `${value.toFixed(value < 10 ? 2 : 0)} ms`
      if (value < 60000) return `${(value / 1000).toFixed(2)} s`
      return `${Math.floor(value / 60000)}m ${Math.round(value % 60000 / 1000)}s`
    }
    function number(value, digits = 0) {
      return Number(value || 0).toLocaleString('zh-CN', { maximumFractionDigits: digits })
    }
    function date(value) {
      if (!value) return '—'
      return new Date(value).toLocaleString('zh-CN', { hour12: false })
    }
    function instanceLabel(instance) {
      return `${instance?.machine_name || instance?.machine_ip || '未知实例'} · ${instance?.machine_ip || ''}:${instance?.port || ''}`
    }
    function detailInstanceLabel(item) {
      if (item?.instance) return instanceLabel(item.instance)
      const aggregateInstances = item?.instances || []
      if (!aggregateInstances.length) return '未关联实例'
      if (aggregateInstances.length === 1) return instanceLabel(aggregateInstances[0])
      const first = instanceLabel(aggregateInstances[0])
      return `${aggregateInstances.length} 个实例 · ${first} 等`
    }
    function sqlPreview(item) {
      return item.sql_text || item.digest_text || 'SQL 文本未采集'
    }
    function topRowKey(item) {
      return `${item.digest || ''}\u0000${item.database || ''}`
    }
    function isTopSQLExpanded(item) {
      return expandedTopSQL.value.has(topRowKey(item))
    }
    function canExpandTopSQL(item) {
      return sqlPreview(item).length > 160 || sqlPreview(item).includes('\n')
    }
    function toggleTopSQL(item) {
      const key = topRowKey(item)
      const next = new Set(expandedTopSQL.value)
      if (next.has(key)) next.delete(key)
      else next.add(key)
      expandedTopSQL.value = next
    }
    function shortDigest(value) {
      if (!value) return '暂无 Digest'
      return value.length > 18 ? `${value.slice(0, 18)}…` : value
    }
    function coverageLabel() {
      if (!statusCount.value) return '尚无采集状态'
      return `${healthyCount.value}/${statusCount.value} 实例采集正常`
    }
    function setRange(value) {
      range.value = value
      if (value !== 'custom') refreshActive()
    }
    function changeTopSort(key) {
      if (topSortKey.value === key) topSortDirection.value = topSortDirection.value === 'desc' ? 'asc' : 'desc'
      else {
        topSortKey.value = key
        topSortDirection.value = 'desc'
      }
      loadTop()
    }
    function changeSlowSort(key) {
      if (slowSortKey.value === key) slowSortDirection.value = slowSortDirection.value === 'desc' ? 'asc' : 'desc'
      else {
        slowSortKey.value = key
        slowSortDirection.value = 'desc'
      }
      loadSlow()
    }
    function sortIndicator(activeKey, direction, key) {
      return activeKey === key ? (direction === 'asc' ? '↑' : '↓') : '↕'
    }

    watch(autoRefresh, resetTimer)
    watch(() => props.mysqlInstances, () => {
      if (selectedInstance.value == null) instanceKey.value = ''
    })
    watch(() => props.fixedCluster, value => {
      cluster.value = value || ''
      instanceKey.value = ''
      refreshActive()
    })
    onMounted(async () => {
      await loadConfig()
      await loadCurrent()
      resetTimer()
    })
    onUnmounted(() => { if (timer) clearInterval(timer) })

    return {
      tab, loading, error, notice, current, top, slow, history, audits, config, configDraft,
      range, customStart, customEnd, cluster, instanceKey, keyword, topSortKey, topSortDirection,
      slowSortKey, slowSortDirection, expandedTopSQL, slowThreshold,
      selected, killDialog, killReason, killConfirmation, killBusy, autoRefresh, refreshedAt,
      effectiveCluster, instances, activeCoverage, healthyCount, statusCount, chooseTab, applyFilters, refreshActive,
      saveConfig, openKill, killSQL, copySQL, duration, number, date, instanceLabel, detailInstanceLabel, sqlPreview,
      coverageLabel, setRange, loadAudits, changeTopSort, changeSlowSort, sortIndicator,
      topRowKey, isTopSQLExpanded, canExpandTopSQL, toggleTopSQL, shortDigest
    }
  },
  template: `
    <section class="sql-diagnostics-page">
      <header class="sql-diag-page-head">
        <div><p>SQL OBSERVABILITY</p><h3>SQL 诊断</h3><span>{{ effectiveCluster }} · 实时执行、区间聚合与历史回放</span></div>
        <div class="sql-diag-live-state"><span :class="{loading,warning:!['settings','audit'].includes(tab) && (!activeCoverage.complete || activeCoverage.warnings?.length)}"><i></i>{{ tab==='settings' ? '全局配置' : tab==='audit' ? '审计记录' : activeCoverage.complete && !activeCoverage.warnings?.length ? '采集正常' : '采集不完整' }}</span><small>{{ refreshedAt ? '更新于 '+date(refreshedAt) : '等待首次采集' }}</small><button class="icon-button" type="button" :disabled="loading" title="刷新当前视图" @click="refreshActive()">↻</button></div>
      </header>

      <nav class="sql-diag-tabs" aria-label="SQL 诊断分类">
        <button type="button" :class="{active:tab==='current'}" @click="chooseTab('current')">实时 SQL <span>{{ current.items?.length || 0 }}</span></button>
        <button type="button" :class="{active:tab==='top'}" @click="chooseTab('top')">TOP-SQL</button>
        <button type="button" :class="{active:tab==='slow'}" @click="chooseTab('slow')">慢 SQL</button>
        <button type="button" :class="{active:tab==='history'}" @click="chooseTab('history')">历史回放</button>
        <button type="button" :class="{active:tab==='audit'}" @click="chooseTab('audit')">查杀审计</button>
        <button type="button" :class="{active:tab==='settings'}" @click="chooseTab('settings')">采集设置</button>
      </nav>

      <div v-if="error" class="sql-diag-message error"><b>请求失败</b><span>{{ error }}</span><button @click="error=''">×</button></div>
      <div v-if="notice" class="sql-diag-message notice"><b>操作完成</b><span>{{ notice }}</span><button @click="notice=''">×</button></div>
      <div v-if="activeCoverage.warnings?.length && tab!=='settings' && tab!=='audit'" class="sql-coverage-warning">
        <div><b>当前结果可能不完整</b><span>{{ activeCoverage.warnings[0] }}</span></div>
        <details v-if="activeCoverage.warnings.length>1"><summary>查看全部 {{ activeCoverage.warnings.length }} 项</summary><p v-for="warning in activeCoverage.warnings" :key="warning">{{ warning }}</p></details>
      </div>

      <section v-if="!['settings','audit'].includes(tab)" class="sql-diag-toolbar">
        <div class="sql-filter-main">
          <span class="sql-scope-chip"><i></i>{{ effectiveCluster }}</span>
          <label>实例<select v-model="instanceKey"><option value="">全部实例</option><option v-for="item in instances" :key="item.machine_id+':'+item.port" :value="item.machine_id+':'+item.port">{{ item.machine_name }} · {{ item.machine_ip }}:{{ item.port }}</option></select></label>
          <label v-if="tab!=='current'" class="sql-keyword">SQL / Digest<input v-model.trim="keyword" placeholder="搜索 SQL、模板或 Digest"></label>
          <label v-if="tab==='slow'">阈值<input v-model.number="slowThreshold" class="sql-threshold-input" type="number" min="1"><small>ms</small></label>
          <label v-if="tab==='current'" class="sql-auto-refresh"><input v-model="autoRefresh" type="checkbox"> 5 秒刷新</label>
          <button class="primary" type="button" @click="applyFilters">查询</button>
        </div>
        <div v-if="tab!=='current'" class="sql-filter-time">
          <b>时间范围</b>
          <div class="sql-range-buttons"><button v-for="item in [['15','15 分钟'],['60','1 小时'],['360','6 小时'],['1440','24 小时']]" type="button" :key="item[0]" :class="{active:range===item[0]}" @click="setRange(item[0])">{{ item[1] }}</button><button type="button" :class="{active:range==='custom'}" @click="range='custom'">自定义</button></div>
        </div>
        <label v-if="range==='custom' && tab!=='current'">开始<input v-model="customStart" type="datetime-local"></label>
        <label v-if="range==='custom' && tab!=='current'">结束<input v-model="customEnd" type="datetime-local"></label>
      </section>

      <section v-if="tab==='current'" class="sql-diag-panel">
        <div class="sql-diag-summary"><article><small>正在执行</small><b>{{ current.items?.length || 0 }}</b><span>已排除 Sleep</span></article><article><small>慢查询</small><b>{{ (current.items||[]).filter(item=>item.elapsed_ms>=slowThreshold).length }}</b><span>阈值 {{ duration(slowThreshold) }}</span></article><article><small>最长执行</small><b>{{ duration(Math.max(0,...(current.items||[]).map(item=>item.elapsed_ms))) }}</b><span>服务器计时</span></article><article><small>采集覆盖</small><b>{{ healthyCount }}/{{ statusCount }}</b><span>实例状态正常</span></article></div>
        <div class="sql-table-wrap"><table class="sql-live-table"><thead><tr><th>SQL 内容</th><th>实例 / 进程</th><th>用户 / 客户端</th><th>数据库 / 状态</th><th>执行时间</th><th>操作</th></tr></thead><tbody>
          <tr v-for="item in current.items" :key="item.id">
            <td class="sql-text-cell"><button class="sql-preview-button" type="button" @click="selected=item"><code>{{ sqlPreview(item) }}</code><small>{{ item.digest ? 'Digest '+item.digest.slice(0,20)+'…' : '暂无 Digest' }}</small></button></td>
            <td><b>{{ item.instance?.machine_name || item.instance?.machine_ip || '未知实例' }}</b><small>{{ item.instance?.machine_ip }}:{{ item.instance?.port }} · Process {{ item.process_id }}</small></td>
            <td><b>{{ item.user }}</b><small>{{ item.client_host || '本地' }}</small></td>
            <td><b>{{ item.database || '未选库' }}</b><small>{{ item.state || item.command || '执行中' }}</small></td>
            <td><strong :class="['sql-duration-badge',{'slow':item.elapsed_ms>=slowThreshold}]">{{ duration(item.elapsed_ms) }}</strong><small>{{ date(item.query_started_at) }}</small></td>
            <td class="sql-row-actions"><button class="text-button" type="button" @click="selected=item">详情</button><button class="danger-link" type="button" @click="openKill(item)">查杀</button></td>
          </tr>
          <tr v-if="!current.items?.length"><td colspan="6" class="empty sql-empty-state"><b>数据为空</b><span>当前没有正在执行的 SQL；若采集覆盖不完整，请先处理上方警告。</span></td></tr>
        </tbody></table></div>
      </section>

      <section v-else-if="tab==='top'" class="sql-diag-panel">
        <header class="sql-result-head"><div><h3>区间 TOP-SQL</h3><p>执行次数与耗时由相邻累计快照求差；长 SQL 可在表格内展开全文。</p></div><span>{{ top.items?.length || 0 }} 个 digest</span></header>
        <div class="sql-table-wrap"><table class="sql-top-table"><thead><tr>
          <th>排名</th><th>SQL 模板</th>
          <th><button :class="['sql-sort-button',{active:topSortKey==='execution_count'}]" type="button" @click="changeTopSort('execution_count')">执行次数 <i>{{ sortIndicator(topSortKey,topSortDirection,'execution_count') }}</i></button></th>
          <th class="sql-grouped-head"><span>耗时</span><div><button :class="['sql-sort-button',{active:topSortKey==='total_latency_ms'}]" type="button" @click="changeTopSort('total_latency_ms')">总计 <i>{{ sortIndicator(topSortKey,topSortDirection,'total_latency_ms') }}</i></button><button :class="['sql-sort-button',{active:topSortKey==='average_latency_ms'}]" type="button" @click="changeTopSort('average_latency_ms')">平均 <i>{{ sortIndicator(topSortKey,topSortDirection,'average_latency_ms') }}</i></button></div></th>
          <th class="sql-grouped-head"><span>执行特征</span><div><button :class="['sql-sort-button',{active:topSortKey==='rows_examined'}]" type="button" @click="changeTopSort('rows_examined')">扫描 <i>{{ sortIndicator(topSortKey,topSortDirection,'rows_examined') }}</i></button><button :class="['sql-sort-button',{active:topSortKey==='error_count'}]" type="button" @click="changeTopSort('error_count')">错误 <i>{{ sortIndicator(topSortKey,topSortDirection,'error_count') }}</i></button></div></th>
        </tr></thead><tbody>
          <tr v-for="item in top.items" :key="topRowKey(item)" :class="{'sql-top-row-expanded':isTopSQLExpanded(item)}">
            <td><strong class="sql-rank">{{ item.rank }}</strong></td>
            <td class="sql-top-sql-cell">
              <div class="sql-top-meta"><b>{{ item.database || '未选库' }}</b><span>{{ item.instances?.length || 0 }} 个实例</span><code :title="item.digest">{{ shortDigest(item.digest) }}</code></div>
              <code :class="['sql-top-preview',{expanded:isTopSQLExpanded(item)}]">{{ sqlPreview(item) }}</code>
              <div class="sql-top-actions"><button v-if="canExpandTopSQL(item)" class="text-button" type="button" :aria-expanded="isTopSQLExpanded(item)" @click="toggleTopSQL(item)">{{ isTopSQLExpanded(item) ? '收起' : '展开全文' }}</button><button class="text-button" type="button" @click="selected=item">查看详情</button></div>
            </td>
            <td><strong class="sql-metric-primary">{{ number(item.execution_count) }}</strong><small>区间执行</small></td>
            <td class="sql-metric-stack"><strong>{{ duration(item.total_latency_ms) }}</strong><small>平均 {{ duration(item.average_latency_ms) }}</small><small>最大 {{ item.max_observed_ms ? duration(item.max_observed_ms) : '暂无样本' }}</small><small v-if="item.lock_time_ms">锁等待 {{ duration(item.lock_time_ms) }}</small></td>
            <td class="sql-metric-stack"><strong>{{ number(item.rows_examined) }} <i>扫描</i></strong><small>返回 {{ number(item.rows_sent) }}</small><small :class="{'sql-metric-error':item.error_count>0}">错误 {{ number(item.error_count) }} · 警告 {{ number(item.warning_count) }}</small></td>
          </tr><tr v-if="!top.items?.length"><td colspan="5" class="empty sql-empty-state"><b>数据为空</b><span>所选区间没有可计算的 Digest 增量；新启用采集后至少需要两个快照。</span></td></tr>
        </tbody></table></div>
      </section>

      <section v-else-if="tab==='slow' || tab==='history'" class="sql-diag-panel">
        <header class="sql-result-head"><div><h3>{{ tab==='slow' ? '慢 SQL 明细' : '历史 SQL 会话与已完成语句' }}</h3><p>{{ tab==='slow' ? '合并已完成语句事件与采样期间仍在运行的长 SQL。' : '按所选时间段回放，来源字段用于区分会话采样和已完成语句。' }}</p></div><span>{{ tab==='slow' ? slow.total : history.total }} 条</span></header>
        <div class="sql-table-wrap"><table class="sql-history-table"><thead><tr>
          <th>SQL 内容</th>
          <th><button v-if="tab==='slow'" :class="['sql-sort-button',{active:slowSortKey==='started_at'}]" type="button" @click="changeSlowSort('started_at')">时间 <i>{{ sortIndicator(slowSortKey,slowSortDirection,'started_at') }}</i></button><span v-else>时间 / 来源</span></th>
          <th>实例</th>
          <th><button v-if="tab==='slow'" :class="['sql-sort-button',{active:slowSortKey==='duration_ms'}]" type="button" @click="changeSlowSort('duration_ms')">耗时 <i>{{ sortIndicator(slowSortKey,slowSortDirection,'duration_ms') }}</i></button><span v-else>耗时</span></th>
          <th>用户 / 库</th>
          <th><button v-if="tab==='slow'" :class="['sql-sort-button',{active:slowSortKey==='rows_examined'}]" type="button" @click="changeSlowSort('rows_examined')">扫描行数 <i>{{ sortIndicator(slowSortKey,slowSortDirection,'rows_examined') }}</i></button><span v-else>扫描行数</span></th>
          <th><button v-if="tab==='slow'" :class="['sql-sort-button',{active:slowSortKey==='rows_sent'}]" type="button" @click="changeSlowSort('rows_sent')">返回行数 <i>{{ sortIndicator(slowSortKey,slowSortDirection,'rows_sent') }}</i></button><span v-else>返回行数</span></th>
          <th><button v-if="tab==='slow'" :class="['sql-sort-button',{active:slowSortKey==='error_count'}]" type="button" @click="changeSlowSort('error_count')">错误数 <i>{{ sortIndicator(slowSortKey,slowSortDirection,'error_count') }}</i></button><span v-else>错误数</span></th>
        </tr></thead><tbody>
          <tr v-for="item in (tab==='slow'?slow.items:history.items)" :key="item.kind+item.id" @click="selected=item">
            <td class="sql-text-cell"><code>{{ sqlPreview(item) }}</code><small>{{ item.digest || '暂无 Digest' }}</small></td>
            <td><b>{{ date(item.started_at) }}</b><small>{{ item.source==='mysql.slow_log' ? 'MySQL 慢日志' : item.kind==='statement' ? '已完成事件' : '会话采样' }}</small></td><td><b>{{ item.instance?.machine_name || item.instance?.machine_ip || '未知实例' }}</b><small>{{ item.instance?.machine_ip }}:{{ item.instance?.port }}{{ item.process_id ? ' · Process '+item.process_id : '' }}</small></td>
            <td><strong :class="{'slow-duration':item.duration_ms>=slowThreshold}">{{ duration(item.duration_ms) }}</strong><small>{{ item.ended_at ? '结束 '+date(item.ended_at) : '采集时仍在执行' }}</small></td>
            <td><b>{{ item.user || '已完成' }}</b><small>{{ item.database || '未选库' }}</small></td>
            <td><b>{{ number(item.rows_examined) }}</b><em v-if="item.no_index_used">未使用索引</em></td>
            <td><b>{{ number(item.rows_sent) }}</b></td>
            <td><b :class="{'slow-duration':item.error_count>0}">{{ number(item.error_count) }}</b></td>
          </tr><tr v-if="!(tab==='slow'?slow.items:history.items)?.length"><td colspan="8" class="empty sql-empty-state"><b>数据为空</b><span>所选时间范围内没有匹配的 SQL 记录。</span></td></tr>
        </tbody></table></div>
      </section>

      <section v-else-if="tab==='audit'" class="sql-diag-panel">
        <header class="sql-result-head"><div><h3>SQL 查杀审计</h3><p>记录目标快照、操作原因、来源、结果和精确时间。</p></div><button class="secondary" @click="loadAudits">刷新</button></header>
        <div class="sql-table-wrap"><table class="sql-audit-table"><thead><tr><th>请求时间</th><th>实例 / 进程</th><th>目标用户</th><th>原因</th><th>SQL</th><th>结果</th></tr></thead><tbody>
          <tr v-for="item in audits" :key="item.id"><td>{{ date(item.requested_at) }}</td><td><b>{{ instanceLabel(item.instance) }}</b><small>Process {{ item.process_id }}</small></td><td>{{ item.user }}<small>{{ item.client_host }}</small></td><td>{{ item.reason }}<small>{{ item.request_source }}</small></td><td class="sql-text-cell"><code>{{ item.sql_text }}</code></td><td><span :class="['status',item.status==='success'?'success':'failed']">{{ item.status }}</span><small>{{ item.error }}</small></td></tr>
          <tr v-if="!audits.length"><td colspan="6" class="empty sql-empty-state"><b>数据为空</b><span>所选时间范围内没有 SQL 查杀记录。</span></td></tr>
        </tbody></table></div>
      </section>

      <section v-else-if="tab==='settings'" class="sql-settings-grid">
        <form class="panel sql-settings-form" @submit.prevent="saveConfig"><header><div><h3>全局采集与保留</h3><p>设置对全部集群生效；修改后下一采集周期应用，历史到期后每小时清理。</p></div><label class="switch"><input v-model="configDraft.enabled" type="checkbox"><span>启用采集</span></label></header>
          <div class="sql-settings-fields"><label>采集间隔（秒）<input v-model.number="configDraft.collection_interval_seconds" type="number" min="2" max="60"><small>允许 2–60 秒；越短越容易捕获短 SQL，存储与目标库开销也越高。</small></label><label>默认慢 SQL 阈值（毫秒）<input v-model.number="configDraft.slow_threshold_ms" type="number" min="1"></label><label>历史保留（小时）<input v-model.number="configDraft.retention_hours" type="number" min="1" max="8760"></label><label>单条 SQL 最大字节<input v-model.number="configDraft.max_sql_text_bytes" type="number" min="256" max="4194304"></label></div>
          <label class="sql-setting-check"><input v-model="configDraft.capture_sql_text" type="checkbox"><span><b>持久化 SQL 文本</b><small>关闭后仅保留 digest 和统计；账号密码类语句无论此项如何都会自动遮蔽认证秘密。</small></span></label>
          <label class="sql-setting-check"><input v-model="configDraft.redact_literals" type="checkbox"><span><b>遮蔽字符串和数字字面量</b><small>适合敏感生产库，但会降低历史 SQL 的参数定位能力；digest 聚合不受影响。</small></span></label>
          <div v-if="configDraft.capture_sql_text && !configDraft.redact_literals" class="sql-sensitive-warning"><b>当前保存 SQL 原始参数</b><span>SQL 可能包含业务敏感数据，请使用较短保留期并限制 Manager 元数据库访问。</span></div>
          <footer><button type="button" class="secondary" @click="configDraft=JSON.parse(JSON.stringify(config))">撤销</button><button class="primary" :disabled="loading">保存设置</button></footer>
        </form>
        <section class="panel sql-capability-panel"><header><div><h3>实例采集能力</h3><p>“历史长事件”关闭时，短 SQL 可能在采样间隔内执行完而无法进入明细；TABLE 慢日志可补足慢语句。</p></div></header><article v-for="item in current.statuses" :key="item.instance.machine_id+':'+item.instance.port"><div><b>{{ instanceLabel(item.instance) }}</b><small>{{ item.last_error || '最近采集正常' }}</small></div><span :class="['status',item.status==='ok'?'success':item.status==='degraded'?'running':'failed']">{{ item.status }}</span><dl><div><dt>performance_schema</dt><dd>{{ item.performance_schema_available?'可用':'不可用' }}</dd></div><div><dt>历史长事件</dt><dd>{{ item.history_long_consumer_enabled?'已启用':'未启用' }}</dd></div><div><dt>Digest 汇总</dt><dd>{{ item.digest_consumer_enabled?'已启用':'未启用' }}</dd></div><div><dt>TABLE 慢日志</dt><dd>{{ item.slow_log_table_available ? '可采集 · '+duration(item.slow_log_threshold_ms) : '未启用' }}</dd></div><div><dt>SQL 文本上限</dt><dd>{{ number(item.sql_text_limit) }} B</dd></div><div><dt>数据库时钟偏差</dt><dd>{{ number(item.server_clock_offset_ms) }} ms</dd></div></dl></article><div v-if="!current.statuses?.length" class="empty sql-empty-state"><b>数据为空</b><span>刷新实时 SQL 后显示当前集群的实例采集能力。</span></div></section>
      </section>

      <div v-if="selected" class="modal-mask sql-detail-mask" @click.self="selected=null"><section class="modal sql-detail-modal"><header><div><p>SQL DETAIL</p><h2>{{ selected.digest_text ? 'SQL 执行详情' : '会话详情' }}</h2><span>{{ detailInstanceLabel(selected) }}</span></div><button @click="selected=null">×</button></header><div class="sql-detail-meta"><div><small>Digest</small><code>{{ selected.digest }}</code></div><div><small>执行时间</small><b>{{ duration(selected.elapsed_ms ?? selected.duration_ms ?? selected.total_latency_ms) }}</b></div><div><small>数据库</small><b>{{ selected.database || '未选库' }}</b></div><div><small>开始时间</small><b>{{ date(selected.query_started_at || selected.started_at || selected.first_seen_at) }}</b></div></div><section><header><b>SQL 内容</b><button class="secondary" @click="copySQL(sqlPreview(selected))">复制 SQL</button></header><pre>{{ sqlPreview(selected) }}</pre></section><section v-if="selected.digest_text && selected.sql_text"><header><b>归一化模板</b></header><pre>{{ selected.digest_text }}</pre></section></section></div>

      <div v-if="killDialog" class="modal-mask sql-kill-mask" @click.self="killDialog=null"><form class="modal sql-kill-modal" @submit.prevent="killSQL"><header><div><p>DESTRUCTIVE DATABASE ACTION</p><h2>确认查杀当前 SQL</h2><span>仅执行 KILL QUERY，保留客户端连接；提交前会重新核对 digest 与开始时间。</span></div><button type="button" @click="killDialog=null">×</button></header><div class="sql-kill-target"><b>{{ instanceLabel(killDialog.instance) }}</b><span>Process {{ killDialog.process_id }} · {{ killDialog.user }} · 已运行 {{ duration(killDialog.elapsed_ms) }}</span><pre>{{ killDialog.sql_text }}</pre></div><label>操作原因<input v-model.trim="killReason" required minlength="3" placeholder="例如：阻塞核心订单表，已确认回滚"></label><label>输入确认短语 <code>KILL {{ killDialog.process_id }}</code><input v-model="killConfirmation" required :placeholder="'KILL '+killDialog.process_id" autocomplete="off"></label><div class="sql-kill-safety"><b>并发安全校验</b><span>若连接 ID 已复用、SQL 已切换或目标属于 MySQL 系统用户，服务端会拒绝执行。</span></div><footer><button type="button" class="secondary" @click="killDialog=null">取消</button><button class="danger" :disabled="killBusy || killConfirmation!==('KILL '+killDialog.process_id)">{{ killBusy ? '正在复核并查杀…' : '查杀 SQL' }}</button></footer></form></div>
    </section>
  `
}
