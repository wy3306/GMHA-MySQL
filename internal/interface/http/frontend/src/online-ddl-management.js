import { computed, onMounted, onUnmounted, ref, watch } from 'vue/dist/vue.esm-bundler.js'

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

export const buildOnlineDDLFingerprint = input => JSON.stringify({
  target: String(input.target || '').trim(),
  schema: String(input.schema || '').trim(),
  table: String(input.table || '').trim(),
  alter: String(input.alter || '').trim().replace(/\s+/g, ' '),
  purpose: String(input.purpose || '').trim(),
  impact: String(input.impact || '').trim(),
  max_load_threads_running: Number(input.max_load_threads_running || 0),
  critical_threads_running: Number(input.critical_threads_running || 0),
  max_lag_seconds: Number(input.max_lag_seconds || 0),
  chunk_time_seconds: Number(input.chunk_time_seconds || 0),
  check_interval_seconds: Number(input.check_interval_seconds || 0),
  alter_foreign_keys_method: String(input.alter_foreign_keys_method || '').trim()
})

export const parseOnlineDDLTaskDetail = detail => {
  const result = { target: null, load: null, verified: null }
  for (const event of detail?.events || []) {
    for (const line of String(field(event, 'Content', 'content') || '').split('\n')) {
      if (line.startsWith('GMHA_ONLINE_DDL_TARGET\t')) {
        const [, schema = '', table = '', engine = '', rows = '0', dataBytes = '0', indexBytes = '0', uniqueIndexes = '0', triggers = '0', foreignKeys = '0', purpose = '', impact = ''] = line.split('\t')
        result.target = { schema, table, engine, rows: Number(rows || 0), dataBytes: Number(dataBytes || 0), indexBytes: Number(indexBytes || 0), uniqueIndexes: Number(uniqueIndexes || 0), triggers: Number(triggers || 0), foreignKeys: Number(foreignKeys || 0), purpose, impact }
      } else if (line.startsWith('GMHA_ONLINE_DDL_LOAD\t')) {
        const [, version = '', binlogFormat = '', readOnly = '', threadsRunning = '0', transactions = '0'] = line.split('\t')
        result.load = { version, binlogFormat, readOnly, threadsRunning: Number(threadsRunning || 0), transactions: Number(transactions || 0) }
      } else if (line.startsWith('GMHA_ONLINE_DDL_VERIFIED\t')) {
        const [, schema = '', table = '', engine = '', rows = '0', dataBytes = '0', indexBytes = '0'] = line.split('\t')
        result.verified = { schema, table, engine, rows: Number(rows || 0), dataBytes: Number(dataBytes || 0), indexBytes: Number(indexBytes || 0) }
      }
    }
  }
  return result
}

const formatBytes = raw => {
  let value = Number(raw || 0)
  if (!Number.isFinite(value) || value <= 0) return '—'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let unit = 0
  while (value >= 1024 && unit < units.length - 1) { value /= 1024; unit++ }
  return `${value >= 100 || unit === 0 ? value.toFixed(0) : value.toFixed(1)} ${units[unit]}`
}

const templates = [
  { label: '新增字段', alter: 'ADD COLUMN new_column varchar(255) NULL', purpose: '为业务表新增可空字段', impact: '复制原表并增加字段存储空间' },
  { label: '修改字段', alter: 'MODIFY COLUMN column_name bigint NOT NULL', purpose: '调整字段类型或约束', impact: '全表复制并转换现有字段数据' },
  { label: '新增索引', alter: 'ADD INDEX idx_column_name (column_name)', purpose: '降低目标查询的扫描行数', impact: '增加索引空间、写放大和复制流量' },
  { label: '删除字段', alter: 'DROP COLUMN old_column', purpose: '移除已下线的历史字段', impact: '字段数据将永久丢失，需确认应用已停止读写' }
]

export default {
  name: 'OnlineDDLManagement',
  props: { instances: { type: Array, default: () => [] } },
  emits: ['open-task'],
  setup(props, { emit }) {
    const target = ref('')
    const form = ref({
      schema: '', table: '', alter: '', purpose: '', impact: '',
      max_load_threads_running: 25, critical_threads_running: 50, max_lag_seconds: 10,
      chunk_time_seconds: 0.5, check_interval_seconds: 1, alter_foreign_keys_method: 'auto',
      risk_acknowledged: false, confirmation: ''
    })
    const advanced = ref(false), loading = ref(false), error = ref(''), notice = ref('')
    const dryRun = ref({ status: 'idle', fingerprint: '', detail: null, checkedAt: '' })
    const progress = ref({ task: null, id: '', active: false, status: '', percent: 0, step: '', startedAt: 0 })
    const elapsedSeconds = ref(0)
    let elapsedTimer = null

    const instanceOptions = computed(() => (props.instances || []).map(item => ({
      value: `${field(item, 'MachineIP', 'machine_ip')}:${field(item, 'Port', 'port')}`,
      label: `${field(item, 'MachineName', 'machine_name') || field(item, 'MachineIP', 'machine_ip')} · ${field(item, 'MachineIP', 'machine_ip')}:${field(item, 'Port', 'port')}`,
      version: field(item, 'Version', 'version') || '版本待上报'
    })))
    watch(instanceOptions, options => {
      if (!options.some(option => option.value === target.value)) target.value = options[0]?.value || ''
    }, { immediate: true })

    const fingerprint = computed(() => buildOnlineDDLFingerprint({ target: target.value, ...form.value }))
    const dryRunReady = computed(() => dryRun.value.status === 'success' && dryRun.value.fingerprint === fingerprint.value)
    const exactTarget = computed(() => form.value.schema && form.value.table ? `${form.value.schema}.${form.value.table}` : '')
    const parsedDryRun = computed(() => parseOnlineDDLTaskDetail(dryRun.value.detail))
    const sqlPreview = computed(() => `ALTER TABLE \`${form.value.schema || 'database'}\`.\`${form.value.table || 'table'}\`\n  ${form.value.alter || 'ADD COLUMN ...'};`)
    const executionReady = computed(() => dryRunReady.value && form.value.risk_acknowledged && form.value.confirmation === exactTarget.value && !loading.value)

    watch(fingerprint, (next, previous) => {
      if (previous && next !== previous && dryRun.value.status === 'success') {
        dryRun.value = { status: 'stale', fingerprint: dryRun.value.fingerprint, detail: dryRun.value.detail, checkedAt: dryRun.value.checkedAt }
        form.value.risk_acknowledged = false
        form.value.confirmation = ''
      }
    })

    const parseTarget = () => {
      if (!target.value) throw new Error('请选择 MySQL 实例')
      const separator = target.value.lastIndexOf(':')
      return { machine: target.value.slice(0, separator), port: Number(target.value.slice(separator + 1)) }
    }
    const validateForm = action => {
      if (!form.value.schema.trim() || !form.value.table.trim()) throw new Error('请填写数据库和表名')
      if (!form.value.alter.trim()) throw new Error('请填写 ALTER 子句')
      if (/^\s*ALTER\s+TABLE\b/i.test(form.value.alter)) throw new Error('只需填写 ALTER TABLE 后面的变更子句')
      if (!form.value.purpose.trim() || !form.value.impact.trim()) throw new Error('请填写变更目的和预期影响')
      if (action === 'execute' && !dryRunReady.value) throw new Error('当前配置尚未通过 PT 预演，请先重新预演')
      if (action === 'execute' && form.value.confirmation !== exactTarget.value) throw new Error(`请输入完整目标 ${exactTarget.value}`)
    }
    const taskFailure = detail => {
      const event = [...(detail?.events || [])].reverse().find(item => normalized(field(item, 'EventType', 'event_type')) === 'error')
      const step = (detail?.steps || []).find(item => normalized(field(item, 'Status', 'status')) === 'failed')
      return field(event, 'Content', 'content') || field(step, 'Message', 'message') || '在线 DDL 任务执行失败'
    }
    const updateProgress = detail => {
      const status = taskStatus(detail)
      progress.value = {
        ...progress.value, task: detail, id: taskID(detail), status, active: !['success', 'failed'].includes(status),
        percent: Number(field(detail?.task, 'ProgressPercent', 'progress_percent') || 0),
        step: field(detail?.task, 'CurrentStep', 'current_step') || '等待 Agent 接收'
      }
    }
    const waitTask = async created => {
      const id = taskID(created)
      if (!id) throw new Error('任务创建失败：未返回任务 ID')
      progress.value = { task: created, id, active: true, status: 'pending', percent: 0, step: '等待 Agent 接收', startedAt: Date.now() }
      elapsedSeconds.value = 0
      for (let attempt = 0; attempt < 7200; attempt++) {
        const detail = await request(`/tasks?id=${encodeURIComponent(id)}`)
        updateProgress(detail)
        if (['success', 'failed'].includes(taskStatus(detail))) return detail
        await new Promise(resolve => setTimeout(resolve, 1000))
      }
      throw new Error('等待在线 DDL 任务超时，请到任务中心继续查看')
    }
    const payload = action => ({
      ...parseTarget(), action,
      schema: form.value.schema.trim(), table: form.value.table.trim(), alter: form.value.alter.trim(),
      purpose: form.value.purpose.trim(), impact: form.value.impact.trim(),
      max_load_threads_running: Number(form.value.max_load_threads_running),
      critical_threads_running: Number(form.value.critical_threads_running),
      max_lag_seconds: Number(form.value.max_lag_seconds),
      chunk_time_seconds: Number(form.value.chunk_time_seconds),
      check_interval_seconds: Number(form.value.check_interval_seconds),
      alter_foreign_keys_method: form.value.alter_foreign_keys_method,
      risk_acknowledged: action === 'execute' && form.value.risk_acknowledged,
      confirmation: action === 'execute' ? form.value.confirmation.trim() : ''
    })
    const dispatch = async action => {
      validateForm(action)
      const created = await request('/tasks/mysql-online-ddl', { method: 'POST', body: JSON.stringify(payload(action)) })
      const detail = await waitTask(created)
      if (taskStatus(detail) !== 'success') throw new Error(taskFailure(detail))
      return detail
    }
    const runDryRun = async () => {
      loading.value = true
      error.value = ''
      notice.value = ''
      const submittedFingerprint = fingerprint.value
      dryRun.value = { status: 'running', fingerprint: submittedFingerprint, detail: null, checkedAt: '' }
      try {
        const detail = await dispatch('dry_run')
        if (submittedFingerprint !== fingerprint.value) {
          dryRun.value = { status: 'stale', fingerprint: submittedFingerprint, detail, checkedAt: new Date().toLocaleString('zh-CN', { hour12: false }) }
          notice.value = '预演已完成，但配置在执行期间发生变化，请重新预演'
        } else {
          dryRun.value = { status: 'success', fingerprint: submittedFingerprint, detail, checkedAt: new Date().toLocaleString('zh-CN', { hour12: false }) }
          notice.value = 'PT dry-run 已通过，可以核对风险并正式执行'
        }
      } catch (err) {
        dryRun.value = { status: 'failed', fingerprint: submittedFingerprint, detail: progress.value.task, checkedAt: new Date().toLocaleString('zh-CN', { hour12: false }) }
        error.value = err.message
      } finally { loading.value = false }
    }
    const runExecute = async () => {
      loading.value = true
      error.value = ''
      notice.value = ''
      try {
        const detail = await dispatch('execute')
        notice.value = `在线 DDL 已执行并核验：${exactTarget.value}`
        dryRun.value = { status: 'idle', fingerprint: '', detail, checkedAt: '' }
        form.value.risk_acknowledged = false
        form.value.confirmation = ''
      } catch (err) { error.value = err.message }
      finally { loading.value = false }
    }
    const applyTemplate = item => {
      form.value.alter = item.alter
      form.value.purpose = item.purpose
      form.value.impact = item.impact
    }
    const openCurrentTask = () => { if (progress.value.task) emit('open-task', progress.value.task) }
    const closeProgress = () => {
      if (!progress.value.active) progress.value = { task: null, id: '', active: false, status: '', percent: 0, step: '', startedAt: 0 }
    }

    onMounted(() => {
      elapsedTimer = setInterval(() => {
        if (progress.value.active && progress.value.startedAt) elapsedSeconds.value = Math.floor((Date.now() - progress.value.startedAt) / 1000)
      }, 1000)
    })
    onUnmounted(() => { if (elapsedTimer) clearInterval(elapsedTimer) })

    return {
      target, form, templates, advanced, loading, error, notice, dryRun, progress, elapsedSeconds,
      instanceOptions, fingerprint, dryRunReady, exactTarget, parsedDryRun, sqlPreview, executionReady,
      formatBytes, applyTemplate, runDryRun, runExecute, openCurrentTask, closeProgress
    }
  },
  template: `
    <main class="online-ddl-page">
      <section class="online-ddl-hero">
        <div><p>ONLINE SCHEMA CHANGE</p><h3>在线 DDL</h3><span>通过 Percona Toolkit 在影子表完成数据复制，再以短时元数据锁原子切换。</span></div>
        <label>目标实例<select v-model="target" :disabled="loading"><option value="">请选择实例</option><option v-for="item in instanceOptions" :key="item.value" :value="item.value">{{ item.label }} · {{ item.version }}</option></select></label>
      </section>

      <div v-if="error" class="online-ddl-message error"><b>操作未完成</b><span>{{ error }}</span><button @click="error=''">×</button></div>
      <div v-if="notice" class="online-ddl-message success"><b>状态更新</b><span>{{ notice }}</span><button @click="notice=''">×</button></div>

      <section v-if="progress.task" :class="['online-ddl-progress',progress.status]">
        <header><div><b>{{ progress.status==='failed' ? '在线 DDL 任务失败' : progress.status==='success' ? '在线 DDL 任务已完成' : '在线 DDL 任务执行中' }}</b><small>{{ progress.id }} · 已用 {{ elapsedSeconds }} 秒</small></div><div><button class="text-button" @click="openCurrentTask">任务详情</button><button v-if="!progress.active" @click="closeProgress">×</button></div></header>
        <div><i :style="{width:progress.percent+'%'}"></i></div>
        <footer><span>{{ progress.step }}</span><b>{{ progress.percent }}%</b></footer>
      </section>

      <ol class="online-ddl-flow">
        <li :class="{done:dryRunReady}"><i>1</i><div><b>填写变更</b><span>限定单表 ALTER 子句</span></div></li>
        <li :class="{active:dryRun.status==='running',done:dryRunReady}"><i>2</i><div><b>PT 预演</b><span>检查工具、表结构与复制条件</span></div></li>
        <li :class="{active:dryRunReady&&!progress.active}"><i>3</i><div><b>风险确认</b><span>确认空间、负载与元数据锁</span></div></li>
        <li :class="{active:progress.active&&progress.status==='running'}"><i>4</i><div><b>复制并切换</b><span>进度持续写入任务中心</span></div></li>
      </ol>

      <section class="online-ddl-workspace">
        <div class="online-ddl-editor">
          <header><div><small>CHANGE DEFINITION</small><h4>定义变更</h4></div><span>仅接受 ALTER TABLE 后的子句</span></header>
          <div class="online-ddl-target-fields"><label>数据库<input v-model.trim="form.schema" :disabled="loading" placeholder="例如 app"></label><label>数据表<input v-model.trim="form.table" :disabled="loading" placeholder="例如 orders"></label></div>
          <div class="online-ddl-templates"><span>快速模板</span><button v-for="item in templates" :key="item.label" type="button" :disabled="loading" @click="applyTemplate(item)">{{ item.label }}</button></div>
          <label class="online-ddl-alter">ALTER 子句<textarea v-model="form.alter" :disabled="loading" rows="6" spellcheck="false" placeholder="ADD COLUMN fulfillment_status varchar(32) NULL"></textarea><small>{{ form.alter.length.toLocaleString('zh-CN') }} / 8,192</small></label>
          <pre class="online-ddl-preview"><code>{{ sqlPreview }}</code></pre>
          <div class="online-ddl-reason-fields"><label>变更目的<textarea v-model.trim="form.purpose" :disabled="loading" rows="2" maxlength="500" placeholder="说明业务需求和预期收益"></textarea></label><label>预期影响<textarea v-model.trim="form.impact" :disabled="loading" rows="2" maxlength="500" placeholder="说明空间、写入、复制或兼容性影响"></textarea></label></div>
        </div>

        <aside class="online-ddl-policy">
          <header><div><small>SAFETY POLICY</small><h4>负载门禁</h4></div><button class="text-button" @click="advanced=!advanced">{{ advanced ? '收起' : '调整' }}</button></header>
          <div class="online-ddl-policy-summary">
            <article><small>暂停复制</small><b>Threads_running ≥ {{ form.max_load_threads_running }}</b></article>
            <article><small>立即中止</small><b>Threads_running ≥ {{ form.critical_threads_running }}</b></article>
            <article><small>复制延迟</small><b>超过 {{ form.max_lag_seconds }} 秒暂停</b></article>
            <article><small>目标块耗时</small><b>{{ form.chunk_time_seconds }} 秒</b></article>
          </div>
          <div v-if="advanced" class="online-ddl-policy-fields">
            <label>暂停阈值<input v-model.number="form.max_load_threads_running" type="number" min="1" max="10000"></label>
            <label>中止阈值<input v-model.number="form.critical_threads_running" type="number" min="2" max="20000"></label>
            <label>最大延迟（秒）<input v-model.number="form.max_lag_seconds" type="number" min="1" max="3600"></label>
            <label>块耗时（秒）<input v-model.number="form.chunk_time_seconds" type="number" min="0.1" max="10" step="0.1"></label>
            <label>检查间隔（秒）<input v-model.number="form.check_interval_seconds" type="number" min="1" max="60"></label>
            <label>外键处理<select v-model="form.alter_foreign_keys_method"><option value="auto">auto（推荐）</option><option value="rebuild_constraints">rebuild_constraints</option><option value="drop_swap">drop_swap</option><option value="none">none</option></select></label>
          </div>
          <div class="online-ddl-risk-note"><b>执行前须知</b><ul><li>影子表可能额外占用接近原表大小的磁盘空间。</li><li>已有触发器、缺少主键或唯一键、复制过滤和外键限制可能导致 PT 拒绝执行。</li><li>最终换表仍需要短时元数据锁，长事务可能阻塞切换。</li></ul></div>
        </aside>
      </section>

      <section class="online-ddl-submit">
        <div class="online-ddl-dry-run">
          <header><div><i>1</i><span><b>预检与 dry-run</b><small>只验证，不修改原表</small></span></div><span :class="['online-ddl-state',dryRun.status]">{{ ({idle:'尚未预演',running:'预演中',success:'已通过',failed:'未通过',stale:'配置已变化'})[dryRun.status] }}</span></header>
          <div v-if="parsedDryRun.target" class="online-ddl-assessment">
            <article><small>表引擎 / 估算行数</small><b>{{ parsedDryRun.target.engine }} · {{ parsedDryRun.target.rows.toLocaleString('zh-CN') }}</b></article>
            <article><small>表与索引空间</small><b>{{ formatBytes(parsedDryRun.target.dataBytes) }} + {{ formatBytes(parsedDryRun.target.indexBytes) }}</b></article>
            <article><small>唯一索引 / 触发器 / 外键</small><b>{{ parsedDryRun.target.uniqueIndexes }} / {{ parsedDryRun.target.triggers }} / {{ parsedDryRun.target.foreignKeys }}</b></article>
            <article v-if="parsedDryRun.load"><small>预检时负载 / 活跃事务</small><b>{{ parsedDryRun.load.threadsRunning }} / {{ parsedDryRun.load.transactions }}</b></article>
          </div>
          <p v-else>PT 会检查工具版本、目标表、唯一键、触发器、外键、复制拓扑和当前负载，并完整执行一次 dry-run。</p>
          <button class="secondary" :disabled="loading || !target" @click="runDryRun">{{ dryRun.status==='running' ? '正在预演…' : dryRun.status==='success' ? '重新预演' : '开始 PT 预演' }}</button>
        </div>

        <div :class="['online-ddl-execute',{locked:!dryRunReady}]">
          <header><div><i>2</i><span><b>正式在线变更</b><small>{{ dryRunReady ? '预演已通过，请完成最终确认' : '通过当前配置的预演后解锁' }}</small></span></div><span>{{ exactTarget || '目标待填写' }}</span></header>
          <template v-if="dryRunReady">
            <label class="online-ddl-ack"><input v-model="form.risk_acknowledged" type="checkbox"><span>我已评估额外磁盘、复制压力、长事务和元数据锁风险，并已准备监控与回退方案。</span></label>
            <label class="online-ddl-confirm">输入 <code>{{ exactTarget }}</code> 确认目标<input v-model.trim="form.confirmation" autocomplete="off" :placeholder="exactTarget"></label>
          </template>
          <p v-else>变更目标或任何限流参数后，旧的预演结果会自动失效，避免以过期检查结果执行。</p>
          <button class="primary" :disabled="!executionReady" @click="runExecute">{{ loading ? '任务执行中…' : '执行在线 DDL' }}</button>
        </div>
      </section>
    </main>
  `
}
