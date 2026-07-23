import { computed, onMounted, onUnmounted, ref, watch } from 'vue/dist/vue.esm-bundler.js'

const request = async (path, options = {}) => {
  const response = await fetch(`/api/v1${path}`, { headers: { 'Content-Type': 'application/json' }, ...options })
  const payload = await response.json().catch(() => ({}))
  if (!response.ok) throw new Error(payload.error || `请求失败（${response.status}）`)
  return payload
}

const value = (item, upper, lower) => item?.[upper] ?? item?.[lower]
const localInputTime = date => {
  const shifted = new Date(date.getTime() - date.getTimezoneOffset() * 60000)
  return shifted.toISOString().slice(0, 16)
}
const formatNumber = input => Number(input || 0).toLocaleString('zh-CN')
const formatBytes = input => {
  const bytes = Number(input || 0)
  if (!bytes) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  const index = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1)
  return `${(bytes / (1024 ** index)).toFixed(index ? 1 : 0)} ${units[index]}`
}
const formatTime = input => input ? new Date(input).toLocaleString('zh-CN', { hour12: false }) : '—'
export const formatDurationMicros = input => {
  if (input === undefined || input === null || Number(input) < 0) return '—'
  const microseconds = Number(input)
  if (microseconds < 1000) return `${Math.round(microseconds)} μs`
  if (microseconds < 1000000) return `${(microseconds / 1000).toFixed(1)} ms`
  if (microseconds < 60000000) return `${(microseconds / 1000000).toFixed(2)} s`
  return `${(microseconds / 60000000).toFixed(2)} min`
}
const taskStatusLabel = status => ({ queued: '排队中', running: '分析中', completed: '已完成', failed: '失败', canceled: '已取消' })[status] || status || '未知'
const operationLabel = kind => ({ INSERT: '新增', UPDATE: '更新', DELETE: '删除', CREATE: '创建', ALTER: '变更', DROP: '删除', TRUNCATE: '清空', RENAME: '重命名' })[kind] || kind

export const buildChartPoints = (buckets, key, width = 760, height = 180) => {
  if (!Array.isArray(buckets) || !buckets.length) return ''
  const max = Math.max(1, ...buckets.map(item => Number(item?.[key] || 0)))
  const step = buckets.length === 1 ? 0 : width / (buckets.length - 1)
  return buckets.map((item, index) => {
    const x = index * step
    const y = height - (Number(item?.[key] || 0) / max) * height
    return `${x.toFixed(1)},${y.toFixed(1)}`
  }).join(' ')
}

export const binlogProgressPercent = progress => {
  const total = Number(progress?.files_total || 0)
  const completed = Number(progress?.files_completed || 0)
  if (!total) return progress?.phase === 'completed' ? 100 : 8
  return Math.max(2, Math.min(100, Math.round(completed / total * 100)))
}

export default {
  name: 'BinlogAnalysis',
  props: {
    instances: { type: Array, default: () => [] },
    clusterName: { type: String, default: '' }
  },
  setup(props) {
    const end = new Date(), start = new Date(end.getTime() - 60 * 60 * 1000)
    const form = ref({
      instance: '', start_time: localInputTime(start), end_time: localInputTime(end),
      start_file: '', big_txn_mode: 'rows', big_txn_rows_threshold: 1000, big_txn_bytes_mb: 64
    })
    const tasks = ref([]), activeTask = ref(null), loading = ref(false), error = ref('')
    const resultTab = ref('tables'), keyword = ref('')
    let pollTimer = null

    const instanceOptions = computed(() => props.instances.map(item => ({
      key: `${value(item, 'MachineID', 'machine_id')}:${value(item, 'Port', 'port')}`,
      machineID: value(item, 'MachineID', 'machine_id'),
      name: value(item, 'MachineName', 'machine_name') || value(item, 'MachineIP', 'machine_ip'),
      ip: value(item, 'MachineIP', 'machine_ip'),
      port: Number(value(item, 'Port', 'port')),
      version: value(item, 'Version', 'version')
    })).filter(item => item.machineID && item.port))
    watch(instanceOptions, items => {
      if (!items.some(item => item.key === form.value.instance)) form.value.instance = items[0]?.key || ''
    }, { immediate: true })

    const result = computed(() => activeTask.value?.result || null)
    const summary = computed(() => result.value?.summary || activeTask.value?.summary || null)
    const progress = computed(() => activeTask.value?.progress || {})
    const progressPercent = computed(() => binlogProgressPercent(progress.value))
    const isRunning = computed(() => ['queued', 'running'].includes(activeTask.value?.status))
    const maxBucketRows = computed(() => Math.max(0, ...(result.value?.buckets || []).map(item => Number(item.total_rows || 0))))
    const chartLines = computed(() => ({
      insert: buildChartPoints(result.value?.buckets, 'insert_rows'),
      update: buildChartPoints(result.value?.buckets, 'update_rows'),
      delete: buildChartPoints(result.value?.buckets, 'delete_rows')
    }))
    const chartAxisLabels = computed(() => {
      const buckets = result.value?.buckets || []
      if (!buckets.length) return []
      const indexes = [...new Set([0, Math.floor((buckets.length - 1) / 2), buckets.length - 1])]
      return indexes.map(index => ({ left: buckets.length === 1 ? 0 : index / (buckets.length - 1) * 100, label: new Date(buckets[index].start).toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false }) }))
    })
    const filteredTables = computed(() => {
      const text = keyword.value.trim().toLowerCase()
      return (result.value?.tables || []).filter(item => !text || `${item.schema}.${item.table}`.toLowerCase().includes(text))
    })
    const filteredBigTransactions = computed(() => {
      const text = keyword.value.trim().toLowerCase()
      return (result.value?.big_transactions || []).filter(item => !text || `${item.gtid} ${(item.tables || []).join(' ')}`.toLowerCase().includes(text))
    })
    const filteredDDLEvents = computed(() => {
      const text = keyword.value.trim().toLowerCase()
      return (result.value?.ddl_events || []).filter(item => !text || `${item.schema}.${item.object} ${item.statement}`.toLowerCase().includes(text))
    })
    const filteredDMLEvents = computed(() => {
      const text = keyword.value.trim().toLowerCase()
      return (result.value?.dml_events || []).filter(item => !text || `${item.schema}.${item.table} ${item.gtid}`.toLowerCase().includes(text)).slice(0, 500)
    })

    const loadTasks = async () => {
      const payload = await request('/mysql/binlog-analysis')
      tasks.value = payload.items || []
      if (!activeTask.value && tasks.value.length) await chooseTask(tasks.value[0])
    }
    const stopPolling = () => { clearTimeout(pollTimer); pollTimer = null }
    const pollTask = async id => {
      stopPolling()
      try {
        const task = await request(`/mysql/binlog-analysis/${encodeURIComponent(id)}`)
        activeTask.value = task
        if (['queued', 'running'].includes(task.status)) pollTimer = setTimeout(() => pollTask(id), 1200)
        else await loadTaskListOnly()
      } catch (err) {
        error.value = err.message
      }
    }
    const loadTaskListOnly = async () => {
      const payload = await request('/mysql/binlog-analysis')
      tasks.value = payload.items || []
    }
    const chooseTask = async task => {
      if (!task?.id) return
      error.value = ''
      await pollTask(task.id)
    }
    const submit = async () => {
      error.value = ''
      const instance = instanceOptions.value.find(item => item.key === form.value.instance)
      if (!instance) { error.value = '请选择要分析的 MySQL 实例'; return }
      if (!form.value.start_time || !form.value.end_time) { error.value = '请选择完整的分析时间范围'; return }
      loading.value = true
      try {
        const task = await request('/mysql/binlog-analysis', {
          method: 'POST',
          body: JSON.stringify({
            machine_id: instance.machineID, port: instance.port,
            start_time: form.value.start_time, end_time: form.value.end_time,
            start_file: form.value.start_file.trim(), big_txn_mode: form.value.big_txn_mode,
            big_txn_rows_threshold: form.value.big_txn_mode === 'rows' ? Number(form.value.big_txn_rows_threshold || 0) : 0,
            big_txn_bytes_threshold: form.value.big_txn_mode === 'bytes' ? Math.round(Number(form.value.big_txn_bytes_mb || 0) * 1024 * 1024) : 0
          })
        })
        activeTask.value = task
        resultTab.value = 'tables'
        await loadTaskListOnly()
        pollTimer = setTimeout(() => pollTask(task.id), 500)
      } catch (err) {
        error.value = err.message
      } finally {
        loading.value = false
      }
    }
    const cancelTask = async () => {
      if (!activeTask.value?.id || !isRunning.value) return
      try {
        activeTask.value = await request(`/mysql/binlog-analysis/${encodeURIComponent(activeTask.value.id)}`, { method: 'DELETE' })
        stopPolling()
        await loadTaskListOnly()
      } catch (err) {
        error.value = err.message
      }
    }
    const useRecentRange = minutes => {
      const rangeEnd = new Date(), rangeStart = new Date(rangeEnd.getTime() - minutes * 60000)
      form.value.start_time = localInputTime(rangeStart)
      form.value.end_time = localInputTime(rangeEnd)
    }
    const downloadJSON = () => {
      if (!result.value) return
      const blob = new Blob([JSON.stringify(result.value, null, 2)], { type: 'application/json;charset=utf-8' })
      const link = document.createElement('a')
      link.href = URL.createObjectURL(blob)
      link.download = `binlog-analysis-${activeTask.value.id}.json`
      link.click()
      URL.revokeObjectURL(link.href)
    }

    onMounted(() => loadTasks().catch(err => { error.value = err.message }))
    onUnmounted(stopPolling)
    return {
      form, tasks, activeTask, loading, error, resultTab, keyword, instanceOptions,
      result, summary, progress, progressPercent, isRunning, maxBucketRows, chartLines, chartAxisLabels,
      filteredTables, filteredBigTransactions, filteredDDLEvents, filteredDMLEvents,
      submit, cancelTask, chooseTask, useRecentRange, downloadJSON,
      formatNumber, formatBytes, formatTime, formatDurationMicros, taskStatusLabel, operationLabel
    }
  },
  template: `
    <main class="binlog-analysis-page">
      <section class="binlog-hero">
        <div><p>BINLOG INTELLIGENCE</p><h3>Binlog 分析</h3><span>从已纳管实例读取 Binlog，定位写入峰值、热点表、DDL 变更与大事务。</span></div>
        <div class="binlog-hero-stats">
          <article><small>分析范围</small><b>{{ summary ? formatNumber(summary.files_analyzed) : '—' }}</b><span>Binlog 文件</span></article>
          <article><small>影响行数</small><b>{{ summary ? formatNumber(summary.total_rows) : '—' }}</b><span>DML rows</span></article>
          <article class="risk"><small>大事务</small><b>{{ summary ? formatNumber(summary.big_txn_count) : '—' }}</b><span>需重点检查</span></article>
        </div>
      </section>

      <div v-if="error" class="binlog-alert"><b>分析请求未完成</b><span>{{ error }}</span><button type="button" @click="error=''">×</button></div>

      <section class="binlog-workbench">
        <form class="instance-panel binlog-config-card" @submit.prevent="submit">
          <header><div><span>01</span><section><h4>配置分析范围</h4><p>账号由平台安全注入，仅建立只读复制流，不执行任何 SQL 变更。</p></section></div><em>READ ONLY</em></header>
          <div class="binlog-form-grid">
            <label class="wide">目标实例<select v-model="form.instance" required><option value="">请选择实例</option><option v-for="item in instanceOptions" :key="item.key" :value="item.key">{{ item.name }} · {{ item.ip }}:{{ item.port }} · MySQL {{ item.version || '未知' }}</option></select></label>
            <label>开始时间<input v-model="form.start_time" type="datetime-local" required></label>
            <label>结束时间<input v-model="form.end_time" type="datetime-local" required></label>
            <div class="binlog-quick-range wide"><span>快速范围</span><button type="button" @click="useRecentRange(30)">近 30 分钟</button><button type="button" @click="useRecentRange(60)">近 1 小时</button><button type="button" @click="useRecentRange(360)">近 6 小时</button><button type="button" @click="useRecentRange(1440)">近 24 小时</button></div>
            <label class="wide">起始 Binlog 文件（可选）<input v-model.trim="form.start_file" placeholder="例如 mysql-bin.000810；云数据库权限受限时手动指定"><small>留空时按时间自动二分定位，单次范围最长 7 天。</small></label>
          </div>
          <section class="binlog-threshold">
            <div><b>大事务识别</b><small>选择一种判定方式；设置为 0 可关闭行数模式检测。</small></div>
            <label :class="{active:form.big_txn_mode==='rows'}"><input v-model="form.big_txn_mode" type="radio" value="rows"><span><b>按影响行数</b><small>适合定位批量写入</small></span></label>
            <label :class="{active:form.big_txn_mode==='bytes'}"><input v-model="form.big_txn_mode" type="radio" value="bytes"><span><b>按事务字节</b><small>依赖 GTID transaction_length</small></span></label>
            <label class="threshold-input"><span>{{ form.big_txn_mode==='rows' ? '行数阈值' : '容量阈值（MB）' }}</span><input v-if="form.big_txn_mode==='rows'" v-model.number="form.big_txn_rows_threshold" type="number" min="0"><input v-else v-model.number="form.big_txn_bytes_mb" type="number" min="1"></label>
          </section>
          <div class="binlog-safety-note"><i>i</i><span><b>生产环境建议从短时间范围开始</b><small>分析会读取 Binlog 并占用网络与 CPU；平台会按 Manager 当前资源自动降低并发。</small></span></div>
          <footer><span>当前集群：{{ clusterName || '未命名集群' }}</span><button class="primary" :disabled="loading || isRunning">{{ loading ? '正在创建…' : isRunning ? '已有任务运行中' : '开始分析' }}</button></footer>
        </form>

        <aside class="instance-panel binlog-history">
          <header><div><span>02</span><section><h4>最近分析</h4><p>保留最近 50 次任务状态。</p></section></div><b>{{ tasks.length }}</b></header>
          <div class="binlog-history-list">
            <button v-for="task in tasks" :key="task.id" type="button" :class="{active:activeTask?.id===task.id}" @click="chooseTask(task)">
              <i :class="task.status"></i><span><b>{{ task.request.machine_name || task.request.machine_ip }}:{{ task.request.port }}</b><small>{{ formatTime(task.request.start_time) }} → {{ formatTime(task.request.end_time) }}</small></span><em>{{ taskStatusLabel(task.status) }}</em>
            </button>
            <div v-if="!tasks.length" class="binlog-history-empty"><i>↗</i><b>暂无分析记录</b><span>完成左侧配置后，任务会出现在这里。</span></div>
          </div>
        </aside>
      </section>

      <section v-if="activeTask && isRunning" class="instance-panel binlog-progress-card">
        <header><div><span class="binlog-pulse"></span><section><b>{{ taskStatusLabel(activeTask.status) }}</b><small>{{ progress.message || '正在准备分析任务' }}</small></section></div><button type="button" class="danger-link" @click="cancelTask">取消分析</button></header>
        <div class="binlog-progress-track"><i :style="{width:progressPercent+'%'}"></i></div>
        <footer><span>{{ progress.current_file || '等待分配 Binlog 文件' }}</span><span>{{ progress.files_completed || 0 }} / {{ progress.files_total || '—' }} 文件</span><span>{{ formatNumber(progress.events_processed) }} 事件</span><span>{{ progress.workers || 1 }} 并发</span></footer>
      </section>

      <section v-else-if="activeTask?.status==='failed'" class="instance-panel binlog-failed"><i>!</i><div><b>本次分析失败</b><p>{{ activeTask.error || progress.message }}</p></div><button class="secondary" type="button" @click="activeTask=null">重新配置</button></section>

      <template v-if="result">
        <section class="binlog-kpis">
          <article class="total"><span>总影响行数</span><b>{{ formatNumber(summary.total_rows) }}</b><small>{{ formatNumber(summary.dml_event_count) }} 个 DML 事件</small></article>
          <article class="insert"><span>INSERT</span><b>{{ formatNumber(summary.insert_rows) }}</b><small>{{ summary.total_rows ? (summary.insert_rows/summary.total_rows*100).toFixed(1) : 0 }}%</small></article>
          <article class="update"><span>UPDATE</span><b>{{ formatNumber(summary.update_rows) }}</b><small>{{ summary.total_rows ? (summary.update_rows/summary.total_rows*100).toFixed(1) : 0 }}%</small></article>
          <article class="delete"><span>DELETE</span><b>{{ formatNumber(summary.delete_rows) }}</b><small>{{ summary.total_rows ? (summary.delete_rows/summary.total_rows*100).toFixed(1) : 0 }}%</small></article>
          <article class="ddl"><span>DDL 变更</span><b>{{ formatNumber(summary.ddl_count) }}</b><small>结构操作</small></article>
          <article class="big"><span>大事务</span><b>{{ formatNumber(summary.big_txn_count) }}</b><small>{{ summary.big_txn_mode==='bytes' ? formatBytes(summary.big_txn_bytes_threshold) : formatNumber(summary.big_txn_rows_threshold)+' 行阈值' }}</small></article>
        </section>

        <section class="instance-panel binlog-chart-card">
          <header><div><h4>写入趋势</h4><p>按 {{ Math.round(summary.bucket_seconds/60) || 1 }} 分钟聚合，观察写入峰值与操作结构。</p></div><div class="binlog-chart-legend"><span class="insert">INSERT</span><span class="update">UPDATE</span><span class="delete">DELETE</span></div></header>
          <div class="binlog-chart-shell">
            <div class="binlog-y-axis"><span>{{ formatNumber(maxBucketRows) }}</span><span>{{ formatNumber(Math.round(maxBucketRows/2)) }}</span><span>0</span></div>
            <div class="binlog-chart">
              <i class="grid top"></i><i class="grid middle"></i><i class="grid bottom"></i>
              <svg viewBox="0 0 760 180" preserveAspectRatio="none" aria-label="Binlog DML 写入趋势">
                <polyline class="insert" :points="chartLines.insert"></polyline>
                <polyline class="update" :points="chartLines.update"></polyline>
                <polyline class="delete" :points="chartLines.delete"></polyline>
              </svg>
              <div class="binlog-x-axis"><span v-for="item in chartAxisLabels" :key="item.left" :style="{left:item.left+'%'}">{{ item.label }}</span></div>
            </div>
          </div>
        </section>

        <section class="instance-panel binlog-detail-card">
          <header><nav><button :class="{active:resultTab==='tables'}" @click="resultTab='tables'">热点表 <span>{{ result.tables.length }}</span></button><button :class="{active:resultTab==='big'}" @click="resultTab='big'">大事务 <span>{{ result.big_transactions.length }}</span></button><button :class="{active:resultTab==='ddl'}" @click="resultTab='ddl'">DDL 变更 <span>{{ result.ddl_events.length }}</span></button><button :class="{active:resultTab==='events'}" @click="resultTab='events'">DML 明细 <span>{{ result.dml_events.length }}</span></button></nav><div><label><span>⌕</span><input v-model.trim="keyword" placeholder="搜索库表、GTID 或 SQL"></label><button type="button" class="secondary" @click="downloadJSON">⇩ 导出 JSON</button></div></header>
          <div class="binlog-result-table">
            <table v-if="resultTab==='tables'"><thead><tr><th>#</th><th>库表</th><th>总影响行数</th><th>INSERT</th><th>UPDATE</th><th>DELETE</th><th>DDL</th><th>写入占比</th></tr></thead><tbody><tr v-for="(item,index) in filteredTables" :key="item.schema+'.'+item.table"><td>{{ index+1 }}</td><td><b>{{ item.schema }}.{{ item.table }}</b></td><td><b>{{ formatNumber(item.total_rows) }}</b></td><td class="insert">{{ formatNumber(item.insert_rows) }}</td><td class="update">{{ formatNumber(item.update_rows) }}</td><td class="delete">{{ formatNumber(item.delete_rows) }}</td><td>{{ formatNumber(item.ddl_count) }}</td><td><div class="binlog-share"><i :style="{width:(summary.total_rows ? item.total_rows/summary.total_rows*100 : 0)+'%'}"></i></div><small>{{ summary.total_rows ? (item.total_rows/summary.total_rows*100).toFixed(1) : 0 }}%</small></td></tr><tr v-if="!filteredTables.length"><td colspan="8" class="empty">没有匹配的热点表。</td></tr></tbody></table>
            <table v-else-if="resultTab==='big'"><thead><tr><th>开始时间</th><th>GTID / Binlog</th><th>影响行数</th><th>事务大小</th><th>历史复制延迟</th><th>操作构成</th><th>涉及表</th></tr></thead><tbody><tr v-for="item in filteredBigTransactions" :key="item.gtid+item.start_time"><td><b>{{ formatTime(item.start_time) }}</b><small>结束 {{ formatTime(item.end_time) }}</small></td><td><b class="mono">{{ item.gtid || '无 GTID' }}</b><small>{{ item.binlog_file }}</small></td><td><b>{{ formatNumber(item.row_count) }}</b></td><td>{{ formatBytes(item.transaction_length) }}</td><td><b>{{ formatDurationMicros(item.replication_delay_micros) }}</b><small v-if="item.original_commit_time">原始提交 {{ formatTime(item.original_commit_time) }}</small></td><td><span class="binlog-op insert">I {{ formatNumber(item.insert_rows) }}</span><span class="binlog-op update">U {{ formatNumber(item.update_rows) }}</span><span class="binlog-op delete">D {{ formatNumber(item.delete_rows) }}</span></td><td><b>{{ (item.tables || []).slice(0,3).join(', ') || '—' }}</b><small v-if="item.tables?.length>3">另有 {{ item.tables.length-3 }} 张表</small></td></tr><tr v-if="!filteredBigTransactions.length"><td colspan="7" class="empty">当前阈值下没有识别到大事务。</td></tr></tbody></table>
            <table v-else-if="resultTab==='ddl'"><thead><tr><th>执行时间</th><th>操作</th><th>对象</th><th>SQL</th><th>Binlog / GTID</th></tr></thead><tbody><tr v-for="item in filteredDDLEvents" :key="item.binlog_file+item.time+item.statement"><td>{{ formatTime(item.time) }}</td><td><span class="binlog-ddl-badge">{{ operationLabel(item.type) }}</span></td><td><b>{{ item.schema || '—' }}<template v-if="item.object">.{{ item.object }}</template></b></td><td><code>{{ item.statement }}</code></td><td><b>{{ item.binlog_file }}</b><small class="mono">{{ item.gtid || '无 GTID' }}</small></td></tr><tr v-if="!filteredDDLEvents.length"><td colspan="5" class="empty">分析范围内没有 DDL 变更。</td></tr></tbody></table>
            <table v-else><thead><tr><th>事件时间</th><th>操作</th><th>库表</th><th>影响行数</th><th>Binlog / GTID</th></tr></thead><tbody><tr v-for="item in filteredDMLEvents" :key="item.binlog_file+item.time+item.schema+item.table"><td>{{ formatTime(item.time) }}</td><td><span :class="['binlog-op',item.type.toLowerCase()]">{{ item.type }}</span></td><td><b>{{ item.schema }}.{{ item.table }}</b></td><td>{{ formatNumber(item.row_count) }}</td><td><b>{{ item.binlog_file }}</b><small class="mono">{{ item.gtid || '无 GTID' }}</small></td></tr><tr v-if="!filteredDMLEvents.length"><td colspan="5" class="empty">没有匹配的 DML 事件。</td></tr></tbody></table>
          </div>
          <footer><span>已分析 {{ summary.files_analyzed }} 个文件 · {{ formatTime(summary.start_time) }} 至 {{ formatTime(summary.end_time) }}</span><span v-if="summary.dml_truncated || summary.ddl_truncated">为控制内存，明细已截断；汇总统计不受影响。</span></footer>
        </section>
      </template>

      <section v-else-if="!activeTask" class="binlog-empty-state">
        <div><span></span><i>↗</i></div><p>ANALYSIS READY</p><h4>从一个短时间窗口开始</h4><span>选择实例和时间范围，系统会自动定位 Binlog 并生成写入趋势、热点表与大事务报告。</span>
      </section>
    </main>`
}
