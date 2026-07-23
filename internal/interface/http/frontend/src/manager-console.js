import { computed, onMounted, onUnmounted, ref } from 'vue'

const managerRequest = async (path, options = {}) => {
  const response = await fetch(`/api/v1${path}`, {
    ...options,
    headers: { 'Content-Type': 'application/json', ...(options.headers || {}) }
  })
  const raw = await response.text()
  let payload = {}
  try { payload = raw ? JSON.parse(raw) : {} } catch (_) { payload = {} }
  if (!response.ok) throw new Error(payload.error || raw || `HTTP ${response.status}`)
  return payload
}

const emptyHA = () => ({
  config: { enabled: false, vip: '', prefix: 24, interface: '', install_dir: '/opt/gmha', service_name: 'gmha-manager' },
  nodes: [], active_node_id: '', current_node_id: '', shared_database: false, ready: false, warnings: []
})

export default {
  name: 'ManagerConsole',
  props: { machines: { type: Array, default: () => [] } },
  emits: ['refresh', 'view-change'],
  setup(props, { emit }) {
    const view = ref('overview')
    const status = ref({ running: false, config: {} })
    const form = ref({})
    const ha = ref(emptyHA())
    const haForm = ref({ ...emptyHA().config })
    const nodeForm = ref({ machine_id: '', http_port: 8080, grpc_port: 9100, interface: '', install_dir: '/opt/gmha' })
    const rebuildForm = ref({ source_dir: '.', confirmation: '' })
    const databaseToken = ref('')
    const databaseTesting = ref(false)
    const databaseVerified = ref(false)
    const databaseChanged = ref(false)
    const busy = ref('')
    const notice = ref('')
    const error = ref('')
    const unreachable = ref(false)
    const runtimeFormLoaded = ref(false)
    const haFormLoaded = ref(false)
    const vipTargetID = ref('')
    const vipInterfaces = ref([])
    const vipInterfacesLoading = ref(false)
    const vipSelectedInterface = ref('')
    let timer = null

    const nodes = computed(() => ha.value.nodes || [])
    const activeNode = computed(() => nodes.value.find(item => item.id === ha.value.active_node_id))
    const vipTargetNode = computed(() => nodes.value.find(item => item.id === vipTargetID.value))
    const onlineCount = computed(() => nodes.value.filter(item => item.state === 'online').length)
    const canSaveDatabase = computed(() => !databaseChanged.value || databaseVerified.value)

    function setView(next) {
      view.value = next
      emit('view-change', next)
      if (next === 'ha') setTimeout(() => loadVIPInterfaces(false), 0)
    }

    async function loadVIPInterfaces(refresh = false) {
      if (!vipTargetID.value) {
        vipInterfaces.value = []
        return
      }
      vipInterfacesLoading.value = true
      error.value = ''
      try {
        const query = new URLSearchParams({
          node_id: vipTargetID.value,
          vip: haForm.value.vip || '',
          prefix: String(haForm.value.prefix || 24)
        })
        const result = await managerRequest(`/manager/ha/interfaces?${query}`, { method: refresh ? 'POST' : 'GET' })
        vipInterfaces.value = result.interfaces || []
        const saved = vipTargetNode.value?.vip_interface
        const candidate = saved && vipInterfaces.value.some(item => item.name === saved) ? saved : result.recommended
        if (!vipInterfaces.value.some(item => item.name === vipSelectedInterface.value)) {
          vipSelectedInterface.value = candidate || ''
        }
        if (vipTargetID.value === ha.value.active_node_id && !haForm.value.interface) {
          haForm.value.interface = vipSelectedInterface.value
        }
      } catch (err) {
        vipInterfaces.value = []
        error.value = `网卡读取失败：${err.message}`
      } finally {
        vipInterfacesLoading.value = false
      }
    }

    async function vipTargetChanged() {
      vipSelectedInterface.value = vipTargetNode.value?.vip_interface || ''
      await loadVIPInterfaces(true)
    }

    function vipInterfaceChanged() {
      if (vipTargetID.value === ha.value.active_node_id) {
        haForm.value.interface = vipSelectedInterface.value
      }
    }

    function resetDatabaseVerification() {
      databaseToken.value = ''
      databaseVerified.value = false
      databaseChanged.value = true
    }

    function chooseDatabase(driver) {
      form.value.database_driver = driver
      form.value.database_password = ''
      form.value.database_port = driver === 'mysql' ? 3306 : driver === 'postgres' ? 5432 : 0
      if (driver === 'postgres' && !form.value.database_ssl_mode) form.value.database_ssl_mode = 'disable'
      resetDatabaseVerification()
    }

    async function load(silent = false, resetForms = false) {
      try {
        const [runtime, topology] = await Promise.all([managerRequest('/manager/status'), managerRequest('/manager/ha')])
        status.value = runtime
        unreachable.value = false
        if ((!runtimeFormLoaded.value || resetForms) && !busy.value && !databaseTesting.value) {
          form.value = { ...runtime.config, database_password: '' }
          runtimeFormLoaded.value = true
          databaseChanged.value = false
        }
        ha.value = topology || emptyHA()
        if (!nodes.value.some(item => item.id === vipTargetID.value && item.state === 'online')) {
          vipTargetID.value = topology?.active_node_id || nodes.value.find(item => item.state === 'online')?.id || ''
        }
        if ((!haFormLoaded.value || resetForms) && !busy.value) {
          haForm.value = { ...emptyHA().config, ...(topology?.config || {}) }
          haFormLoaded.value = true
        }
      } catch (err) {
        unreachable.value = true
        if (!silent) error.value = err.message
      }
    }

    async function testDatabase() {
      databaseTesting.value = true
      error.value = ''; notice.value = ''
      try {
        const result = await managerRequest('/manager/database/test', { method: 'POST', body: JSON.stringify(form.value) })
        databaseToken.value = result.test_token
        databaseVerified.value = true
        notice.value = `${result.message}（${result.address}）`
      } catch (err) {
        resetDatabaseVerification()
        error.value = err.message
      } finally { databaseTesting.value = false }
    }

    async function saveRuntime() {
      if (!canSaveDatabase.value) { error.value = '数据库参数有变化，请先检测连接。'; return }
      busy.value = 'save-runtime'; error.value = ''; notice.value = ''
      try {
        await managerRequest('/manager/config', { method: 'PUT', body: JSON.stringify({ ...form.value, test_token: databaseToken.value }) })
        notice.value = 'Manager 配置已保存；重启后使用新配置。'
        resetDatabaseVerification()
        await load(false, true)
        emit('refresh')
      } catch (err) { error.value = err.message } finally { busy.value = '' }
    }

    async function runtimeAction(action) {
      const label = { restart: '重启', stop: '关闭', start: '启动' }[action]
      if (!confirm(`确认${label}当前 Manager？${action === 'stop' ? ' 控制台连接将中断。' : ''}`)) return
      busy.value = action; error.value = ''; notice.value = ''
      try {
        await managerRequest(`/manager/${action}`, { method: 'POST', body: JSON.stringify({ config: form.value }) })
        notice.value = `Manager 已提交${label}。`
        if (action === 'stop') {
          status.value.running = false
          unreachable.value = true
          return
        }
        for (let i = 0; i < 30; i++) {
          await new Promise(resolve => setTimeout(resolve, 600))
          await load(true)
          if (status.value.running) break
        }
      } catch (err) {
        if (action === 'stop') {
          notice.value = 'Manager 已关闭，当前页面连接已断开。'
          unreachable.value = true
        } else error.value = err.message
      } finally { busy.value = '' }
    }

    async function saveHA() {
      if (vipTargetID.value === ha.value.active_node_id && vipSelectedInterface.value) {
        haForm.value.interface = vipSelectedInterface.value
      }
      busy.value = 'save-ha'; error.value = ''; notice.value = ''
      try {
        await managerRequest('/manager/ha/config', { method: 'PUT', body: JSON.stringify(haForm.value) })
        notice.value = haForm.value.enabled ? 'Manager 高可用配置已保存；请重启当前节点，使 Agent 与下载地址统一使用 VIP。' : 'Manager 高可用配置已保存。'
        await load(false, true)
      } catch (err) { error.value = err.message } finally { busy.value = '' }
    }

    async function addNode() {
      busy.value = 'add-node'; error.value = ''; notice.value = ''
      try {
        const node = await managerRequest('/manager/ha/nodes', { method: 'POST', body: JSON.stringify(nodeForm.value) })
        notice.value = `已创建 ${node.name || node.ip} 的 Manager 安装任务。`
        await load()
        emit('refresh')
      } catch (err) { error.value = err.message } finally { busy.value = '' }
    }

    async function nodeAction(node, action) {
      const label = { start: '启动', restart: '重启', stop: '关闭' }[action]
      if (!confirm(`确认${label}节点 ${node.name || node.ip}？`)) return
      busy.value = `${action}-${node.id}`; error.value = ''
      try {
        const result = await managerRequest('/manager/ha/nodes/action', { method: 'POST', body: JSON.stringify({ node_id: node.id, action }) })
        notice.value = `节点操作已提交，任务 ${result.task_id}。`
        await load()
      } catch (err) { error.value = err.message } finally { busy.value = '' }
    }

    async function switchVIP(node) {
      if (!node) { error.value = '请选择 VIP 目标 Manager 节点。'; return }
      const sameNode = node.id === ha.value.active_node_id
      const prompt = sameNode
        ? `确认在 ${node.name || node.ip} 绑定或刷新 VIP ${haForm.value.vip}？`
        : `确认将 VIP ${haForm.value.vip} 漂移到 ${node.name || node.ip}？`
      if (!confirm(prompt)) return
      busy.value = `vip-${node.id}`; error.value = ''
      try {
        await managerRequest('/manager/ha/vip/switch', { method: 'POST', body: JSON.stringify({ target_node_id: node.id, interface: vipSelectedInterface.value }) })
        notice.value = sameNode
          ? 'VIP 绑定或刷新已开始，并将发送免费 ARP 更新网络邻居。'
          : 'VIP 漂移已开始：先释放原节点，再由目标节点接管并发送免费 ARP。'
        await load()
      } catch (err) { error.value = err.message } finally { busy.value = '' }
    }

    async function rebuild() {
      if (rebuildForm.value.confirmation !== 'REBUILD') { error.value = '请输入 REBUILD 确认重编译。'; return }
      busy.value = 'rebuild'; error.value = ''; notice.value = ''
      try {
        const job = await managerRequest('/upgrades/manager/rebuild', { method: 'POST', body: JSON.stringify(rebuildForm.value) })
        notice.value = `重编译任务 ${job.id} 已启动；完成安装后 Manager 会自动重启。`
        rebuildForm.value.confirmation = ''
      } catch (err) { error.value = err.message } finally { busy.value = '' }
    }

    function machineLabel(machine) { return `${machine.Name || machine.name || machine.IP || machine.ip} · ${machine.IP || machine.ip}` }
    function nodeStateLabel(value) { return ({ online: '在线', offline: '离线', installing: '安装中', restarting: '重启中', switching: '切换中', error: '异常' })[value] || value || '未知' }

    onMounted(() => {
      load()
      timer = setInterval(() => load(true), 5000)
    })
    onUnmounted(() => clearInterval(timer))

    return {
      view, status, form, ha, haForm, nodeForm, rebuildForm, databaseTesting, databaseVerified, databaseChanged,
      busy, notice, error, unreachable, nodes, activeNode, onlineCount, canSaveDatabase, vipTargetID, vipTargetNode,
      vipInterfaces, vipInterfacesLoading, vipSelectedInterface,
      setView, chooseDatabase, resetDatabaseVerification, load, testDatabase, saveRuntime, runtimeAction,
      saveHA, addNode, nodeAction, switchVIP, rebuild, loadVIPInterfaces, vipTargetChanged, vipInterfaceChanged, machineLabel, nodeStateLabel
    }
  },
  template: `
    <div :class="['manager-console','view-'+view]">
      <section class="manager-page-head">
        <div><p>MANAGER CONTROL PLANE</p><h2>Manager 控制台</h2><span>管理运行状态、元数据库、高可用拓扑与内核维护。</span></div>
        <div class="manager-page-status"><i :class="status.running && !unreachable ? 'online' : 'offline'"></i><span><b>{{ status.running && !unreachable ? '当前节点在线' : '当前节点不可达' }}</b><small>{{ status.version || '版本未上报' }} · {{ status.pid ? 'PID ' + status.pid : '未发现 PID' }}</small></span><button type="button" class="secondary" @click="load()">刷新</button></div>
      </section>
      <nav class="manager-tabs" aria-label="Manager 控制台子页面">
        <button type="button" :class="{active:view==='overview'}" @click="setView('overview')"><i>◫</i><span><b>运行概览</b><small>状态与节点控制</small></span></button>
        <button type="button" :class="{active:view==='database'}" @click="setView('database')"><i>DB</i><span><b>元数据库</b><small>连接检测与切换</small></span></button>
        <button type="button" :class="{active:view==='ha'}" @click="setView('ha')"><i>HA</i><span><b>高可用与 VIP</b><small>拓扑、网卡与漂移</small></span></button>
        <button type="button" :class="{active:view==='maintenance'}" @click="setView('maintenance')"><i>↑</i><span><b>内核维护</b><small>重编译与版本升级</small></span></button>
      </nav>
      <div v-if="notice" class="manager-inline-message success">{{ notice }}</div>
      <div v-if="error" class="manager-inline-message error">{{ error }}</div>

      <template v-if="view==='overview'">
        <section class="manager-kpis">
          <article><small>Manager 节点</small><b>{{ nodes.length }}</b><span>{{ onlineCount }} 个在线</span></article>
          <article><small>当前主节点</small><b>{{ activeNode?.name || '未选举' }}</b><span>{{ activeNode?.ip || '等待拓扑就绪' }}</span></article>
          <article><small>接入地址 VIP</small><b>{{ ha.config?.vip || '未配置' }}</b><span>{{ activeNode?.vip_interface || ha.config?.interface || '尚未选择网卡' }}</span></article>
          <article><small>元数据库</small><b>{{ form.database_driver || 'sqlite' }}</b><span>{{ ha.shared_database ? '共享存储已就绪' : '仅支持单节点' }}</span></article>
        </section>
        <section class="manager-overview-grid">
          <article class="panel manager-runtime-panel">
            <header><div><span>LOCAL RUNTIME</span><h3>当前节点运行控制</h3><p>运行参数与生命周期操作集中显示，不再占用整列高度。</p></div><div class="manager-runtime-actions"><button type="button" class="secondary" :disabled="!!busy || unreachable" @click="runtimeAction('restart')">重启</button><button type="button" class="danger-button" :disabled="!!busy || unreachable" @click="runtimeAction('stop')">关闭</button></div></header>
            <dl><div><dt>HTTP 监听</dt><dd>{{ form.listen_http || '—' }}</dd></div><div><dt>gRPC 监听</dt><dd>{{ form.listen_grpc || '—' }}</dd></div><div><dt>启动时间</dt><dd>{{ status.started_at ? new Date(status.started_at).toLocaleString('zh-CN') : '—' }}</dd></div><div><dt>日志文件</dt><dd>{{ status.log_path || '由系统服务管理' }}</dd></div></dl>
          </article>
          <article class="panel manager-readiness-panel">
            <header><div><span>AVAILABILITY</span><h3>高可用就绪情况</h3><p>快速确认共享数据库、节点、VIP 和当前持有者。</p></div><strong :class="ha.ready ? 'ready' : 'warning'">{{ ha.ready ? '已就绪' : '待配置' }}</strong></header>
            <ul><li :class="{ok:ha.shared_database}"><i>{{ ha.shared_database ? '✓' : '1' }}</i><span><b>共享元数据库</b><small>{{ ha.shared_database ? '已使用 MySQL / PostgreSQL' : '当前为 SQLite' }}</small></span></li><li :class="{ok:nodes.length>=2}"><i>{{ nodes.length>=2 ? '✓' : '2' }}</i><span><b>Manager 多节点</b><small>{{ nodes.length }} 个节点，{{ onlineCount }} 个在线</small></span></li><li :class="{ok:ha.config?.enabled&&ha.config?.vip}"><i>{{ ha.config?.enabled&&ha.config?.vip ? '✓' : '3' }}</i><span><b>统一接入 VIP</b><small>{{ ha.config?.vip || '尚未配置' }}</small></span></li></ul>
            <footer><button type="button" class="text-button" @click="setView('ha')">进入高可用与 VIP →</button></footer>
          </article>
        </section>
      </template>

      <section v-else-if="view==='database'" class="manager-database-page">
        <aside class="manager-database-nav">
          <header><span>DATABASE TYPE</span><b>选择元数据库</b><small>切换只改变右侧子页面，不影响控制台整体布局。</small></header>
          <button type="button" :class="{active:form.database_driver==='sqlite'}" @click="chooseDatabase('sqlite')"><i>S</i><span><b>SQLite</b><small>本地文件 · 单节点</small></span><em v-if="form.database_driver==='sqlite'">当前</em></button>
          <button type="button" :class="{active:form.database_driver==='mysql'}" @click="chooseDatabase('mysql')"><i>M</i><span><b>MySQL</b><small>共享存储 · 推荐</small></span><em v-if="form.database_driver==='mysql'">当前</em></button>
          <button type="button" :class="{active:form.database_driver==='postgres'}" @click="chooseDatabase('postgres')"><i>P</i><span><b>PostgreSQL</b><small>共享存储 · 可用</small></span><em v-if="form.database_driver==='postgres'">当前</em></button>
          <footer><b>安全切换规则</b><small>连接检测成功后才能保存；平台投入使用后禁止直接更换元数据库。</small></footer>
        </aside>
        <form class="panel manager-database-editor" @submit.prevent="saveRuntime">
          <header><div><span>METADATA DATABASE / {{ (form.database_driver || 'sqlite').toUpperCase() }}</span><h3>{{ form.database_driver==='sqlite' ? 'SQLite 文件数据库' : form.database_driver==='mysql' ? 'MySQL 连接配置' : 'PostgreSQL 连接配置' }}</h3><p>填写常规连接信息即可，系统负责生成并保管连接串。</p></div><em :class="{verified:databaseVerified}">{{ databaseVerified ? '连接已验证' : databaseChanged ? '需要检测' : '配置未变更' }}</em></header>
          <div class="manager-database-editor-body">
            <div v-if="form.database_driver==='sqlite'" class="manager-form-fields one"><label>数据库文件路径<input v-model.trim="form.db_path" @input="resetDatabaseVerification" required placeholder="./data/manager.db"><small>适合单节点初始化；启用 Manager 高可用前需要切换为共享数据库。</small></label></div>
            <div v-else class="manager-form-fields">
              <label>数据库地址<input v-model.trim="form.database_host" @input="resetDatabaseVerification" required placeholder="10.0.0.20"></label><label>端口<input v-model.number="form.database_port" @input="resetDatabaseVerification" type="number" min="1" max="65535" required></label><label>数据库名称<input v-model.trim="form.database_name" @input="resetDatabaseVerification" required placeholder="gmha"></label><label>数据库账号<input v-model.trim="form.database_username" @input="resetDatabaseVerification" required placeholder="gmha"></label><label>数据库密码<input v-model="form.database_password" @input="resetDatabaseVerification" type="password" :placeholder="form.database_password_set ? '已配置；留空表示保持原密码' : '请输入数据库密码'" autocomplete="new-password"></label><label v-if="form.database_driver==='postgres'">SSL 模式<select v-model="form.database_ssl_mode" @change="resetDatabaseVerification"><option value="disable">disable</option><option value="require">require</option><option value="verify-ca">verify-ca</option><option value="verify-full">verify-full</option></select></label>
            </div>
            <details class="manager-advanced-config"><summary>服务监听与路径高级配置</summary><div class="manager-form-fields"><label>HTTP 监听<input v-model.trim="form.listen_http"></label><label>gRPC 监听<input v-model.trim="form.listen_grpc"></label><label>Manager HTTP 地址<input v-model.trim="form.manager_http_addr"></label><label>Manager gRPC 地址<input v-model.trim="form.manager_grpc_addr"></label><label>Agent 二进制路径<input v-model.trim="form.agent_binary_path"></label><label>SSH 公钥路径<input v-model.trim="form.manager_public_key"></label></div></details>
          </div>
          <footer><span>{{ databaseChanged ? '参数已变化，请先检测连接。' : '当前配置可以直接保存。' }}</span><div><button type="button" class="secondary" :disabled="databaseTesting || !!busy" @click="testDatabase">{{ databaseTesting ? '检测中…' : '检测连接' }}</button><button class="primary" :disabled="!!busy || !canSaveDatabase">{{ busy==='save-runtime' ? '保存中…' : '保存配置' }}</button></div></footer>
        </form>
      </section>

      <template v-else-if="view==='ha'">
        <section class="panel manager-topology-panel">
          <header><div><span>HIGH AVAILABILITY TOPOLOGY</span><h3>Manager 高可用拓扑</h3><p>拓扑只展示运行关系；所有 VIP 配置和漂移统一在下方一个区域完成。</p></div><strong :class="ha.ready ? 'ready' : 'warning'">{{ ha.ready ? '高可用已就绪' : '配置未完成' }}</strong></header>
          <div class="manager-topology-canvas">
            <article class="manager-client-node"><i>APP</i><span><b>管理入口</b><small>浏览器 / Agent / API</small></span></article><div class="manager-topology-line"></div>
            <article :class="['manager-vip-node',{ready:ha.config?.enabled && ha.config?.vip}]"><i>VIP</i><span><b>{{ ha.config?.vip || '等待配置 VIP' }}</b><small>{{ activeNode?.vip_interface || ha.config?.interface || '未选择网卡' }} · /{{ ha.config?.prefix || 24 }}</small></span></article><div class="manager-topology-branch"></div>
            <div class="manager-node-row"><article v-for="node in nodes" :key="node.id" :class="['manager-node-card',node.role,node.state]"><header><i></i><span>{{ node.role==='active' ? 'ACTIVE' : 'STANDBY' }}</span><em v-if="node.id===ha.current_node_id">当前</em></header><h4>{{ node.name || node.ip }}</h4><p>{{ node.ip }}</p><dl><div><dt>HTTP</dt><dd>{{ node.http_address }}</dd></div><div><dt>VIP 网卡</dt><dd>{{ node.vip_interface || '待推荐' }}</dd></div><div><dt>版本</dt><dd>{{ node.version || '未知' }}</dd></div><div><dt>状态</dt><dd>{{ nodeStateLabel(node.state) }}</dd></div></dl></article><article v-if="!nodes.length" class="manager-node-empty">启动 Manager 后将自动注册当前节点。</article></div>
            <div class="manager-topology-line database"></div><article :class="['manager-shared-db',{ready:ha.shared_database}]"><i>DB</i><span><b>{{ ha.shared_database ? '共享元数据库' : '本地 SQLite' }}</b><small>{{ form.database_driver }} · {{ form.database_host || form.db_path || '未配置' }}</small></span></article>
          </div>
          <div v-if="ha.warnings?.length" class="manager-ha-warnings"><p v-for="item in ha.warnings" :key="item">{{ item }}</p></div>
        </section>

        <section class="panel manager-vip-workspace">
          <header><div><span>MANAGER ACCESS ENDPOINT</span><h3>VIP 管理</h3><p>参照集群 VIP 管理：选择目标节点、扫描网卡、自动推荐并执行唯一入口漂移。</p></div><label class="manager-switch"><input v-model="haForm.enabled" type="checkbox"><i></i><span>{{ haForm.enabled ? '已启用' : '未启用' }}</span></label></header>
          <form @submit.prevent="saveHA">
            <div class="manager-vip-form-grid">
              <label>Manager VIP<input v-model.trim="haForm.vip" required placeholder="例如 10.0.0.100" @change="loadVIPInterfaces(false)"></label>
              <label>网络前缀<input v-model.number="haForm.prefix" type="number" min="1" max="128" required @change="loadVIPInterfaces(false)"></label>
              <label>目标持有节点<select v-model="vipTargetID" required @change="vipTargetChanged"><option value="">请选择在线节点</option><option v-for="node in nodes.filter(item=>item.state==='online')" :key="node.id" :value="node.id">{{ node.name || node.ip }} · {{ node.role==='active' ? '当前持有' : '备用' }}</option></select></label>
              <label class="manager-vip-interface-field">目标 VIP 网卡<div><select v-model="vipSelectedInterface" required @change="vipInterfaceChanged"><option value="">{{ vipInterfacesLoading ? '正在扫描节点网卡…' : '请选择网卡' }}</option><option v-for="item in vipInterfaces" :key="item.name" :value="item.name">{{ item.name }} · {{ item.ips.join(' / ') }}{{ item.recommended ? ' · 推荐' : '' }}</option></select><button type="button" class="secondary" :disabled="vipInterfacesLoading || !vipTargetID" @click="loadVIPInterfaces(true)">{{ vipInterfacesLoading ? '扫描中…' : '重新扫描' }}</button></div><small v-if="vipInterfaces.length">推荐 {{ vipInterfaces.find(item=>item.recommended)?.name || '—' }}：{{ vipInterfaces.find(item=>item.recommended)?.reason || '根据节点网络自动选择' }}</small><small v-else>选择节点后将显示该节点全部可用网卡，并优先推荐与 VIP 同网段的接口。</small></label>
              <label>Manager 安装目录<input v-model.trim="haForm.install_dir" placeholder="/opt/gmha"></label>
            </div>
            <footer><ol><li><i>1</i>扫描目标网卡</li><li><i>2</i>释放原节点 VIP</li><li><i>3</i>绑定目标接口</li><li><i>4</i>发送免费 ARP</li></ol><div><button type="submit" class="secondary" :disabled="!!busy">{{ busy==='save-ha' ? '保存中…' : '保存 VIP 配置' }}</button><button type="button" class="primary" :disabled="!!busy || !haForm.enabled || !vipTargetNode || !vipSelectedInterface" @click="switchVIP(vipTargetNode)">{{ vipTargetID===ha.active_node_id ? '绑定/刷新 VIP' : '安全漂移 VIP' }}</button></div></footer>
          </form>
          <div class="manager-vip-current"><span :class="['status',ha.config?.enabled&&ha.config?.vip?'success':'offline']">{{ ha.config?.enabled&&ha.config?.vip ? '已配置' : '未配置' }}</span><div><small>当前 Manager VIP</small><b>{{ ha.config?.vip ? ha.config.vip + '/' + ha.config.prefix : '尚未配置' }}</b></div><div><small>当前持有节点</small><b>{{ activeNode?.name || '未选举' }}</b></div><div><small>当前网卡</small><b>{{ activeNode?.vip_interface || ha.config?.interface || '待检测' }}</b></div></div>
        </section>

        <section class="manager-ha-lower">
          <form class="panel manager-add-node-panel" @submit.prevent="addNode"><header><div><span>EXPAND MANAGER</span><h3>添加 Manager 节点</h3><p>选择已纳管且 Agent 在线的机器，自动安装并加入拓扑。</p></div></header><div class="manager-form-fields"><label class="wide">目标机器<select v-model="nodeForm.machine_id" required><option value="">请选择机器</option><option v-for="machine in machines" :key="machine.ID || machine.id" :value="machine.ID || machine.id">{{ machineLabel(machine) }}</option></select></label><label>HTTP 端口<input v-model.number="nodeForm.http_port" type="number" min="1" max="65535"></label><label>gRPC 端口<input v-model.number="nodeForm.grpc_port" type="number" min="1" max="65535"></label><label class="wide">安装目录<input v-model.trim="nodeForm.install_dir"></label></div><footer><button class="primary" :disabled="!!busy || !ha.shared_database">{{ busy==='add-node' ? '正在创建安装任务…' : '安装并加入拓扑' }}</button></footer></form>
          <section class="panel manager-node-operations"><header><div><span>NODE LIFECYCLE</span><h3>节点生命周期</h3><p>节点启停与 VIP 管理解耦，避免同一卡片堆叠过多操作。</p></div><em>{{ onlineCount }}/{{ nodes.length }} 在线</em></header><div><article v-for="node in nodes" :key="'op-'+node.id"><i :class="node.state"></i><span><b>{{ node.name || node.ip }}</b><small>{{ node.ip }} · {{ nodeStateLabel(node.state) }}</small></span><div v-if="node.id!==ha.current_node_id"><button v-if="node.state!=='online'" type="button" class="text-button" :disabled="!!busy" @click="nodeAction(node,'start')">启动</button><button v-if="node.state==='online'" type="button" class="text-button" :disabled="!!busy" @click="nodeAction(node,'restart')">重启</button><button v-if="node.state==='online' && node.role!=='active'" type="button" class="danger-link" :disabled="!!busy" @click="nodeAction(node,'stop')">关闭</button></div><em v-else>当前节点</em></article><p v-if="!nodes.length">暂无 Manager 节点。</p></div></section>
        </section>
      </template>

      <template v-else>
        <section class="panel manager-rebuild-panel"><header><div><span>MANAGER KERNEL</span><h3>内核重编译、安装与重启</h3><p>候选程序自检通过后备份当前内核、原子安装并自动重启。</p></div><strong>高风险运维</strong></header><form @submit.prevent="rebuild"><label>源码目录<input v-model.trim="rebuildForm.source_dir" required placeholder="/opt/gmha-src"></label><label>输入 REBUILD 确认<input v-model.trim="rebuildForm.confirmation" autocomplete="off" placeholder="REBUILD"></label><button class="danger-button" :disabled="!!busy || rebuildForm.confirmation!=='REBUILD'">{{ busy==='rebuild' ? '正在启动任务…' : '重编译并安装重启' }}</button></form></section>
        <section class="manager-maintenance-note"><i>↑</i><span><b>版本升级</b><small>升级制品、版本关系与执行记录统一显示在本子页面下方。</small></span></section>
      </template>
    </div>
  `
}
