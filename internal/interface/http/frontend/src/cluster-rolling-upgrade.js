import { computed, onUnmounted, ref } from 'vue/dist/vue.esm-bundler.js'

const value = (item, upper, lower) => item?.[upper] ?? item?.[lower]

const request = async (path, options = {}) => {
  const response = await fetch(`/api/v1${path}`, { headers: { 'Content-Type': 'application/json' }, ...options })
  const payload = await response.json().catch(() => ({}))
  if (!response.ok) throw new Error(payload.error || '请求失败')
  return payload
}

const compareVersions = (left, right) => {
  const a = String(left || '').split('.').map(Number), b = String(right || '').split('.').map(Number)
  for (let index = 0; index < Math.max(a.length, b.length, 3); index++) {
    if ((a[index] || 0) !== (b[index] || 0)) return (a[index] || 0) - (b[index] || 0)
  }
  return 0
}

const stageLabel = status => ({
  pending: '等待执行', running: '执行中', success: '已完成', failed: '失败'
})[status] || status || '等待执行'

export default {
  name: 'ClusterRollingUpgrade',
  props: {
    clusterName: { type: String, required: true },
    instances: { type: Array, default: () => [] },
    packages: { type: Array, default: () => [] }
  },
  emits: ['open-task', 'refresh'],
  setup(props, { emit }) {
    const targetVersion = ref('')
    const port = ref(3306)
    const planning = ref(false)
    const starting = ref(false)
    const error = ref('')
    const plan = ref(null)
    const run = ref(null)
    const riskAcknowledged = ref(false)
    let pollToken = 0

    const currentVersions = computed(() => [...new Set(props.instances.map(item => value(item, 'Version', 'version')).filter(Boolean))])
    const highestCurrent = computed(() => [...currentVersions.value].sort(compareVersions).at(-1) || '')
    const targetVersions = computed(() => [...new Set(props.packages.map(item => item.version || item.Version).filter(version => !highestCurrent.value || compareVersions(version, highestCurrent.value) > 0))].sort((a, b) => compareVersions(b, a)))
    const progress = computed(() => {
      const stages = run.value?.stages || plan.value?.stages || []
      if (!stages.length) return 0
      return Math.round(stages.filter(stage => stage.status === 'success').length * 100 / stages.length)
    })
    const runActive = computed(() => ['pending', 'running'].includes(run.value?.status))
    const temporaryPrimary = computed(() => (run.value?.nodes || plan.value?.nodes || []).find(node => node.machine_id === (run.value?.temporary_primary_machine_id || plan.value?.temporary_primary_machine_id)))
    const originalPrimary = computed(() => (run.value?.nodes || plan.value?.nodes || []).find(node => node.machine_id === (run.value?.original_primary_machine_id || plan.value?.original_primary_machine_id)))

    function ensureDefaults() {
      if (!targetVersion.value && targetVersions.value.length) targetVersion.value = targetVersions.value[0]
      const ports = [...new Set(props.instances.map(item => Number(value(item, 'Port', 'port'))).filter(Boolean))]
      if (ports.length === 1) port.value = ports[0]
    }

    async function createPlan() {
      ensureDefaults()
      if (!targetVersion.value) {
        error.value = '当前软件仓库没有高于集群现有版本的 MySQL 安装包。'
        return
      }
      planning.value = true
      error.value = ''
      plan.value = null
      run.value = null
      try {
        plan.value = await request('/tasks/mysql-cluster-upgrade/plan', {
          method: 'POST',
          body: JSON.stringify({ cluster: props.clusterName, target_version: targetVersion.value, port: Number(port.value) })
        })
      } catch (err) {
        error.value = err.message
      } finally {
        planning.value = false
      }
    }

    async function startUpgrade() {
      if (!plan.value?.executable || !riskAcknowledged.value) return
      if (!confirm(`确认将集群 ${props.clusterName} 的全部 MySQL 节点滚动升级到 ${targetVersion.value}？\n\n系统会先升级从库，迁移 VIP 并切主，再升级原主库，最后切回原主库。任一步骤不满足安全条件都会停止。`)) return
      starting.value = true
      error.value = ''
      const token = ++pollToken
      try {
        run.value = await request('/tasks/mysql-cluster-upgrade/start', {
          method: 'POST',
          body: JSON.stringify({ cluster: props.clusterName, target_version: targetVersion.value, port: Number(port.value), risk_acknowledged: true })
        })
        while (token === pollToken && ['pending', 'running'].includes(run.value?.status)) {
          await new Promise(resolve => setTimeout(resolve, 1000))
          run.value = await request(`/tasks/mysql-cluster-upgrade?run_id=${encodeURIComponent(run.value.run_id)}`)
        }
        if (run.value?.status === 'failed') error.value = run.value.error || '集群滚动升级失败'
        if (run.value?.status === 'success') emit('refresh')
      } catch (err) {
        if (token === pollToken) error.value = err.message
      } finally {
        if (token === pollToken) starting.value = false
      }
    }

    function openTask() {
      if (run.value?.run_id) emit('open-task', { Task: { ID: run.value.run_id } })
    }

    ensureDefaults()
    onUnmounted(() => { pollToken++ })
    return {
      targetVersion, targetVersions, currentVersions, port, planning, starting, error, plan, run,
      riskAcknowledged, progress, runActive, temporaryPrimary, originalPrimary,
      stageLabel, createPlan, startUpgrade, openTask
    }
  },
  template: `
    <section class="cluster-rolling-upgrade">
      <header class="rolling-upgrade-hero">
        <div><p>ZERO-DOWNTIME ROLLING UPGRADE</p><h3>集群不停机滚动升级</h3><span>先逐台升级从库，再迁移 VIP 并切换主从；升级原主库后切回，始终保留可用写入口。</span></div>
        <div class="rolling-upgrade-route"><span><b>1</b>升级从库</span><i>→</i><span><b>2</b>切换主从</span><i>→</i><span><b>3</b>升级原主</span><i>→</i><span><b>4</b>切回原主</span></div>
      </header>

      <div class="rolling-upgrade-config">
        <label>目标 MySQL 版本<select v-model="targetVersion"><option value="">选择目标版本</option><option v-for="version in targetVersions" :key="version" :value="version">MySQL {{ version }}</option></select><small>后端会按每台机器的 CPU 架构和 glibc 自动选择兼容制品</small></label>
        <label>集群实例端口<input v-model.number="port" type="number" min="1" max="65535"><small>要求集群内每台机器在该端口登记一个 MySQL 实例</small></label>
        <button class="secondary" :disabled="planning || runActive" @click="createPlan">{{ planning ? '正在实时探测拓扑…' : '生成滚动升级计划' }}</button>
      </div>

      <div v-if="error" class="rolling-upgrade-error"><b>升级流程未完成</b><span>{{ error }}</span></div>

      <template v-if="plan || run">
        <section class="rolling-upgrade-summary">
          <article><small>当前版本</small><b>{{ currentVersions.join(' / ') || '未知' }}</b></article>
          <article><small>目标版本</small><b>{{ run?.target_version || plan?.target_version }}</b></article>
          <article><small>原主库</small><b>{{ originalPrimary?.machine || '未识别' }}</b></article>
          <article><small>临时主库</small><b>{{ temporaryPrimary?.machine || '未识别' }}</b></article>
          <article><small>整体进度</small><b>{{ progress }}%</b></article>
        </section>

        <section v-if="plan?.blocking_reasons?.length" class="rolling-upgrade-blockers"><b>安全门禁未通过</b><p v-for="reason in plan.blocking_reasons" :key="reason">{{ reason }}</p></section>
        <section v-if="plan?.warnings?.length" class="rolling-upgrade-warnings"><b>执行约束</b><p v-for="warning in plan.warnings" :key="warning">{{ warning }}</p></section>

        <div class="rolling-upgrade-layout">
          <section class="rolling-upgrade-stages">
            <header><div><h4>在线升级状态机</h4><p>Manager 严格串行推进，复制未追平时禁止强制切主。</p></div><button v-if="run?.run_id" class="text-button" @click="openTask">任务详情 →</button></header>
            <ol><li v-for="(stage,index) in (run?.stages || plan?.stages || [])" :key="stage.code" :class="stage.status"><i>{{ stage.status==='success' ? '✓' : stage.status==='failed' ? '!' : index+1 }}</i><span><b>{{ stage.name }}</b><small>{{ stage.message || stageLabel(stage.status) }}</small></span><em>{{ stageLabel(stage.status) }}</em></li></ol>
          </section>
          <section class="rolling-upgrade-nodes">
            <header><h4>节点升级顺序与状态</h4><span>{{ (run?.nodes || plan?.nodes || []).length }} 个节点</span></header>
            <div><table><thead><tr><th>节点</th><th>当前角色</th><th>版本路径</th><th>制品</th><th>状态</th></tr></thead><tbody><tr v-for="node in (run?.nodes || plan?.nodes || [])" :key="node.machine_id"><td><b>{{ node.machine }}</b><small>{{ node.ip }}:{{ node.port }}</small></td><td><span :class="['rolling-node-role',node.role]">{{ node.machine_id===(run?.original_primary_machine_id || plan?.original_primary_machine_id) ? '原主库' : node.machine_id===(run?.temporary_primary_machine_id || plan?.temporary_primary_machine_id) ? '临时主候选' : '从库' }}</span></td><td>{{ node.current_version }} → {{ node.target_version }}</td><td><code>{{ node.package_name }}</code></td><td><span :class="['status',node.status]">{{ node.status || '等待' }}</span><small v-if="node.error" class="rolling-node-error">{{ node.error }}</small></td></tr></tbody></table></div>
          </section>
        </div>

        <footer v-if="!run" class="rolling-upgrade-submit">
          <label><input v-model="riskAcknowledged" type="checkbox"><span><b>我确认已完成全节点可恢复备份，并验证客户端具备断线重连能力</b><small>主从切换会迁移 VIP 并短暂重建连接，但不会同时停止全部数据库节点。</small></span></label>
          <button class="primary" :disabled="!plan?.executable || !riskAcknowledged || starting" @click="startUpgrade">{{ starting ? '正在启动…' : '开始集群不停机升级' }}</button>
        </footer>
        <footer v-else class="rolling-upgrade-running"><div><b>{{ run.status==='success' ? '集群滚动升级完成' : run.status==='failed' ? '流程已安全停止' : '滚动升级正在执行' }}</b><small>运行 ID：{{ run.run_id }} · 当前阶段：{{ run.current_stage || '准备中' }}</small></div><progress :value="progress" max="100"></progress></footer>
      </template>
    </section>`
}
