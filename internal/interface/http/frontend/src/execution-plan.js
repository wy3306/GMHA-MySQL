import { computed, onMounted, ref, watch } from 'vue/dist/vue.esm-bundler.js'

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

const emptyPlan = () => ({ columns: [], rows: [], instance: null, generated_at: '' })
const fieldValue = (row, name) => {
  const key = Object.keys(row || {}).find(item => item.toLowerCase() === String(name).toLowerCase())
  return key ? row[key] : null
}
const numericValue = value => {
  const number = Number(value)
  return Number.isFinite(number) ? number : 0
}

export const summarizeExecutionPlan = rows => {
  const items = Array.isArray(rows) ? rows : []
  return items.reduce((summary, row) => {
    const accessType = String(fieldValue(row, 'type') || '').toUpperCase()
    const key = fieldValue(row, 'key')
    summary.estimatedRows += numericValue(fieldValue(row, 'rows'))
    if (key != null && String(key).trim()) summary.indexSteps++
    if (accessType === 'ALL' || accessType === 'INDEX') summary.scanSteps++
    return summary
  }, { steps: items.length, estimatedRows: 0, indexSteps: 0, scanSteps: 0 })
}

export const executionPlanAccessTone = type => {
  const value = String(type || '').toUpperCase()
  if (['SYSTEM', 'CONST', 'EQ_REF', 'REF'].includes(value)) return 'good'
  if (['RANGE', 'FULLTEXT', 'REF_OR_NULL', 'INDEX_MERGE'].includes(value)) return 'medium'
  if (['ALL', 'INDEX'].includes(value)) return 'risk'
  return 'neutral'
}

export const executionPlanColumnLabel = column => ({
  id: '步骤',
  select_type: '查询类型',
  table: '访问对象',
  partitions: '分区',
  type: '访问方式',
  possible_keys: '候选索引',
  key: '命中索引',
  key_len: '索引长度',
  ref: '索引引用',
  rows: '预估扫描行',
  filtered: '过滤比例',
  Extra: '优化器说明'
})[column] || column

export default {
  name: 'ExecutionPlan',
  props: {
    instances: { type: Array, default: () => [] },
    clusterName: { type: String, default: '' }
  },
  setup(props) {
    const selectedKey = ref('')
    const database = ref('')
    const sql = ref('')
    const loading = ref(false)
    const error = ref('')
    const notice = ref('')
    const plan = ref(emptyPlan())

    const normalizedInstances = computed(() => (props.instances || []).map(item => ({
      raw: item,
      machineID: item.MachineID || item.machine_id || '',
      machineName: item.MachineName || item.machine_name || item.MachineIP || item.machine_ip || '未命名机器',
      machineIP: item.MachineIP || item.machine_ip || '',
      port: Number(item.Port || item.port || 3306),
      version: item.Version || item.version || '版本待上报',
      role: item.Role || item.role || item.TopologyRole || item.topology_role || '',
      status: String(item.HeartbeatStatus || item.heartbeat_status || item.Status || item.status || '').toLowerCase()
    })).filter(item => item.machineID && item.port))
    const instanceKey = item => `${item.machineID}:${item.port}`
    const selectedInstance = computed(() => normalizedInstances.value.find(item => instanceKey(item) === selectedKey.value))
    const summary = computed(() => summarizeExecutionPlan(plan.value.rows))
    const hasPlan = computed(() => plan.value.columns.length > 0)

    function ensureSelectedInstance() {
      if (selectedInstance.value) return
      const preferred = normalizedInstances.value.find(item => /(ok|success|healthy|online|running)/.test(item.status)) || normalizedInstances.value[0]
      selectedKey.value = preferred ? instanceKey(preferred) : ''
    }
    watch(normalizedInstances, ensureSelectedInstance, { immediate: true })
    watch(selectedKey, () => {
      plan.value = emptyPlan()
      error.value = ''
      notice.value = ''
    })

    async function runExplain() {
      error.value = ''
      notice.value = ''
      if (!selectedInstance.value) {
        error.value = '请先选择一个可用的 MySQL 实例。'
        return
      }
      if (!sql.value.trim()) {
        error.value = '请输入需要分析的 SQL。'
        return
      }
      loading.value = true
      try {
        plan.value = await request('/sql-diagnostics/explain', {
          method: 'POST',
          body: JSON.stringify({
            machine_id: selectedInstance.value.machineID,
            port: selectedInstance.value.port,
            database: database.value.trim(),
            sql: sql.value
          })
        })
        notice.value = `已在 ${selectedInstance.value.machineName} · ${selectedInstance.value.machineIP}:${selectedInstance.value.port} 生成执行计划。`
      } catch (err) {
        plan.value = emptyPlan()
        error.value = err.message
      } finally {
        loading.value = false
      }
    }
    function editorShortcut(event) {
      if ((event.ctrlKey || event.metaKey) && event.key === 'Enter') {
        event.preventDefault()
        runExplain()
      }
    }
    function fillExample() {
      sql.value = 'SELECT o.id, o.status, o.created_at\nFROM orders AS o\nWHERE o.customer_id = 10001\n  AND o.created_at >= CURRENT_DATE - INTERVAL 7 DAY\nORDER BY o.created_at DESC\nLIMIT 50;'
    }
    function displayValue(row, column) {
      const value = fieldValue(row, column)
      if (value == null || value === '') return '—'
      if (String(column).toLowerCase() === 'filtered') return `${numericValue(value).toLocaleString('zh-CN', { maximumFractionDigits: 2 })}%`
      if (String(column).toLowerCase() === 'rows') return numericValue(value).toLocaleString('zh-CN')
      return String(value)
    }
    function columnClass(column) {
      return `plan-column-${String(column).toLowerCase().replace(/[^a-z0-9]+/g, '-')}`
    }
    function instanceState(item) {
      if (/(fail|error|offline|critical)/.test(item.status)) return { tone: 'error', label: '状态异常' }
      if (/(ok|success|healthy|online|running)/.test(item.status)) return { tone: 'healthy', label: '运行正常' }
      return { tone: 'unknown', label: '等待上报' }
    }
    function formatTime(value) {
      return value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '—'
    }

    onMounted(ensureSelectedInstance)
    return {
      selectedKey, database, sql, loading, error, notice, plan, normalizedInstances, selectedInstance,
      instanceKey, summary, hasPlan, runExplain, editorShortcut, fillExample, displayValue,
      columnClass, instanceState, formatTime, executionPlanAccessTone, executionPlanColumnLabel, fieldValue
    }
  },
  template: `
    <main class="execution-plan-page">
      <section class="plan-hero">
        <div class="plan-hero-copy">
          <p>QUERY OPTIMIZER</p>
          <h3>查看执行计划</h3>
          <span>在指定实例上使用安全的 EXPLAIN 分析 SQL，快速识别全表扫描、索引命中与预估扫描行数。</span>
        </div>
        <label class="plan-instance-switch">
          <span>分析目标实例</span>
          <select v-model="selectedKey" :disabled="loading || !normalizedInstances.length" aria-label="切换执行计划分析实例">
            <option v-if="!normalizedInstances.length" value="">当前集群暂无可用实例</option>
            <option v-for="item in normalizedInstances" :key="instanceKey(item)" :value="instanceKey(item)">{{ item.machineName }} · {{ item.machineIP }}:{{ item.port }}</option>
          </select>
          <small v-if="selectedInstance">MySQL {{ selectedInstance.version }} · 切换实例会清空旧计划，保留 SQL 草稿</small>
        </label>
      </section>

      <div v-if="error" class="plan-message error" role="alert"><b>未能生成执行计划</b><span>{{ error }}</span></div>
      <div v-if="notice" class="plan-message success" role="status"><b>执行计划已更新</b><span>{{ notice }}</span></div>

      <section class="plan-workspace">
        <article class="instance-panel plan-editor-panel">
          <header class="plan-section-head">
            <div><span class="plan-step">01</span><div><h4>输入 SQL</h4><p>输入原始语句即可，系统会自动添加普通 EXPLAIN。</p></div></div>
            <button type="button" class="text-button" :disabled="loading" @click="fillExample">填入示例</button>
          </header>
          <label class="plan-database-field">
            <span>默认数据库 <em>可选</em></span>
            <input v-model.trim="database" :disabled="loading" maxlength="128" placeholder="例如：orders">
            <small>SQL 已使用 database.table 完整限定时可留空。</small>
          </label>
          <label class="plan-sql-editor">
            <span>SQL 语句</span>
            <textarea v-model="sql" :disabled="loading" maxlength="65536" spellcheck="false" placeholder="SELECT * FROM orders WHERE customer_id = 10001;" @keydown="editorShortcut"></textarea>
          </label>
          <footer class="plan-editor-actions">
            <div class="plan-safety-note"><i>✓</i><span><b>仅查看优化器计划</b><small>不会使用 EXPLAIN ANALYZE，也不会执行输入的业务语句。</small></span></div>
            <div><small>{{ sql.length.toLocaleString('zh-CN') }} / 65,536</small><button type="button" class="primary plan-run-button" :disabled="loading || !selectedInstance || !sql.trim()" @click="runExplain">{{ loading ? '正在分析…' : '生成执行计划' }}</button><kbd>⌘/Ctrl + Enter</kbd></div>
          </footer>
        </article>

        <aside class="instance-panel plan-target-panel">
          <header class="plan-section-head"><div><span class="plan-step">02</span><div><h4>当前分析目标</h4><p>确认实例后再生成计划。</p></div></div></header>
          <template v-if="selectedInstance">
            <div class="plan-target-identity"><span :class="['instance-health-dot',instanceState(selectedInstance).tone]"></span><div><b>{{ selectedInstance.machineName }}</b><small>{{ selectedInstance.machineID }}</small></div><em :class="instanceState(selectedInstance).tone">{{ instanceState(selectedInstance).label }}</em></div>
            <dl class="plan-target-details"><div><dt>访问地址</dt><dd>{{ selectedInstance.machineIP }}:{{ selectedInstance.port }}</dd></div><div><dt>MySQL 版本</dt><dd>{{ selectedInstance.version }}</dd></div><div><dt>默认数据库</dt><dd>{{ database || '由 SQL 完整限定' }}</dd></div><div><dt>集群</dt><dd>{{ clusterName || '—' }}</dd></div></dl>
          </template>
          <div v-else class="plan-target-empty"><i>!</i><b>暂无可用实例</b><span>请先在当前集群中创建并启动 MySQL 实例。</span></div>
        </aside>
      </section>

      <section v-if="hasPlan" class="plan-results">
        <header class="plan-results-head"><div><p>EXPLAIN RESULT</p><h3>优化器执行路径</h3><span>{{ plan.instance?.machine_name || selectedInstance?.machineName }} · {{ plan.instance?.machine_ip || selectedInstance?.machineIP }}:{{ plan.instance?.port || selectedInstance?.port }} · {{ formatTime(plan.generated_at) }}</span></div><span class="plan-result-database">{{ plan.database || '未指定默认数据库' }}</span></header>
        <div class="plan-summary-grid">
          <article><small>执行步骤</small><b>{{ summary.steps }}</b><span>优化器返回的访问路径</span></article>
          <article><small>预估扫描行</small><b>{{ summary.estimatedRows.toLocaleString('zh-CN') }}</b><span>各步骤 rows 估算合计</span></article>
          <article><small>命中索引</small><b>{{ summary.indexSteps }}</b><span>{{ summary.indexSteps ? '存在明确 key 选择' : '尚未发现索引命中' }}</span></article>
          <article :class="{risk:summary.scanSteps}"><small>全量扫描步骤</small><b>{{ summary.scanSteps }}</b><span>{{ summary.scanSteps ? '请关注 ALL / INDEX' : '未发现全量扫描' }}</span></article>
        </div>
        <article class="instance-panel plan-table-panel">
          <div class="plan-table-wrap"><table class="plan-table"><thead><tr><th v-for="column in plan.columns" :key="column" :class="columnClass(column)">{{ executionPlanColumnLabel(column) }}</th></tr></thead><tbody><tr v-for="(row,index) in plan.rows" :key="index"><td v-for="column in plan.columns" :key="column" :class="columnClass(column)"><span v-if="column.toLowerCase()==='type'" :class="['plan-access-badge',executionPlanAccessTone(fieldValue(row,column))]">{{ displayValue(row,column) }}</span><code v-else-if="['table','key','possible_keys','ref'].includes(column.toLowerCase())">{{ displayValue(row,column) }}</code><span v-else>{{ displayValue(row,column) }}</span></td></tr><tr v-if="!plan.rows.length"><td :colspan="plan.columns.length || 1" class="empty">MySQL 没有返回执行步骤。</td></tr></tbody></table></div>
          <footer class="plan-legend"><span><i class="good"></i>高效定位</span><span><i class="medium"></i>范围或索引合并</span><span><i class="risk"></i>全量扫描</span><small>执行计划是优化器估算结果，实际性能还会受到数据分布、缓存和并发影响。</small></footer>
        </article>
      </section>

      <section v-else class="plan-empty-state">
        <div class="plan-empty-visual"><span></span><span></span><span></span><i>EXPLAIN</i></div>
        <div><b>{{ loading ? '正在向目标实例请求优化器计划…' : '等待生成执行计划' }}</b><p>选择目标实例，输入 SQL 后即可查看访问方式、索引选择、预估扫描行和优化器说明。</p></div>
      </section>
    </main>
  `
}
