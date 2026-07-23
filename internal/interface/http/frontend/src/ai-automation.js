import { computed, nextTick, onMounted, onUnmounted, ref } from 'vue/dist/vue.esm-bundler.js'

const request = async (path = '', options = {}) => {
  const response = await fetch(`/api/v1/ai${path}`, {
    ...options,
    headers: { 'Content-Type': 'application/json', ...(options.headers || {}) }
  })
  const raw = await response.text()
  let payload = {}
  try { payload = raw ? JSON.parse(raw) : {} } catch (_) { payload = {} }
  if (!response.ok) throw new Error(payload.error || raw || `请求失败（${response.status}）`)
  return payload
}

const emptyProvider = () => ({
  id: '', name: 'DeepSeek 运维模型', type: 'deepseek', base_url: 'https://api.deepseek.com',
  model: 'deepseek-v4-flash', api_key: '', enabled: true, is_default: true
})

const providerPresets = {
  deepseek: { name: 'DeepSeek 运维模型', base_url: 'https://api.deepseek.com', model: 'deepseek-v4-flash' },
  qwen: { name: '通义千问运维模型', base_url: 'https://dashscope.aliyuncs.com/compatible-mode/v1', model: 'qwen-plus' },
  openai: { name: 'OpenAI 运维模型', base_url: 'https://api.openai.com/v1', model: 'gpt-4o-mini' },
  anthropic: { name: 'Anthropic 运维模型', base_url: 'https://api.anthropic.com/v1', model: 'claude-sonnet-4-20250514' },
  ollama: { name: '本地 Ollama 模型', base_url: 'http://127.0.0.1:11434/v1', model: 'qwen2.5:14b' },
  custom: { name: '自定义兼容模型', base_url: 'https://', model: '' }
}

export default {
  name: 'AIAutomation',
  setup() {
    const loading = ref(true)
    const busy = ref('')
    const error = ref('')
    const notice = ref('')
    const tab = ref('workspace')
    const overview = ref({
      providers: [], settings: {}, messages: [], plans: [], workflows: [], runs: [], actions: [], stats: {}
    })
    const prompt = ref('')
    const showProvider = ref(false)
    const providerForm = ref(emptyProvider())
    const approvalPlan = ref(null)
    const chatPlanID = ref('')
    const armedPlanID = ref('')
    const confirmation = ref('')
    const mediumApproved = ref(false)
    const settingsDraft = ref({})
    const chatStream = ref(null)
    const pendingUserMessages = ref([])
    let pollTimer = null
    let polling = false

    const enabledProviders = computed(() => (overview.value.providers || []).filter(item => item.enabled))
    const defaultProvider = computed(() => (overview.value.providers || []).find(item => item.id === overview.value.settings.default_provider_id || item.is_default))
    const pendingPlans = computed(() => (overview.value.plans || []).filter(item => ['proposed', 'approval_required'].includes(item.status)))
    const recentPlans = computed(() => (overview.value.plans || []).slice(0, 12))
    const messages = computed(() => [
      ...(overview.value.messages || []).filter(item => item.session_id === 'default'),
      ...pendingUserMessages.value
    ])
    const activeProviderCount = computed(() => enabledProviders.value.length)
    const automationReady = computed(() => overview.value.settings.enabled && !!defaultProvider.value)
    const activeChatPlan = computed(() => {
      const plans = overview.value.plans || []
      const selected = plans.find(plan => plan.id === chatPlanID.value)
      if (selected) return selected
      return plans.find(plan => plan.session_id === 'default' && ['proposed', 'approval_required'].includes(plan.status)) || null
    })
    const activeChatWorkflow = computed(() => {
      if (!activeChatPlan.value?.workflow_id) return null
      return (overview.value.workflows || []).find(item => item.id === activeChatPlan.value.workflow_id) || null
    })
    const highRiskPlan = computed(() => ['high', 'critical'].includes(approvalPlan.value?.risk))
    const canConfirm = computed(() => canConfirmPlan(approvalPlan.value))

    async function load(silent = false) {
      if (!silent) loading.value = true
      try {
        overview.value = await request()
        settingsDraft.value = JSON.parse(JSON.stringify(overview.value.settings || {}))
        error.value = ''
      } catch (err) {
        error.value = err.message
      } finally {
        loading.value = false
      }
    }

    function flash(message) {
      notice.value = message
      setTimeout(() => { if (notice.value === message) notice.value = '' }, 3200)
    }

    function choosePreset() {
      const preset = providerPresets[providerForm.value.type] || providerPresets.custom
      const keepID = providerForm.value.id
      const keepKey = providerForm.value.api_key
      providerForm.value = { ...providerForm.value, ...preset, id: keepID, api_key: keepKey }
    }

    function openProvider(item = null) {
      providerForm.value = item
        ? { ...item, api_key: item.has_api_key ? '••••••••' : '' }
        : emptyProvider()
      showProvider.value = true
      error.value = ''
    }

    async function saveProvider() {
      busy.value = 'provider'
      try {
        await request('/providers', { method: providerForm.value.id ? 'PUT' : 'POST', body: JSON.stringify(providerForm.value) })
        showProvider.value = false
        flash(providerForm.value.id ? '模型配置已更新' : '模型已接入，请进行连接测试')
        await load(true)
      } catch (err) {
        error.value = err.message
      } finally {
        busy.value = ''
      }
    }

    async function testProvider(item) {
      busy.value = `test:${item.id}`
      try {
        await request('/providers/test', { method: 'POST', body: JSON.stringify({ id: item.id }) })
        flash(`${item.name} 连接成功`)
      } catch (err) {
        error.value = err.message
      } finally {
        await load(true)
        busy.value = ''
      }
    }

    async function removeProvider(item) {
      if (!window.confirm(`确认移除模型“${item.name}”？已保存的 API 密钥也会一并清除。`)) return
      try {
        await request(`/providers?id=${encodeURIComponent(item.id)}`, { method: 'DELETE' })
        flash('模型配置已移除')
        await load(true)
      } catch (err) {
        error.value = err.message
      }
    }

    async function saveSettings() {
      busy.value = 'settings'
      try {
        await request('/settings', { method: 'PUT', body: JSON.stringify(settingsDraft.value) })
        flash('AI 自动化策略已保存')
        await load(true)
      } catch (err) {
        error.value = err.message
      } finally {
        busy.value = ''
      }
    }

    async function sendPrompt(text = '') {
      const content = String(text || prompt.value).trim()
      if (!content || busy.value) return
      prompt.value = ''
      busy.value = 'chat'
      error.value = ''
      const optimisticID = `local-${Date.now()}`
      pendingUserMessages.value.push({
        id: optimisticID, session_id: 'default', role: 'user', content,
        created_at: new Date().toISOString(), pending: true
      })
      await scrollChatBottom()
      try {
        const result = await request('/chat', {
          method: 'POST',
          body: JSON.stringify({ session_id: 'default', provider_id: defaultProvider.value?.id || '', message: content })
        })
        pendingUserMessages.value = pendingUserMessages.value.filter(item => item.id !== optimisticID)
        await load(true)
        await scrollChatBottom()
        const reviewPlan = (result.plans || []).find(plan => ['approval_required', 'blocked'].includes(plan.status))
        if (reviewPlan) selectChatPlan(reviewPlan)
      } catch (err) {
        pendingUserMessages.value = pendingUserMessages.value.filter(item => item.id !== optimisticID)
        prompt.value = content
        error.value = err.message
      } finally {
        busy.value = ''
      }
    }

    function handlePromptKeydown(event) {
      if (event.key !== 'Enter' || event.shiftKey || event.isComposing) return
      event.preventDefault()
      sendPrompt()
    }

    async function scrollChatBottom() {
      await nextTick()
      if (chatStream.value) chatStream.value.scrollTop = chatStream.value.scrollHeight
    }

    async function pollExecution() {
      if (polling || document.hidden) return
      const monitoring = (overview.value.plans || []).some(plan =>
        ['submitted', 'executing'].includes(plan.status) ||
        ['recovery_analysis', 'analyzing_failure'].includes(plan.execution_stage)
      ) || (overview.value.workflows || []).some(item => ['running', 'paused', 'interrupted'].includes(item.status))
      if (tab.value !== 'copilot' && tab.value !== 'audit' && !monitoring) return
      polling = true
      const messageCount = messages.value.length
      const knownPlanIDs = new Set((overview.value.plans || []).map(plan => plan.id))
      try {
        await load(true)
        if (tab.value === 'copilot' && messages.value.length !== messageCount) {
          const recoveryPlan = (overview.value.plans || []).find(plan =>
            !knownPlanIDs.has(plan.id) && plan.parent_plan_id &&
            ['proposed', 'approval_required', 'blocked'].includes(plan.status)
          )
          if (recoveryPlan) selectChatPlan(recoveryPlan)
          await scrollChatBottom()
        }
      } finally {
        polling = false
      }
    }

    async function analyzeNow() {
      busy.value = 'analysis'
      error.value = ''
      try {
        const run = await request('/analyze', {
          method: 'POST', body: JSON.stringify({ provider_id: defaultProvider.value?.id || '' })
        })
        flash(run.plan_ids?.length ? `分析完成，生成 ${run.plan_ids.length} 个待审计划` : '分析完成，暂未发现需要执行的操作')
        await load(true)
      } catch (err) {
        error.value = err.message
      } finally {
        busy.value = ''
      }
    }

    function requestExecution(plan) {
      approvalPlan.value = plan
      resetApprovalInputs()
      if (plan.risk === 'low') executePlan(plan)
    }

    function resetApprovalInputs() {
      armedPlanID.value = ''
      confirmation.value = ''
      mediumApproved.value = false
    }

    function selectChatPlan(plan) {
      chatPlanID.value = plan?.id || ''
      resetApprovalInputs()
    }

    function closeChatPlan() {
      chatPlanID.value = ''
      resetApprovalInputs()
    }

    function canConfirmPlan(plan) {
      if (!plan) return false
      if (['high', 'critical'].includes(plan.risk)) return confirmation.value === plan.confirmation_phrase
      if (plan.risk === 'medium') return !overview.value.settings?.require_approval_medium || mediumApproved.value
      return true
    }

    function requestPlanExecution(plan) {
      if (!plan) return
      if (['high', 'critical'].includes(plan.risk)) {
        if (armedPlanID.value !== plan.id) {
          armedPlanID.value = plan.id
          return
        }
        confirmation.value = plan.confirmation_phrase
      }
      executePlan(plan)
    }

    async function executePlan(planInput = null) {
      // Vue passes the click Event when a handler is referenced without
      // arguments. Only accept a real persisted plan here so an Event can
      // never turn into an empty plan ID request.
      const plan = planInput?.id ? planInput : approvalPlan.value
      if (!plan || (plan.risk !== 'low' && !canConfirmPlan(plan))) return
      busy.value = `plan:${plan.id}`
      try {
        const result = await request('/plans/execute', {
          method: 'POST',
          body: JSON.stringify({ id: plan.id, confirmation: confirmation.value, approved: mediumApproved.value })
        })
        if (approvalPlan.value?.id === plan.id) approvalPlan.value = null
        resetApprovalInputs()
        await load(true)
        chatPlanID.value = plan.id
        const workflow = (overview.value.workflows || []).find(item => item.id === result.workflow_id)
        flash(workflow ? `工作流已启动，共 ${workflow.operations?.length || 1} 个子操作` : (result.task_id ? `操作已提交任务中心：${result.task_id}` : '操作已提交'))
      } catch (err) {
        const message = err.message
        await load(true)
        if (approvalPlan.value?.id === plan.id) approvalPlan.value = null
        resetApprovalInputs()
        error.value = message
      } finally {
        busy.value = ''
      }
    }

    async function rejectPlan(plan) {
      if (!plan || busy.value) return
      busy.value = `plan:${plan.id}`
      try {
        await request('/plans/reject', { method: 'POST', body: JSON.stringify({ id: plan.id }) })
        if (approvalPlan.value?.id === plan.id) approvalPlan.value = null
        if (chatPlanID.value === plan.id) closeChatPlan()
        flash('执行计划已拒绝')
        await load(true)
      } catch (err) {
        const message = err.message
        await load(true)
        if (approvalPlan.value?.id === plan.id) approvalPlan.value = null
        error.value = message
      } finally {
        busy.value = ''
      }
    }

    async function pauseWorkflow(workflow) {
      if (!workflow || busy.value) return
      busy.value = `workflow:${workflow.id}`
      try {
        await request('/workflows/pause', { method: 'POST', body: JSON.stringify({ id: workflow.id }) })
        await load(true)
        flash('工作流已暂停；当前子任务会继续监控，但不会启动下一步')
      } catch (err) {
        error.value = err.message
      } finally {
        busy.value = ''
      }
    }

    async function resumeWorkflow(workflow) {
      if (!workflow || busy.value) return
      busy.value = `workflow:${workflow.id}`
      try {
        await request('/workflows/resume', { method: 'POST', body: JSON.stringify({ id: workflow.id }) })
        await load(true)
        flash('工作流已恢复，平台会先刷新上下文再推进')
      } catch (err) {
        error.value = err.message
      } finally {
        busy.value = ''
      }
    }

    function toggleAction(action) {
      const allowed = new Set(settingsDraft.value.allowed_actions || [])
      if (allowed.has(action)) allowed.delete(action)
      else allowed.add(action)
      settingsDraft.value.allowed_actions = [...allowed]
    }

    function providerLabel(type) {
      return ({ deepseek: 'DeepSeek', qwen: '通义千问', openai: 'OpenAI', anthropic: 'Anthropic', ollama: 'Ollama', custom: 'OpenAI 兼容' })[type] || type
    }
    function riskLabel(risk) { return ({ low: '低风险', medium: '中风险', high: '高风险', critical: '极高风险' })[risk] || risk }
    function statusLabel(status) {
      return ({ pending: '等待前置步骤', staged: '已编排', proposed: '可执行', approval_required: '等待审批', blocked: '预检已阻止', executing: '执行中', submitted: '已提交', verifying: '监控验证中', paused: '已暂停', interrupted: '恢复需确认', skipped: '已跳过', succeeded: '已完成', failed: '失败', rejected: '已拒绝', expired: '已过期', running: '运行中' })[status] || status
    }
    function operationStatusLabel(operation) {
      if (operation?.status === 'pending') return operation.depends_on?.length ? '等待前置步骤' : '等待执行'
      return statusLabel(operation?.status)
    }
    function phaseLabel(phase) {
      return ({ understand: '架构理解', precheck: '安全预检', execute: '执行变更', verify: '结果验证', rollback: '失败回滚' })[phase] || phase
    }
    function date(value) { return value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '—' }
    function shortDate(value) { return value ? new Date(value).toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false }) : '—' }
    function actionAllowed(id) { return (settingsDraft.value.allowed_actions || []).includes(id) }
    function planForMessage(id) { return (overview.value.plans || []).find(plan => plan.id === id) }

    onMounted(async () => {
      await load()
      await scrollChatBottom()
      pollTimer = window.setInterval(pollExecution, 5000)
    })
    onUnmounted(() => {
      if (pollTimer) window.clearInterval(pollTimer)
    })

    return {
      loading, busy, error, notice, tab, overview, prompt, showProvider, providerForm, approvalPlan, chatPlanID, armedPlanID, chatStream, pendingUserMessages,
      confirmation, mediumApproved, settingsDraft, enabledProviders, defaultProvider, pendingPlans,
      recentPlans, messages, activeProviderCount, automationReady, activeChatPlan, activeChatWorkflow, highRiskPlan, canConfirm,
      load, choosePreset, openProvider, saveProvider, testProvider, removeProvider, saveSettings,
      sendPrompt, analyzeNow, requestExecution, executePlan, rejectPlan, pauseWorkflow, resumeWorkflow, toggleAction,
      handlePromptKeydown, scrollChatBottom, pollExecution, planForMessage, selectChatPlan, closeChatPlan, canConfirmPlan, requestPlanExecution,
      providerLabel, riskLabel, statusLabel, operationStatusLabel, phaseLabel, date, shortDate, actionAllowed
    }
  },
  template: `
    <section :class="['ai-page',{'copilot-mode':tab==='copilot'}]">
      <div v-if="notice" class="ai-toast success">{{ notice }}</div>
      <div v-if="error" class="ai-toast error"><span>{{ error }}</span><button @click="error=''">×</button></div>

      <header v-if="tab!=='copilot'" class="ai-hero">
        <div class="ai-hero-copy">
          <div class="ai-eyebrow"><i></i> 智能运维控制台</div>
          <h2>AI 与自动化</h2>
          <p>让大模型理解监控与告警，由 GMHA 的安全执行引擎完成数据库自动化运维。</p>
          <div class="ai-hero-status">
            <span :class="{ready:defaultProvider}"><i></i>{{ automationReady ? '自动化引擎运行中' : defaultProvider ? '手动分析可用 · 自动化未启用' : '等待完成模型配置' }}</span>
          </div>
        </div>
        <div class="ai-hero-actions">
          <button class="secondary" @click="tab='providers';openProvider()">＋ 接入模型</button>
          <button class="primary ai-analyze-button" :disabled="busy==='analysis' || !defaultProvider" @click="analyzeNow">
            <span>{{ busy==='analysis' ? '◌' : '✦' }}</span>{{ busy==='analysis' ? 'AI 正在分析…' : '分析全局状态' }}
          </button>
        </div>
      </header>

      <nav class="ai-tabs" aria-label="AI 与自动化页面导航">
        <button :class="{active:tab==='workspace'}" @click="tab='workspace'"><span>⌁</span>运维工作台<em v-if="pendingPlans.length">{{ pendingPlans.length }}</em></button>
        <button :class="{active:tab==='copilot'}" @click="tab='copilot';scrollChatBottom()"><span>✦</span>AI 运维助手</button>
        <button :class="{active:tab==='providers'}" @click="tab='providers'"><span>◈</span>模型接入</button>
        <button :class="{active:tab==='policy'}" @click="tab='policy'"><span>⌾</span>自动化策略</button>
        <button :class="{active:tab==='audit'}" @click="tab='audit'"><span>☷</span>执行审计</button>
      </nav>

      <div v-if="loading" class="ai-loading"><i></i><b>正在载入 AI 运维控制面…</b></div>

      <template v-else-if="tab==='workspace'">
        <section class="ai-stat-grid">
          <article><div class="ai-stat-icon violet">✦</div><span><small>今日 AI 分析</small><b>{{ overview.stats?.successful_runs || 0 }}</b><em>次完整巡检</em></span></article>
          <article><div class="ai-stat-icon amber">!</div><span><small>等待人工审批</small><b>{{ overview.stats?.pending_approvals || 0 }}</b><em>变更不会自动越权</em></span></article>
          <article><div class="ai-stat-icon green">✓</div><span><small>已授权动作</small><b>{{ overview.settings?.allowed_actions?.length || 0 }}</b><em>来自安全动作目录</em></span></article>
          <article><div class="ai-stat-icon blue">◈</div><span><small>当前分析模型</small><b class="ai-model-stat">{{ defaultProvider?.model || '未配置' }}</b><em>{{ defaultProvider?.name || '接入模型后可用' }}</em></span></article>
        </section>

        <div class="ai-workspace-grid">
          <section class="panel ai-pending-panel">
            <header class="ai-section-head">
              <div><span>APPROVAL QUEUE</span><h3>待审批执行计划</h3><p>AI 只提出计划；证据、影响范围与回滚方式确认后才会进入任务中心。</p></div>
              <button class="text-button" @click="load(true)">刷新</button>
            </header>
            <div v-if="pendingPlans.length" class="ai-plan-list">
              <article v-for="plan in pendingPlans" :key="plan.id" :class="['ai-plan-card',plan.risk]">
                <div class="ai-plan-risk"><span>{{ riskLabel(plan.risk) }}</span><i>{{ plan.risk==='low' ? '只读诊断' : plan.risk==='medium' ? '需要审批' : '强制二次确认' }}</i></div>
                <div class="ai-plan-main">
                  <header><div><h4>{{ plan.title }}</h4><p>{{ plan.summary || 'AI 建议执行该动作以进一步处理当前异常。' }}</p></div><span>{{ shortDate(plan.created_at) }} · 有效至 {{ shortDate(plan.expires_at) }}</span></header>
                  <dl>
                    <div><dt>操作对象</dt><dd>{{ plan.target_name || plan.target_id }}</dd></div>
                    <div><dt>白名单动作</dt><dd>{{ plan.action_label }}</dd></div>
                    <div><dt>回滚策略</dt><dd>{{ plan.rollback || '任务失败时停止后续步骤并保留完整日志' }}</dd></div>
                  </dl>
                  <details v-if="plan.evidence?.length"><summary>查看 AI 判断依据（{{ plan.evidence.length }}）</summary><ul><li v-for="evidence in plan.evidence" :key="evidence">{{ evidence }}</li></ul></details>
                </div>
                <footer><button class="ai-reject" :disabled="busy===('plan:'+plan.id)" @click="rejectPlan(plan)">{{ busy===('plan:'+plan.id) ? '处理中…' : '拒绝' }}</button><button class="primary" :disabled="busy===('plan:'+plan.id)" @click="requestExecution(plan)">{{ plan.risk==='low' ? '执行只读诊断' : '审阅并批准' }}</button></footer>
              </article>
            </div>
            <div v-else class="ai-empty-state">
              <div>✓</div><h4>当前没有待审批操作</h4><p>系统不会因为 AI 的一句建议直接修改生产环境。</p>
              <button class="secondary" :disabled="!defaultProvider" @click="analyzeNow">运行一次 AI 巡检</button>
            </div>
          </section>

          <aside class="ai-side-stack">
            <section class="panel ai-safety-card">
              <header><div><span>SAFETY GATE</span><h3>四级安全闸门</h3></div><b>强制生效</b></header>
              <ol>
                <li><i class="low">L1</i><span><b>只读诊断</b><small>可按策略自动执行，不改动服务</small></span></li>
                <li><i class="medium">L2</i><span><b>可恢复变更</b><small>默认需要运维人员批准</small></span></li>
                <li><i class="high">L3</i><span><b>服务中断</b><small>必须逐字输入确认短语</small></span></li>
                <li><i class="critical">L4</i><span><b>关机 / 数据风险</b><small>必须逐字二次确认，未确认绝不提交</small></span></li>
              </ol>
            </section>
            <section class="panel ai-recent-runs">
              <header><div><span>RECENT ANALYSIS</span><h3>最近分析</h3></div><button @click="tab='audit'">全部记录 →</button></header>
              <div v-if="overview.runs?.length">
                <article v-for="run in overview.runs.slice(0,4)" :key="run.id">
                  <i :class="run.status">{{ run.status==='succeeded' ? '✓' : run.status==='running' ? '…' : '!' }}</i>
                  <span><b>{{ run.summary || (run.status==='running' ? '正在分析监控数据' : run.error) }}</b><small>{{ run.trigger==='scheduled' ? '定时巡检' : '手动分析' }} · {{ shortDate(run.started_at) }}</small></span>
                  <em v-if="run.plan_ids?.length">{{ run.plan_ids.length }} 个计划</em>
                </article>
              </div>
              <p v-else class="ai-compact-empty">还没有分析记录。接入模型后运行第一次全局分析。</p>
            </section>
          </aside>
        </div>
      </template>

      <template v-else-if="tab==='copilot'">
        <section :class="['ai-copilot',{withPlan:activeChatPlan}]">
          <aside class="ai-copilot-context">
            <div class="ai-copilot-brand"><span>✦</span><div><b>GMHA Copilot</b><small>{{ defaultProvider?.model || '尚未配置模型' }}</small></div><i :class="{online:defaultProvider}"></i></div>
            <h4>可以这样问</h4>
            <button @click="sendPrompt('分析当前所有活动告警，按影响范围和紧急程度排序。')"><span>01</span>分析当前活动告警</button>
            <button @click="sendPrompt('哪些机器存在容量或稳定性风险？请给出判断证据。')"><span>02</span>识别容量与稳定性风险</button>
            <button @click="sendPrompt('检查 Agent 离线问题，生成安全的修复计划。')"><span>03</span>生成 Agent 修复计划</button>
            <div class="ai-context-note"><i>⌾</i><p><b>已自动附加平台上下文</b><small>活动告警、机器状态、MySQL 实例、集群归属与进行中任务。凭据和敏感配置不会发送给模型。</small></p></div>
          </aside>
          <main>
            <header><div><h3>AI 运维助手</h3><p>自然语言分析平台状态，变更计划仍受安全闸门约束。</p></div><span>{{ messages.length }} 条消息</span></header>
            <div ref="chatStream" class="ai-chat-stream">
              <section v-if="!messages.length" class="ai-chat-welcome">
                <div>✦</div><h3>从一个运维问题开始</h3><p>我会读取 GMHA 当前的监控与告警上下文，说明证据，并在需要时生成可审批的修复计划。</p>
              </section>
              <article v-for="message in messages" :key="message.id" :class="['ai-message',message.role,{pending:message.pending}]">
                <div>{{ message.role==='assistant' ? '✦' : '你' }}</div>
                <span>
                  <small>{{ message.role==='assistant' ? 'GMHA Copilot' : '运维人员' }} · {{ message.pending ? '正在发送' : shortDate(message.created_at) }}</small>
                  <p>{{ message.content }}</p>
                  <section v-if="message.plan_id && planForMessage(message.plan_id)" :class="['ai-inline-plan',planForMessage(message.plan_id).risk]">
                    <div><b>{{ planForMessage(message.plan_id).action_label }}</b><small>{{ riskLabel(planForMessage(message.plan_id).risk) }} · {{ statusLabel(planForMessage(message.plan_id).status) }}</small><p v-if="planForMessage(message.plan_id).error" class="ai-inline-plan-error">{{ planForMessage(message.plan_id).error }}</p></div>
                    <button class="secondary" @click="selectChatPlan(planForMessage(message.plan_id))">{{ planForMessage(message.plan_id).status==='blocked' ? '查看处理方案' : planForMessage(message.plan_id).status==='failed' ? '查看异常分析' : ['executing','submitted','succeeded'].includes(planForMessage(message.plan_id).status) ? '查看执行进度' : '查看并审批' }}</button>
                  </section>
                </span>
              </article>
              <article v-if="busy==='chat'" class="ai-message assistant thinking"><div>✦</div><span><small>GMHA Copilot</small><p><i></i><i></i><i></i> 正在读取监控上下文并分析…</p></span></article>
            </div>
            <form class="ai-chat-input" @submit.prevent="sendPrompt()">
              <textarea v-model="prompt" rows="3" :disabled="!defaultProvider" :placeholder="defaultProvider ? '描述你想分析或处理的运维问题…' : '请先在“模型接入”中配置默认大模型'" @keydown="handlePromptKeydown"></textarea>
              <footer><small><b>Enter 发送</b><span>Shift + Enter 换行 · 所有变更均由 GMHA 白名单和审批策略复核</span></small><button type="submit" class="primary" :disabled="!prompt.trim() || busy==='chat' || !defaultProvider">发送 <span>↑</span></button></footer>
            </form>
          </main>
          <aside v-if="activeChatPlan" class="ai-chat-plan-drawer">
            <header class="ai-plan-drawer-head">
              <div><span :class="['ai-plan-risk-pill',activeChatPlan.risk]">{{ activeChatPlan.status==='blocked' ? '预检已阻止' : activeChatPlan.status==='failed' ? '执行异常' : riskLabel(activeChatPlan.risk) }}</span><h3>{{ activeChatPlan.status==='blocked' ? '预检结果与处理方案' : activeChatPlan.status==='failed' ? '异常分析与恢复方案' : '执行计划与风险' }}</h3></div>
              <button type="button" aria-label="关闭计划审批栏" @click="closeChatPlan">×</button>
            </header>
            <div class="ai-plan-drawer-body">
              <section :class="['ai-plan-drawer-summary',activeChatPlan.risk]">
                <i>!</i><div><b>{{ activeChatPlan.action_label }}</b><p>{{ activeChatPlan.summary }}</p></div>
              </section>
              <section v-if="activeChatPlan.error" class="ai-plan-blocked-notice">
                <b>{{ activeChatPlan.status==='failed' ? '任务执行异常' : '当前方案不可执行' }}</b><p>{{ activeChatPlan.error }}</p>
              </section>
              <section v-if="activeChatPlan.failure_analysis" class="ai-plan-failure-analysis">
                <h4>AI 异常分析</h4><p>{{ activeChatPlan.failure_analysis }}</p>
                <small v-if="activeChatPlan.recovery_plan_id">已生成新的恢复计划：{{ activeChatPlan.recovery_plan_id }}</small>
              </section>
              <dl>
                <div><dt>目标对象</dt><dd>{{ activeChatPlan.target_name || activeChatPlan.target_id }}<small>{{ activeChatPlan.target_id }}</small></dd></div>
                <div><dt>风险等级</dt><dd>{{ riskLabel(activeChatPlan.risk) }}</dd></div>
              </dl>
              <section v-if="activeChatWorkflow" class="ai-plan-drawer-section ai-runtime-workflow">
                <header><div><h4>任务编排与实时状态</h4><p>{{ activeChatWorkflow.goal }}</p></div><em :class="['ai-status-pill',activeChatWorkflow.status]">{{ statusLabel(activeChatWorkflow.status) }}</em></header>
                <p v-if="activeChatWorkflow.pause_reason" class="ai-workflow-pause-reason">{{ activeChatWorkflow.pause_reason }}</p>
                <ol>
                  <li v-for="(operation,index) in activeChatWorkflow.operations" :key="operation.id" :class="[operation.status,{current:activeChatWorkflow.current_operation_id===operation.id}]">
                    <i>{{ operation.status==='succeeded' ? '✓' : operation.status==='failed' || operation.status==='blocked' ? '!' : index+1 }}</i>
                    <div>
                      <span><b>{{ operation.title }}</b><em>{{ operationStatusLabel(operation) }}</em></span>
                      <p>{{ operation.action_label }} · {{ operation.target_name || operation.target_id }}</p>
                      <small v-if="operation.depends_on?.length">依赖：{{ operation.depends_on.join('、') }}</small>
                      <small v-if="operation.execution_stage">{{ operation.execution_stage }}</small>
                      <small v-if="operation.task_id">子任务：{{ operation.task_id }}</small>
                      <small v-if="operation.error" class="error">{{ operation.error }}</small>
                      <details v-if="planForMessage(operation.plan_id)" class="ai-operation-detail">
                        <summary>依据、验证与失败处理</summary>
                        <p>{{ planForMessage(operation.plan_id).summary }}</p>
                        <ul v-if="planForMessage(operation.plan_id).evidence?.length"><li v-for="item in planForMessage(operation.plan_id).evidence" :key="item">{{ item }}</li></ul>
                        <ol v-if="planForMessage(operation.plan_id).steps?.length"><li v-for="step in planForMessage(operation.plan_id).steps" :key="step.order"><b>{{ step.title }}</b><span>{{ step.verification || step.detail }}</span></li></ol>
                        <p v-if="planForMessage(operation.plan_id).rollback">回滚：{{ planForMessage(operation.plan_id).rollback }}</p>
                      </details>
                    </div>
                  </li>
                </ol>
                <details v-if="activeChatWorkflow.checkpoints?.length" class="ai-workflow-checkpoints">
                  <summary>最近检查点（{{ activeChatWorkflow.checkpoints.length }}）</summary>
                  <ul><li v-for="item in activeChatWorkflow.checkpoints.slice(-4).reverse()" :key="item.id"><b>{{ item.phase }}</b><span>{{ item.summary?.join('；') || item.result }}</span><time>{{ shortDate(item.created_at) }}</time></li></ul>
                </details>
              </section>
              <section v-if="!activeChatWorkflow && activeChatPlan.evidence?.length" class="ai-plan-drawer-section">
                <h4>平台事实与判断依据</h4>
                <ul><li v-for="item in activeChatPlan.evidence" :key="item">{{ item }}</li></ul>
              </section>
              <section v-if="!activeChatWorkflow && activeChatPlan.steps?.length" class="ai-plan-drawer-section ai-plan-workflow">
                <h4>完整操作流程</h4>
                <ol>
                  <li v-for="step in activeChatPlan.steps" :key="step.order">
                    <i>{{ step.order }}</i>
                    <div>
                      <span><em>{{ phaseLabel(step.phase) }}</em><b>{{ step.title }}</b></span>
                      <p>{{ step.detail }}</p>
                      <details v-if="step.verification || step.on_failure">
                        <summary>验证标准与失败处理</summary>
                        <small v-if="step.verification"><b>验证：</b>{{ step.verification }}</small>
                        <small v-if="step.on_failure"><b>失败：</b>{{ step.on_failure }}</small>
                      </details>
                    </div>
                  </li>
                </ol>
              </section>
              <section v-if="!activeChatWorkflow" class="ai-plan-drawer-section">
                <h4>回滚与恢复</h4>
                <p>{{ activeChatPlan.rollback || '失败时停止后续步骤，需根据任务日志人工恢复。' }}</p>
              </section>
              <label v-if="['proposed','approval_required'].includes(activeChatPlan.status) && activeChatPlan.risk==='medium'" class="ai-approval-check ai-plan-drawer-check">
                <input v-model="mediumApproved" type="checkbox">
                <span><b>我已检查目标与影响范围</b><small>批准后操作将进入任务中心并保留完整审计记录。</small></span>
              </label>
              <section v-else-if="['proposed','approval_required'].includes(activeChatPlan.status) && ['high','critical'].includes(activeChatPlan.risk)" :class="['ai-plan-drawer-confirm',{armed:armedPlanID===activeChatPlan.id}]">
                <span><b>高风险操作需要二次确认</b><small>第一次点击仅确认已阅读风险，不会提交任务；第二次点击才会执行。</small></span>
                <div><i>{{ armedPlanID===activeChatPlan.id ? '2' : '1' }}</i><p><b>{{ armedPlanID===activeChatPlan.id ? '请再次确认执行' : '先确认风险与影响范围' }}</b><small>{{ armedPlanID===activeChatPlan.id ? '再次点击底部按钮后，系统才会提交任务。' : '点击底部按钮进入最终确认步骤。' }}</small></p></div>
              </section>
            </div>
            <footer v-if="['proposed','approval_required'].includes(activeChatPlan.status)" class="ai-plan-drawer-actions">
              <button type="button" class="secondary" :disabled="busy===('plan:'+activeChatPlan.id)" @click="rejectPlan(activeChatPlan)">拒绝计划</button>
              <button type="button" class="danger-button" :disabled="(activeChatPlan.risk==='medium' && !canConfirmPlan(activeChatPlan)) || busy===('plan:'+activeChatPlan.id)" @click="requestPlanExecution(activeChatPlan)">{{ busy===('plan:'+activeChatPlan.id) ? '正在提交…' : ['high','critical'].includes(activeChatPlan.risk) ? (armedPlanID===activeChatPlan.id ? '再次确认并执行' : '确认风险，继续') : '确认并执行' }}</button>
            </footer>
            <footer v-else-if="activeChatWorkflow && ['running','paused','interrupted'].includes(activeChatWorkflow.status)" class="ai-plan-drawer-actions ai-workflow-controls">
              <span><b>{{ activeChatWorkflow.current_operation_id ? '正在跟踪当前子任务' : '停在安全检查点' }}</b><small>暂停只阻止后续步骤，不会强行中止已提交的数据库任务。</small></span>
              <button v-if="activeChatWorkflow.status==='running'" type="button" class="secondary" :disabled="busy===('workflow:'+activeChatWorkflow.id)" @click="pauseWorkflow(activeChatWorkflow)">暂停后续步骤</button>
              <button v-else type="button" class="primary" :disabled="busy===('workflow:'+activeChatWorkflow.id)" @click="resumeWorkflow(activeChatWorkflow)">重新检查并恢复</button>
            </footer>
          </aside>
        </section>
      </template>

      <template v-else-if="tab==='providers'">
        <section class="ai-config-page">
          <header class="ai-page-head"><div><span>MODEL PROVIDERS</span><h3>模型接入</h3><p>支持主流云模型、OpenAI 兼容服务与本地 Ollama。API 密钥使用 Manager 本地密钥加密保存。</p></div><button class="primary" @click="openProvider()">＋ 接入新模型</button></header>
          <div class="ai-provider-grid">
            <article v-for="item in overview.providers" :key="item.id" class="panel ai-provider-card">
              <header><div :class="['ai-provider-logo',item.type]">{{ item.type==='deepseek' ? 'DS' : item.type==='qwen' ? 'QW' : item.type==='anthropic' ? 'AN' : item.type==='ollama' ? 'OL' : 'AI' }}</div><span><b>{{ item.name }}</b><small>{{ providerLabel(item.type) }}</small></span><em v-if="item.is_default">默认</em></header>
              <dl><div><dt>模型</dt><dd>{{ item.model }}</dd></div><div><dt>API 地址</dt><dd>{{ item.base_url }}</dd></div><div><dt>凭据</dt><dd>{{ item.has_api_key ? '已加密保存' : item.type==='ollama' ? '本地免密' : '未配置' }}</dd></div></dl>
              <div :class="['ai-provider-health',item.last_status || 'unknown']"><i></i><span><b>{{ item.last_status==='connected' ? '连接正常' : item.last_status==='failed' ? '连接失败' : '尚未测试' }}</b><small>{{ item.last_error || (item.last_tested_at ? date(item.last_tested_at) : '保存后建议进行连接测试') }}</small></span></div>
              <footer><button class="danger-link" @click="removeProvider(item)">移除</button><span></span><button class="secondary" :disabled="busy===('test:'+item.id)" @click="testProvider(item)">{{ busy===('test:'+item.id) ? '测试中…' : '测试连接' }}</button><button class="primary" @click="openProvider(item)">编辑</button></footer>
            </article>
            <button class="ai-provider-add" @click="openProvider()"><span>＋</span><b>接入另一个模型</b><small>云模型 / 私有模型 / 本地模型</small></button>
          </div>
          <section class="ai-secret-note"><i>⌾</i><div><b>凭据安全说明</b><p>API 密钥仅在 Manager 端使用 AES-256-GCM 加密保存，不会返回浏览器、不写入任务日志，也不会随监控上下文发送给模型。远程模型地址强制使用 HTTPS。</p></div></section>
        </section>
      </template>

      <template v-else-if="tab==='policy'">
        <section class="ai-policy-layout">
          <main>
            <header class="ai-page-head"><div><span>AUTOMATION POLICY</span><h3>自动化策略</h3><p>决定 AI 何时分析、哪些动作可以执行，以及每个风险等级由谁批准。</p></div><button class="primary" :disabled="busy==='settings'" @click="saveSettings">{{ busy==='settings' ? '保存中…' : '保存策略' }}</button></header>
            <section class="panel ai-policy-section">
              <header><div><h4>自动分析</h4><p>定时把活动告警与资产健康状态交给默认模型分析。</p></div><label class="ai-switch"><input v-model="settingsDraft.enabled" type="checkbox"><i></i></label></header>
              <div class="ai-policy-fields">
                <label>默认分析模型<select v-model="settingsDraft.default_provider_id"><option value="">请选择模型</option><option v-for="item in enabledProviders" :key="item.id" :value="item.id">{{ item.name }} · {{ item.model }}</option></select></label>
                <label>分析范围<select v-model="settingsDraft.analysis_scope"><option value="all">全部集群与机器</option><option value="alerts">仅活动告警对象</option></select></label>
                <label>巡检间隔<select v-model.number="settingsDraft.analysis_interval_minutes"><option :value="5">每 5 分钟</option><option :value="15">每 15 分钟</option><option :value="30">每 30 分钟</option><option :value="60">每小时</option><option :value="360">每 6 小时</option></select></label>
              </div>
              <label class="ai-check-row"><input v-model="settingsDraft.auto_analyze_alerts" type="checkbox"><span><b>自动分析新告警与存量异常</b><small>仅生成分析结论和执行计划，不代表自动执行变更。</small></span></label>
            </section>
            <section class="panel ai-policy-section">
              <header><div><h4>风险与审批</h4><p>高危操作的二次确认由服务端强制执行，页面设置无法关闭。</p></div><span class="ai-enforced">服务端强制</span></header>
              <div class="ai-risk-policy">
                <article><i class="low">L1</i><span><b>低风险 · 只读诊断</b><small>采集监控与诊断数据</small></span><label class="ai-switch"><input v-model="settingsDraft.auto_execute_low_risk" type="checkbox"><i></i></label><em>{{ settingsDraft.auto_execute_low_risk ? '允许自动执行' : '人工执行' }}</em></article>
                <article><i class="medium">L2</i><span><b>中风险 · 可恢复变更</b><small>例如重启异常 Agent</small></span><label class="ai-switch"><input v-model="settingsDraft.require_approval_medium" type="checkbox"><i></i></label><em>{{ settingsDraft.require_approval_medium ? '需要人工审批' : '按计划执行' }}</em></article>
                <article><i class="high">L3</i><span><b>高风险 · 服务中断</b><small>例如重启 MySQL</small></span><strong>输入确认短语</strong><em>无法关闭</em></article>
                <article><i class="critical">L4</i><span><b>极高风险 · 主机与数据</b><small>关机、停止数据库、数据相关操作</small></span><strong>输入确认短语</strong><em>绝不自动</em></article>
              </div>
            </section>
            <section class="panel ai-policy-section">
              <header><div><h4>动作授权目录</h4><p>即使模型建议了动作，也只有这里授权的白名单项可以进入执行阶段。</p></div><span>{{ settingsDraft.allowed_actions?.length || 0 }} / {{ overview.actions.length }} 已授权</span></header>
              <div class="ai-action-catalog">
                <button v-for="action in overview.actions" :key="action.id" :class="{enabled:actionAllowed(action.id)}" @click="toggleAction(action.id)"><i>{{ actionAllowed(action.id) ? '✓' : '' }}</i><span><b>{{ action.label }}</b><small>{{ action.description }}</small></span><em :class="action.risk">{{ riskLabel(action.risk) }}</em></button>
              </div>
              <div class="ai-data-guard"><i>!</i><p><b>高危动作可以审批执行，但不能绕过专用流程</b><small>删除集群登记、停止数据库和重启主机等目录动作必须逐字二次确认；DROP、TRUNCATE、删除文件等未登记动作仍需使用对应业务模块。</small></p></div>
            </section>
          </main>
          <aside class="panel ai-policy-summary">
            <span>POLICY PREVIEW</span><h3>当前生效边界</h3>
            <div :class="['ai-engine-state',{on:settingsDraft.enabled}]"><i></i><b>{{ settingsDraft.enabled ? 'AI 自动化已启用' : 'AI 自动化已暂停' }}</b><small>{{ settingsDraft.enabled ? '定时分析会按下列规则运行' : '对话和手动分析仍可使用' }}</small></div>
            <dl><div><dt>默认模型</dt><dd>{{ enabledProviders.find(item=>item.id===settingsDraft.default_provider_id)?.name || '未选择' }}</dd></div><div><dt>巡检频率</dt><dd>{{ settingsDraft.analysis_interval_minutes || 15 }} 分钟</dd></div><div><dt>自动执行上限</dt><dd>{{ settingsDraft.require_approval_medium ? (settingsDraft.auto_execute_low_risk ? '仅低风险' : '不自动执行') : '中风险及以下' }}</dd></div><div><dt>高危保护</dt><dd>始终二次确认</dd></div></dl>
            <p><i>⌾</i>模型无法修改此策略，也无法把自定义命令塞入执行计划。</p>
          </aside>
        </section>
      </template>

      <template v-else-if="tab==='audit'">
        <section class="ai-audit-page">
          <header class="ai-page-head"><div><span>AUDIT TRAIL</span><h3>执行审计</h3><p>保留 AI 分析、人工审批和任务提交的完整链路，便于追责与复盘。</p></div><button class="secondary" @click="load(true)">刷新记录</button></header>
          <section class="panel ai-audit-table">
            <header><span>时间</span><span>来源 / 操作</span><span>目标</span><span>风险</span><span>状态</span><span>任务</span></header>
            <article v-for="plan in recentPlans" :key="plan.id">
              <time>{{ date(plan.created_at) }}</time>
              <span><b>{{ plan.title }}</b><small>{{ plan.run_id ? 'AI 自动巡检' : 'AI 运维助手' }} · {{ plan.action_label }}</small></span>
              <span><b>{{ plan.target_name || plan.target_id }}</b><small>{{ plan.target_id }}</small></span>
              <span><em :class="['ai-risk-pill',plan.risk]">{{ riskLabel(plan.risk) }}</em></span>
              <span><em :class="['ai-status-pill',plan.status]">{{ statusLabel(plan.status) }}</em><small v-if="plan.error">{{ plan.error }}</small></span>
              <span><code v-if="plan.task_id">{{ plan.task_id }}</code><small v-else>—</small></span>
            </article>
            <div v-if="!recentPlans.length" class="ai-empty-state compact"><div>☷</div><h4>暂无执行审计记录</h4><p>AI 生成计划后会自动出现在这里。</p></div>
          </section>
        </section>
      </template>

      <div v-if="showProvider" class="modal-backdrop ai-modal-backdrop" @click.self="showProvider=false">
        <form class="modal ai-provider-modal" @submit.prevent="saveProvider">
          <header><div><span>MODEL CONNECTION</span><h3>{{ providerForm.id ? '编辑模型接入' : '接入大模型' }}</h3><p>模型只获得平台只读上下文，不直接持有服务器凭据。</p></div><button type="button" @click="showProvider=false">×</button></header>
          <div class="ai-modal-body">
            <label>提供商<select v-model="providerForm.type" @change="choosePreset"><option value="deepseek">DeepSeek</option><option value="qwen">通义千问（OpenAI 兼容）</option><option value="openai">OpenAI</option><option value="anthropic">Anthropic</option><option value="ollama">本地 Ollama</option><option value="custom">自定义 OpenAI 兼容服务</option></select></label>
            <label>显示名称<input v-model.trim="providerForm.name" required placeholder="例如：生产运维模型"></label>
            <label class="wide">API 地址<input v-model.trim="providerForm.base_url" required placeholder="https://api.example.com/v1"><small>远程服务必须使用 HTTPS；本地回环地址允许 HTTP。</small></label>
            <label>模型名称<input v-model.trim="providerForm.model" required placeholder="例如：deepseek-v4-flash"></label>
            <label>API 密钥<input v-model="providerForm.api_key" :required="providerForm.type!=='ollama' && !providerForm.has_api_key" type="password" autocomplete="new-password" placeholder="仅在 Manager 端加密保存"></label>
            <section class="ai-provider-options wide">
              <label class="ai-check-row"><input v-model="providerForm.enabled" type="checkbox"><span><b>启用此模型</b><small>停用后不会用于对话和定时分析。</small></span></label>
              <label class="ai-check-row"><input v-model="providerForm.is_default" type="checkbox"><span><b>设为默认分析模型</b><small>定时巡检和未指定模型的对话将使用它。</small></span></label>
            </section>
          </div>
          <footer><button type="button" class="secondary" @click="showProvider=false">取消</button><button class="primary" :disabled="busy==='provider'">{{ busy==='provider' ? '保存中…' : '保存模型配置' }}</button></footer>
        </form>
      </div>

      <div v-if="approvalPlan && approvalPlan.risk!=='low'" class="modal-backdrop ai-modal-backdrop" @click.self="approvalPlan=null">
        <section :class="['modal ai-approval-modal',approvalPlan.risk]">
          <header><div><span>{{ riskLabel(approvalPlan.risk) }} OPERATION</span><h3>审批执行计划</h3><p>请确认影响范围和执行证据。</p></div><button @click="approvalPlan=null">×</button></header>
          <div class="ai-approval-body">
            <div class="ai-approval-warning"><i>!</i><span><b>{{ approvalPlan.action_label }}</b><p>{{ approvalPlan.summary }}</p></span></div>
            <dl><div><dt>目标对象</dt><dd>{{ approvalPlan.target_name || approvalPlan.target_id }}<small>{{ approvalPlan.target_id }}</small></dd></div><div><dt>风险等级</dt><dd>{{ riskLabel(approvalPlan.risk) }}<small>有效至 {{ shortDate(approvalPlan.expires_at) }}</small></dd></div><div><dt>回滚说明</dt><dd>{{ approvalPlan.rollback || '失败时停止后续步骤，需根据任务日志人工恢复。' }}</dd></div></dl>
            <div v-if="approvalPlan.evidence?.length" class="ai-approval-evidence"><b>AI 判断依据</b><ul><li v-for="item in approvalPlan.evidence" :key="item">{{ item }}</li></ul></div>
            <label v-if="approvalPlan.risk==='medium'" class="ai-approval-check"><input v-model="mediumApproved" type="checkbox"><span><b>我已检查目标与影响范围，同意执行此计划</b><small>操作将进入任务中心并保留完整审计记录。</small></span></label>
            <label v-else class="ai-confirm-phrase"><span><b>二次确认</b><small>此操作可能造成服务中断。请逐字输入下方确认短语：</small></span><code>{{ approvalPlan.confirmation_phrase }}</code><input v-model="confirmation" autocomplete="off" placeholder="在此输入完整确认短语"></label>
          </div>
          <footer><button class="secondary" @click="approvalPlan=null">取消</button><button class="danger-button" :disabled="!canConfirm || busy===('plan:'+approvalPlan.id)" @click="executePlan()">{{ busy===('plan:'+approvalPlan.id) ? '正在提交…' : '确认并提交任务' }}</button></footer>
        </section>
      </div>
    </section>
  `
}
