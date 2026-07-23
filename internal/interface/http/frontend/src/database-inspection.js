import { computed, onUnmounted, ref } from 'vue/dist/vue.esm-bundler.js'

const value = (item, upper, lower) => item?.[upper] ?? item?.[lower]

const request = async (path, options = {}) => {
  const response = await fetch(`/api/v1${path}`, { headers: { 'Content-Type': 'application/json' }, ...options })
  const payload = await response.json().catch(() => ({}))
  if (!response.ok) throw new Error(payload.error || '请求失败')
  return payload
}

const statusLabel = status => ({
  pass: '通过', warning: '警告', critical: '严重', info: '信息',
  success: '完成', failed: '失败', pending: '等待中', sent: '已下发', running: '执行中'
})[String(status || '').toLowerCase()] || status || '未知'

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
        for (let attempt = 0; attempt < 180 && token === runToken; attempt++) {
          const payload = await request(`/tasks/database-inspection/results?task_ids=${encodeURIComponent(taskIDs.value.join(','))}`)
          result.value = payload
          if (payload.ready) {
            const target = payload.targets?.[0]
            if (target?.status === 'failed') error.value = target.error || '巡检执行失败'
            return
          }
          await new Promise(resolve => setTimeout(resolve, 1000))
        }
        if (token === runToken) error.value = '等待巡检结果超时，可前往任务中心查看执行状态。'
      } catch (err) {
        if (token === runToken) error.value = err.message
      } finally {
        if (token === runToken) running.value = false
      }
    }

    function openTask() {
      if (taskIDs.value[0]) emit('open-task', { Task: { ID: taskIDs.value[0] } })
    }

    onUnmounted(() => { runToken++ })
    return {
      selected, level, running, error, result, taskIDs, severity, selectedInstance, summary, filteredChecks,
      instanceKey, value, statusLabel, reportURL, dataURL, runInspection, openTask
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
        <div class="inspection-launch-grid">
          <label><span>目标实例</span><select v-model="selected"><option value="">请选择 MySQL 实例</option><option v-for="item in instances" :key="instanceKey(item)" :value="instanceKey(item)">{{ value(item,'MachineName','machine_name') || value(item,'MachineIP','machine_ip') }} · {{ value(item,'MachineIP','machine_ip') }}:{{ value(item,'Port','port') }} · MySQL {{ value(item,'Version','version') || '未知' }}</option></select></label>
          <div class="inspection-levels">
            <label :class="{active:level==='standard'}"><input v-model="level" type="radio" value="standard"><span><b>标准巡检</b><small>连接、负载、慢查询、GTID、Binlog 与持久性参数</small></span></label>
            <label :class="{active:level==='deep'}"><input v-model="level" type="radio" value="deep"><span><b>深度巡检</b><small>增加表结构、碎片、长事务、临时表和 InnoDB 深层指标</small></span></label>
          </div>
          <button class="primary inspection-run" :disabled="running || !selected" @click="runInspection">{{ running ? '正在巡检并生成结果…' : (level==='deep' ? '开始深度巡检' : '开始数据库巡检') }}</button>
        </div>
        <div v-if="error" class="inspection-error">{{ error }}</div>
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
