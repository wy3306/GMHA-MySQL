import { computed, ref, watch } from 'vue/dist/vue.esm-bundler.js'

const request = async (path, options = {}) => {
  const response = await fetch(`/api/v1${path}`, { headers: { 'Content-Type': 'application/json' }, ...options })
  const payload = await response.json().catch(() => ({}))
  if (!response.ok) throw new Error(payload.error || '请求失败')
  return payload
}

const field = (item, upper, lower) => item?.[upper] ?? item?.[lower]
const normalized = value => String(value || '').toLowerCase()
const taskStatus = detail => normalized(field(detail?.task, 'Status', 'status'))
const taskID = detail => field(detail?.task, 'ID', 'id')

export const archiveConfirmation = form =>
  `${String(form?.source_schema || '').trim()}.${String(form?.source_table || '').trim()}->${String(form?.destination_schema || '').trim()}.${String(form?.destination_table || '').trim()}`

export const archiveRequestFingerprint = form => JSON.stringify([
  form?.target, form?.source_schema, form?.source_table, form?.destination_schema, form?.destination_table,
  form?.where, form?.index, Number(form?.batch_size || 0), Number(form?.sleep_seconds || 0),
  Number(form?.run_time_seconds || 0), Boolean(form?.delete_source)
])

export const parseArchiveTaskDetail = detail => {
  const result = {
    eligibleRows: null, eligibleRowsCapped: false, source: null, destinationExists: null, destinationMissing: false,
    remainingRows: null, destinationRows: null, selected: null, inserted: null, deleted: null
  }
  for (const event of detail?.events || []) {
    for (const line of String(field(event, 'Content', 'content') || '').split('\n')) {
      const parts = line.trim().split('\t')
      if (parts[0] === 'GMHA_ARCHIVE_SOURCE') {
        result.eligibleRows = Number(parts[3] || 0)
        result.eligibleRowsCapped = String(parts[4] || '').toUpperCase() === 'YES'
      }
      if (parts[0] === 'GMHA_ARCHIVE_TABLE') {
        result.source = {
          schema: parts[1], table: parts[2], engine: parts[3], estimatedRows: Number(parts[4] || 0),
          dataBytes: Number(parts[5] || 0), indexBytes: Number(parts[6] || 0), primaryKeys: Number(parts[7] || 0)
        }
      }
      if (parts[0] === 'GMHA_ARCHIVE_DESTINATION') result.destinationExists = Number(parts[3] || 0) > 0
      if (parts[0] === 'GMHA_ARCHIVE_DESTINATION_MISSING') result.destinationMissing = true
      if (parts[0] === 'GMHA_ARCHIVE_REMAINING') result.remainingRows = Number(parts[3] || 0)
      if (parts[0] === 'GMHA_ARCHIVE_DESTINATION_ROWS') result.destinationRows = Number(parts[3] || 0)
      const stat = line.trim().match(/^(SELECT|INSERT|DELETE)\s+(\d+)$/i)
      if (stat) result[({ select: 'selected', insert: 'inserted', delete: 'deleted' })[stat[1].toLowerCase()]] = Number(stat[2])
    }
  }
  return result
}

const formatNumber = value => value === null || value === undefined ? '—' : Number(value).toLocaleString()
const formatEligibleRows = result => result?.eligibleRowsCapped ? `≥ ${formatNumber(result.eligibleRows)}` : formatNumber(result?.eligibleRows)
const formatBytes = raw => {
  let value = Number(raw || 0)
  if (!Number.isFinite(value) || value <= 0) return '—'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let unit = 0
  while (value >= 1024 && unit < units.length - 1) { value /= 1024; unit++ }
  return `${value >= 100 || unit === 0 ? value.toFixed(0) : value.toFixed(1)} ${units[unit]}`
}

export default {
  name: 'ArchiveManagement',
  props: { instances: { type: Array, default: () => [] } },
  emits: ['open-task'],
  setup(props, { emit }) {
    const form = ref({
      target: '', source_schema: '', source_table: '', destination_schema: 'archive', destination_table: '',
      where: '', index: 'PRIMARY', batch_size: 1000, sleep_seconds: 1, run_time_seconds: 3600,
      delete_source: true, risk_acknowledged: false, confirmation: ''
    })
    const busy = ref(false), error = ref(''), notice = ref('')
    const preview = ref({ fingerprint: '', detail: null, result: null })
    const progress = ref({ task: null, id: '', status: '', percent: 0, step: '' })

    const instanceOptions = computed(() => (props.instances || []).map(item => ({
      value: `${field(item, 'MachineIP', 'machine_ip')}:${field(item, 'Port', 'port')}`,
      label: `${field(item, 'MachineName', 'machine_name') || field(item, 'MachineIP', 'machine_ip')} · ${field(item, 'MachineIP', 'machine_ip')}:${field(item, 'Port', 'port')}`
    })))
    watch(instanceOptions, options => {
      if (!options.some(option => option.value === form.value.target)) form.value.target = options[0]?.value || ''
    }, { immediate: true })
    watch(() => form.value.source_table, value => {
      if (!form.value.destination_table) form.value.destination_table = value ? `${value}_archive` : ''
    })

    const fingerprint = computed(() => archiveRequestFingerprint(form.value))
    const previewReady = computed(() => Boolean(preview.value.detail) && preview.value.fingerprint === fingerprint.value && taskStatus(preview.value.detail) === 'success')
    const confirmationTarget = computed(() => archiveConfirmation(form.value))
    const executeReady = computed(() =>
      previewReady.value && form.value.risk_acknowledged && form.value.confirmation === confirmationTarget.value && !busy.value
    )
    const modeLabel = computed(() => form.value.delete_source ? '迁移（复制成功后删除源行）' : '仅复制（保留源行）')

    const parseTarget = () => {
      const separator = form.value.target.lastIndexOf(':')
      if (separator <= 0) throw new Error('请选择 MySQL 实例')
      return { machine: form.value.target.slice(0, separator), port: Number(form.value.target.slice(separator + 1)) }
    }
    const payload = action => ({
      ...parseTarget(), action,
      source_schema: form.value.source_schema.trim(), source_table: form.value.source_table.trim(),
      destination_schema: form.value.destination_schema.trim(), destination_table: form.value.destination_table.trim(),
      where: form.value.where.trim(), index: form.value.index.trim(),
      batch_size: Number(form.value.batch_size), sleep_seconds: Number(form.value.sleep_seconds),
      run_time_seconds: Number(form.value.run_time_seconds || 0), delete_source: Boolean(form.value.delete_source),
      risk_acknowledged: action === 'execute' && form.value.risk_acknowledged,
      confirmation: action === 'execute' ? form.value.confirmation.trim() : ''
    })
    const updateProgress = detail => {
      progress.value = {
        task: detail, id: taskID(detail), status: taskStatus(detail),
        percent: Number(field(detail?.task, 'ProgressPercent', 'progress_percent') || 0),
        step: field(detail?.task, 'CurrentStep', 'current_step') || '等待 Agent 接收'
      }
    }
    const failure = detail => {
      const failedEvent = [...(detail?.events || [])].reverse().find(event => normalized(field(event, 'EventType', 'event_type')) === 'error')
      const failedStep = (detail?.steps || []).find(step => normalized(field(step, 'Status', 'status')) === 'failed')
      return field(failedEvent, 'Content', 'content') || field(failedStep, 'Message', 'message') || '归档任务执行失败'
    }
    const waitTask = async created => {
      const id = taskID(created)
      if (!id) throw new Error('任务创建失败：未返回任务 ID')
      updateProgress(created)
      for (let attempt = 0; attempt < 900; attempt++) {
        const detail = await request(`/tasks?id=${encodeURIComponent(id)}`)
        updateProgress(detail)
        if (['success', 'failed'].includes(taskStatus(detail))) return detail
        await new Promise(resolve => setTimeout(resolve, 1000))
      }
      throw new Error('等待归档任务超时，请到任务中心继续查看')
    }
    const run = async action => {
      busy.value = true
      error.value = ''
      notice.value = ''
      try {
        const submittedFingerprint = fingerprint.value
        const created = await request('/tasks/mysql-archive', { method: 'POST', body: JSON.stringify(payload(action)) })
        const detail = await waitTask(created)
        if (taskStatus(detail) !== 'success') throw new Error(failure(detail))
        const result = parseArchiveTaskDetail(detail)
        if (action === 'dry_run') {
          preview.value = { fingerprint: submittedFingerprint, detail, result }
          notice.value = `预检完成：筛选条件当前匹配 ${formatEligibleRows(result)} 行`
        } else {
          notice.value = `归档完成：PT 插入 ${formatNumber(result.inserted)} 行，删除 ${formatNumber(result.deleted)} 行`
          preview.value = { fingerprint: '', detail: null, result: null }
          form.value.risk_acknowledged = false
          form.value.confirmation = ''
        }
      } catch (err) {
        error.value = err.message
      } finally {
        busy.value = false
      }
    }
    const previewArchive = () => run('dry_run')
    const executeArchive = () => run('execute')
    const openTask = () => { if (progress.value.task) emit('open-task', progress.value.task) }

    return {
      form, busy, error, notice, preview, progress, instanceOptions, fingerprint, previewReady, confirmationTarget,
      executeReady, modeLabel, formatNumber, formatEligibleRows, formatBytes, previewArchive, executeArchive, openTask
    }
  },
  template: `
    <main class="archive-management-page">
      <header class="archive-hero">
        <div><p>DATA ARCHIVING</p><h3>数据库归档</h3><span>使用 Percona pt-archiver 分批搬迁历史数据，先预检查询路径与目标表，再执行可审计归档任务。</span></div>
        <label>目标实例<select v-model="form.target"><option value="">请选择实例</option><option v-for="option in instanceOptions" :key="option.value" :value="option.value">{{ option.label }}</option></select></label>
      </header>
      <div v-if="error" class="archive-message error"><b>操作未完成</b><span>{{ error }}</span><button @click="error=''">×</button></div>
      <div v-if="notice" class="archive-message success"><b>操作完成</b><span>{{ notice }}</span><button @click="notice=''">×</button></div>
      <section v-if="progress.task" :class="['archive-progress',progress.status]">
        <header><div><b>{{ progress.status==='success' ? '任务已完成' : progress.status==='failed' ? '任务失败' : '归档任务执行中' }}</b><small>{{ progress.id }}</small></div><button class="text-button" @click="openTask">查看任务详情</button></header>
        <div><i :style="{width:progress.percent+'%'}"></i></div><footer><span>{{ progress.step }}</span><b>{{ progress.percent }}%</b></footer>
      </section>
      <div class="archive-layout">
        <section class="archive-form-card">
          <header><div><b>1. 定义归档范围</b><span>筛选条件必须限定历史数据，平台拒绝 1=1、注释和数据变更语句。</span></div><em>PT ARCHIVER</em></header>
          <div class="archive-form-grid">
            <label>源数据库<input v-model.trim="form.source_schema" required placeholder="例如 app"></label>
            <label>源数据表<input v-model.trim="form.source_table" required placeholder="例如 orders"></label>
            <label>归档数据库<input v-model.trim="form.destination_schema" required placeholder="例如 archive"></label>
            <label>归档数据表<input v-model.trim="form.destination_table" required placeholder="例如 orders_archive"></label>
            <label class="wide">归档条件（不含 WHERE）<textarea v-model.trim="form.where" required placeholder="例如 created_at < NOW() - INTERVAL 180 DAY"></textarea><small>应命中可前向扫描的索引范围；预检结果会显示当前匹配行数。</small></label>
            <label>扫描索引<input v-model.trim="form.index" placeholder="默认 PRIMARY"></label>
            <label>单批行数<input v-model.number="form.batch_size" type="number" min="1" max="100000"></label>
            <label>批次间隔（秒）<input v-model.number="form.sleep_seconds" type="number" min="0" max="60"></label>
            <label>单次最长执行（秒）<input v-model.number="form.run_time_seconds" type="number" min="0" max="86400"><small>0 表示不限制；可重复执行直至匹配行为 0。</small></label>
          </div>
          <label class="archive-mode"><input v-model="form.delete_source" type="checkbox"><span><b>归档后删除源数据</b><small>PT 仅在目标表插入成功后删除对应源行；关闭时只复制，源表数据保持不变。</small></span></label>
          <footer><span>当前模式：<b>{{ modeLabel }}</b></span><button class="primary" :disabled="busy || !form.target" @click="previewArchive">{{ busy ? '任务执行中…' : '运行归档预检' }}</button></footer>
        </section>
        <aside class="archive-safety-card">
          <header><b>2. 预检与执行门禁</b><span :class="{ready:previewReady}">{{ previewReady ? '预检已通过' : '等待预检' }}</span></header>
          <div v-if="preview.result" class="archive-preview-result">
            <article><small>当前匹配</small><b>{{ formatEligibleRows(preview.result) }} 行</b><span>{{ preview.result.eligibleRowsCapped ? '预检最多扫描 100,000 行 · ' : '' }}{{ preview.result.source?.engine || '未知引擎' }} · 表估算 {{ formatNumber(preview.result.source?.estimatedRows) }} 行</span></article>
            <article><small>源表空间</small><b>{{ formatBytes((preview.result.source?.dataBytes || 0) + (preview.result.source?.indexBytes || 0)) }}</b><span>主键 {{ preview.result.source?.primaryKeys ? '已检测' : '未检测到' }}</span></article>
            <article><small>归档目标</small><b>{{ preview.result.destinationExists ? '目标表已存在' : '执行时自动创建' }}</b><span>{{ form.destination_schema }}.{{ form.destination_table }}</span></article>
          </div>
          <div v-else class="archive-empty-preview"><i>i</i><p>预检会验证 PT 工具、源表、筛选结果、主键与归档目标。目标表不存在时，正式执行会使用 <code>CREATE TABLE LIKE</code> 按源表结构创建。</p></div>
          <ol>
            <li><i>1</i><span><b>检查范围</b><small>统计筛选行数并读取表结构和主键。</small></span></li>
            <li><i>2</i><span><b>准备目标</b><small>目标表不存在时按源表结构创建。</small></span></li>
            <li><i>3</i><span><b>PT dry-run</b><small>输出查询并检查源/目标列兼容性。</small></span></li>
            <li><i>4</i><span><b>分批归档</b><small>每批提交、限速、重试锁等待并输出统计。</small></span></li>
            <li><i>5</i><span><b>结果核验</b><small>复查源表剩余匹配行和归档表总行数。</small></span></li>
          </ol>
          <label class="archive-risk-ack"><input v-model="form.risk_acknowledged" type="checkbox" :disabled="!previewReady"><span><b>我已确认筛选条件、索引路径、空间和复制影响</b><small>正式迁移会写入归档表，并可能删除源表中的匹配数据。</small></span></label>
          <label class="archive-confirm">输入 <b>{{ confirmationTarget }}</b> 确认<input v-model.trim="form.confirmation" :disabled="!previewReady" autocomplete="off"></label>
          <button class="danger-button" :disabled="!executeReady" @click="executeArchive">{{ busy ? '归档执行中…' : form.delete_source ? '确认并执行数据迁移' : '确认并执行归档复制' }}</button>
        </aside>
      </div>
    </main>`
}
