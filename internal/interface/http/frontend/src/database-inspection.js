import { computed, onMounted, onUnmounted, ref } from 'vue/dist/vue.esm-bundler.js'

const value = (item, upper, lower) => item?.[upper] ?? item?.[lower]

const request = async (path, options = {}) => {
  const response = await fetch(`/api/v1${path}`, { headers: { 'Content-Type': 'application/json' }, ...options })
  const payload = await response.json().catch(() => ({}))
  if (!response.ok) {
    const error = new Error(payload.error || `请求失败（${response.status}）`)
    error.status = response.status
    throw error
  }
  return payload
}

const taskSpec = task => {
  const raw = value(task, 'SpecJSON', 'spec_json')
  if (raw && typeof raw === 'object') return raw
  try { return JSON.parse(raw || '{}') } catch (_) { return {} }
}

const inspectionChildren = detail => {
  const children = detail?.children || detail?.Children || []
  return children.filter(task => {
    const operation = taskSpec(task).operation
    return operation === 'database_inspection' || operation === 'database_deep_inspection'
  })
}

export const inspectionHistoryRecordFromTasks = (parent, children, result) => {
  const targets = result?.targets || []
  const clusters = new Set(String(taskSpec(parent).target || '').split(',').map(item => item.trim()).filter(Boolean))
  targets.forEach(target => { if (target.cluster) clusters.add(target.cluster) })
  const scores = targets.filter(target => String(target.status).toLowerCase() === 'success').map(target => Number(target.score || 0))
  const deep = children.some(task => taskSpec(task).operation === 'database_deep_inspection')
  return {
    id: value(parent, 'ID', 'id'),
    level: deep ? 'deep' : 'standard',
    status: value(parent, 'Status', 'status'),
    progress: Number(value(parent, 'ProgressPercent', 'progress_percent') || 0),
    ready: Boolean(result?.ready),
    clusters: [...clusters].sort(),
    task_ids: children.map(task => value(task, 'ID', 'id')).filter(Boolean),
    targets,
    target_count: targets.length,
    average_score: scores.length ? Math.round(scores.reduce((total, score) => total + score, 0) / scores.length) : 0,
    passed: targets.reduce((total, target) => total + Number(target.passed || 0), 0),
    warnings: targets.reduce((total, target) => total + Number(target.warnings || 0), 0),
    critical: targets.reduce((total, target) => total + Number(target.critical || 0), 0),
    failed: Number(result?.failed || 0),
    created_at: value(parent, 'CreatedAt', 'created_at'),
    finished_at: value(parent, 'FinishedAt', 'finished_at')
  }
}

const statusLabel = status => ({
  pass: '通过', warning: '警告', critical: '严重', info: '信息',
  success: '完成', failed: '失败', pending: '等待中', sent: '已下发', running: '执行中'
})[String(status || '').toLowerCase()] || status || '未知'

export const inspectionHistoryDataURL = record => `/api/v1/tasks/database-inspection/data?task_ids=${encodeURIComponent((record?.task_ids || []).join(','))}`

const formatHistoryTime = raw => {
  if (!raw) return '—'
  const date = new Date(raw)
  return Number.isNaN(date.getTime()) ? raw : date.toLocaleString('zh-CN', { hour12: false })
}

const historyTarget = record => {
  const target = record?.targets?.[0]
  if (!target) return `${record?.target_count || 0} 个实例`
  const address = [target.ip, target.port].filter(Boolean).join(':')
  const name = target.machine || target.hostname || address
  return record.target_count > 1 ? `${name} 等 ${record.target_count} 个实例` : [name, address && address !== name ? address : ''].filter(Boolean).join(' · ')
}

export default {
  name: 'DatabaseInspection',
  props: {
    clusterName: { type: String, required: true },
    instances: { type: Array, default: () => [] }
  },
  emits: ['open-task'],
  setup(props, { emit }) {
    const selected = ref('')
    const level = ref('standard')
    const running = ref(false)
    const error = ref('')
    const result = ref({ ready: false, targets: [], checks: [] })
    const taskIDs = ref([])
    const severity = ref('all')
    const history = ref([])
    const historyTotal = ref(0)
    const historyLoading = ref(false)
    const historyError = ref('')
    const activeHistoryID = ref('')
    let runToken = 0

    const instanceKey = item => `${value(item, 'MachineID', 'machine_id')}|${value(item, 'Port', 'port')}`
    const selectedInstance = computed(() => props.instances.find(item => instanceKey(item) === selected.value))
    const summary = computed(() => {
      const target = result.value.targets?.[0] || {}
      return {
        score: target.score ?? '—',
        pass: target.passed || 0,
        warning: target.warnings || 0,
        critical: target.critical || 0,
        information: target.information || 0
      }
    })
    const filteredChecks = computed(() => {
      const checks = result.value.checks || []
      return severity.value === 'all' ? checks : checks.filter(item => item.status === severity.value)
    })
    const exportQuery = computed(() => encodeURIComponent(taskIDs.value.join(',')))
    const reportURL = computed(() => `/api/v1/tasks/database-inspection/report?task_ids=${exportQuery.value}`)
    const dataURL = computed(() => `/api/v1/tasks/database-inspection/data?task_ids=${exportQuery.value}`)

    async function loadHistoryFromRetainedTasks() {
      const payload = await request('/tasks?limit=500')
      const tasks = Array.isArray(payload) ? payload : (payload.items || payload.Items || [])
      const parents = tasks.filter(task => String(value(task, 'Type', 'type')).toLowerCase() === 'batch_operation' && taskSpec(task).operation === 'cluster_automation')
      const items = []
      for (const parent of parents.slice(0, 100)) {
        if (items.length >= 50) break
        const parentID = value(parent, 'ID', 'id')
        if (!parentID) continue
        let detail
        try { detail = await request(`/tasks?id=${encodeURIComponent(parentID)}`) } catch (_) { continue }
        const children = inspectionChildren(detail)
        if (!children.length) continue
        const taskIDs = children.map(task => value(task, 'ID', 'id')).filter(Boolean)
        let inspection
        try { inspection = await request(`/tasks/database-inspection/results?task_ids=${encodeURIComponent(taskIDs.join(','))}`) } catch (_) { continue }
        const record = inspectionHistoryRecordFromTasks(parent, children, inspection)
        if (props.clusterName && !record.clusters.some(name => String(name).toLowerCase() === props.clusterName.toLowerCase())) continue
        items.push(record)
      }
      return { items, total: items.length }
    }

    async function loadHistory() {
      historyLoading.value = true
      historyError.value = ''
      try {
        let payload
        try {
          payload = await request(`/tasks/database-inspection/history?cluster=${encodeURIComponent(props.clusterName)}&limit=50`)
        } catch (err) {
          if (err.status === 401 || err.status === 403) throw err
          payload = await loadHistoryFromRetainedTasks()
        }
        history.value = payload.items || []
        historyTotal.value = payload.total || 0
      } catch (err) {
        historyError.value = `巡检记录暂时无法读取：${err.message}`
      } finally {
        historyLoading.value = false
      }
    }

    async function waitForInspection(ids, token) {
      for (let attempt = 0; attempt < 180 && token === runToken; attempt++) {
        const payload = await request(`/tasks/database-inspection/results?task_ids=${encodeURIComponent(ids.join(','))}`)
        result.value = payload
        if (payload.ready) {
          const target = payload.targets?.[0]
          if (target?.status === 'failed') error.value = target.error || '巡检执行失败'
          return
        }
        await new Promise(resolve => setTimeout(resolve, 1000))
      }
      if (token === runToken) error.value = '等待巡检结果超时，可前往任务中心查看执行状态。'
    }

    async function runInspection() {
      const instance = selectedInstance.value
      if (!instance) {
        error.value = '请先选择需要巡检的数据库实例。'
        return
      }
      const machineID = value(instance, 'MachineID', 'machine_id')
      const port = Number(value(instance, 'Port', 'port') || 3306)
      if (!machineID) {
        error.value = '该实例缺少关联机器，无法创建巡检任务。'
        return
      }
      const token = ++runToken
      running.value = true
      error.value = ''
      result.value = { ready: false, targets: [], checks: [] }
      taskIDs.value = []
      activeHistoryID.value = ''
      try {
        const operation = level.value === 'deep' ? 'database_deep_inspection' : 'database_inspection'
        const created = await request('/tasks/cluster-automation', {
          method: 'POST',
          body: JSON.stringify({ clusters: [props.clusterName], target_machine_id: machineID, operation, port })
        })
        const failures = (created.items || []).filter(item => !item.task_id)
        if (failures.length) throw new Error(failures[0].error || '巡检任务创建失败')
        taskIDs.value = (created.items || []).map(item => item.task_id).filter(Boolean)
        if (!taskIDs.value.length) throw new Error('未创建任何巡检任务')
        activeHistoryID.value = created.parent_task_id || ''
        await waitForInspection(taskIDs.value, token)
      } catch (err) {
        if (token === runToken) error.value = err.message
      } finally {
        if (token === runToken) {
          running.value = false
          await loadHistory()
        }
      }
    }

    async function openHistory(record) {
      const ids = record?.task_ids || []
      if (!ids.length) return
      const token = ++runToken
      activeHistoryID.value = record.id
      taskIDs.value = [...ids]
      level.value = record.level === 'deep' ? 'deep' : 'standard'
      result.value = { ready: false, targets: [], checks: [] }
      running.value = !record.ready
      error.value = ''
      try {
        await waitForInspection(taskIDs.value, token)
      } catch (err) {
        if (token === runToken) error.value = err.message
      } finally {
        if (token === runToken) {
          running.value = false
          if (!record.ready) await loadHistory()
        }
      }
    }

    function openTask() {
      if (taskIDs.value[0]) emit('open-task', { Task: { ID: taskIDs.value[0] } })
    }

    onMounted(loadHistory)
    onUnmounted(() => { runToken++ })
    return {
      selected, level, running, error, result, taskIDs, severity, selectedInstance, summary, filteredChecks,
      history, historyTotal, historyLoading, historyError, activeHistoryID,
      instanceKey, value, statusLabel, reportURL, dataURL, runInspection, openTask, loadHistory, openHistory,
      inspectionHistoryDataURL, formatHistoryTime, historyTarget
    }
  },
  template: `
    <main class="database-inspection-page">
      <section class="inspection-hero">
        <div><p>DATABASE HEALTH INSPECTION</p><h3>数据库巡检</h3><span>从可用性、连接、负载、SQL 质量、持久性和表结构等维度评估实例健康度。</span></div>
        <div class="inspection-shield"><b>{{ result.targets?.length ? summary.score : '—' }}</b><small>健康评分</small></div>
      </section>

      <section class="instance-panel inspection-launcher">
        <header><div><h3>创建巡检任务</h3><p>管理员凭据由目标 Agent 临时注入，不经过浏览器，也不会保存在巡检报告中。</p></div><button v-if="taskIDs.length" class="text-button" @click="openTask">查看任务详情 →</button></header>
        <div class="inspection-launch-workflow">
          <div class="inspection-launch-fields">
            <section class="inspection-launch-step">
              <header><i>1</i><span><b>选择巡检目标</b><small>从当前集群选择一个 MySQL 实例</small></span></header>
              <label class="inspection-target-field"><span>数据库实例</span><div class="inspection-select-shell"><select v-model="selected"><option value="">请选择 MySQL 实例</option><option v-for="item in instances" :key="instanceKey(item)" :value="instanceKey(item)">{{ value(item,'MachineName','machine_name') || value(item,'MachineIP','machine_ip') }} · {{ value(item,'MachineIP','machine_ip') }}:{{ value(item,'Port','port') }} · MySQL {{ value(item,'Version','version') || '未知' }}</option></select></div></label>
            </section>
            <section class="inspection-launch-step inspection-mode-step">
              <header><i>2</i><span><b>选择巡检深度</b><small>标准巡检适合日常检查，深度巡检覆盖更多结构与事务指标</small></span></header>
              <div class="inspection-levels">
                <label :class="{active:level==='standard'}"><input v-model="level" type="radio" value="standard"><span><b>标准巡检</b><small>连接、负载、慢查询、GTID、Binlog 与持久性参数</small></span><em>日常推荐</em></label>
                <label :class="{active:level==='deep'}"><input v-model="level" type="radio" value="deep"><span><b>深度巡检</b><small>增加表结构、碎片、长事务、临时表和 InnoDB 深层指标</small></span><em>全面检查</em></label>
              </div>
            </section>
          </div>
          <footer class="inspection-launch-action"><div><i>3</i><span><b>确认并开始巡检</b><small>{{ selectedInstance ? ((value(selectedInstance,'MachineName','machine_name') || value(selectedInstance,'MachineIP','machine_ip')) + ' · ' + (level==='deep' ? '深度巡检' : '标准巡检')) : '选择目标实例后即可创建巡检任务' }}</small></span></div><button class="primary inspection-run" :disabled="running || !selected" @click="runInspection"><span>{{ running ? '正在巡检并生成结果…' : (level==='deep' ? '开始深度巡检' : '开始数据库巡检') }}</span><i>→</i></button></footer>
        </div>
        <div v-if="error" class="inspection-error">{{ error }}</div>
      </section>

      <section class="instance-panel inspection-history">
        <header><div><h3>巡检记录</h3><p>巡检结果随任务长期保留，可随时回看并导出 Excel 表格。</p></div><button class="text-button" :disabled="historyLoading" @click="loadHistory">{{ historyLoading ? '刷新中…' : '刷新记录' }}</button></header>
        <div v-if="historyError" class="inspection-history-error">{{ historyError }}</div>
        <div v-if="history.length" class="inspection-history-table"><table><thead><tr><th>巡检时间</th><th>目标实例</th><th>巡检类型</th><th>任务状态</th><th>健康评分</th><th>风险项</th><th>操作</th></tr></thead><tbody><tr v-for="record in history" :key="record.id" :class="{active:activeHistoryID===record.id}"><td>{{ formatHistoryTime(record.created_at) }}</td><td><b>{{ historyTarget(record) }}</b><small>{{ (record.clusters || []).join('、') || clusterName }}</small></td><td>{{ record.level==='deep' ? '深度巡检' : '标准巡检' }}</td><td><span :class="['inspection-status',record.status]">{{ statusLabel(record.status) }}</span><small v-if="!record.ready">{{ record.progress || 0 }}%</small></td><td><b class="history-score">{{ record.ready && !record.failed ? record.average_score : '—' }}</b></td><td><span class="history-risk critical">严重 {{ record.critical || 0 }}</span><span class="history-risk warning">警告 {{ record.warnings || 0 }}</span></td><td><button class="text-button" @click="openHistory(record)">{{ activeHistoryID===record.id ? '重新加载' : '查看详情' }}</button><a v-if="record.ready" :href="inspectionHistoryDataURL(record)" download>导出 Excel</a></td></tr></tbody></table></div>
        <div v-else-if="!historyLoading" class="inspection-history-empty">暂无巡检记录，完成首次巡检后会显示在这里。</div>
        <footer v-if="historyTotal>history.length">已显示最近 {{ history.length }} 条，共 {{ historyTotal }} 条记录</footer>
      </section>

      <template v-if="result.targets?.length">
        <section class="inspection-kpis">
          <article class="score"><small>健康评分</small><b>{{ summary.score }}</b><span>/ 100</span></article>
          <article class="pass"><small>检查通过</small><b>{{ summary.pass }}</b><span>项</span></article>
          <article class="warning"><small>风险警告</small><b>{{ summary.warning }}</b><span>项</span></article>
          <article class="critical"><small>严重风险</small><b>{{ summary.critical }}</b><span>项</span></article>
          <article><small>信息项</small><b>{{ summary.information }}</b><span>项</span></article>
        </section>

        <section class="instance-panel inspection-results">
          <header>
            <div><h3>巡检明细</h3><p>{{ result.targets[0]?.hostname || result.targets[0]?.machine }} · MySQL {{ result.targets[0]?.version || '未知' }} · {{ level==='deep' ? '深度巡检' : '标准巡检' }}</p></div>
            <div v-if="result.ready" class="inspection-exports"><a :href="reportURL" download>导出 Word 报告</a><a :href="dataURL" download>导出 Excel 数据</a></div>
          </header>
          <div class="inspection-filter"><button v-for="item in [['all','全部'],['critical','严重'],['warning','警告'],['pass','通过'],['info','信息']]" :key="item[0]" :class="{active:severity===item[0]}" @click="severity=item[0]">{{ item[1] }}</button><span>共 {{ filteredChecks.length }} 项</span></div>
          <div class="inspection-table-wrap"><table><thead><tr><th>分类</th><th>检查项</th><th>状态</th><th>当前值</th><th>期望阈值</th><th>检查说明</th><th>整改建议</th></tr></thead><tbody><tr v-for="check in filteredChecks" :key="check.code" :class="'inspection-'+check.status"><td><span class="inspection-category">{{ check.category }}</span></td><td><b>{{ check.title }}</b><small>{{ check.code }}</small></td><td><span :class="['inspection-status',check.status]">{{ statusLabel(check.status) }}</span></td><td>{{ check.value || '—' }}</td><td>{{ check.threshold || '—' }}</td><td>{{ check.description || '—' }}</td><td>{{ check.recommendation || '—' }}</td></tr><tr v-if="!filteredChecks.length"><td colspan="7" class="empty">当前筛选条件下没有巡检项。</td></tr></tbody></table></div>
        </section>
      </template>
      <section v-else class="inspection-empty"><i>✓</i><div><b>尚未执行巡检</b><span>选择目标实例与巡检深度，完成后可在此查看风险明细并导出 Word / Excel。</span></div></section>
    </main>`
}
