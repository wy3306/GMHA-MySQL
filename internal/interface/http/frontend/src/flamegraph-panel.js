import { computed, onMounted, onUnmounted, ref } from 'vue'
import './flamegraph-panel.css'

const request = async (path, options = {}) => {
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

const localDateTime = value => {
  const date = value instanceof Date ? value : new Date(value)
  const offset = date.getTimezoneOffset()
  return new Date(date.getTime() - offset * 60000).toISOString().slice(0, 16)
}

const machineField = (machine, upper, lower) => machine?.[upper] ?? machine?.[lower] ?? ''

function parseFoldedStacks(source) {
  const root = { name: '全部样本', value: 0, children: new Map(), path: '' }
  for (const rawLine of String(source || '').split(/\r?\n/)) {
    const line = rawLine.trim()
    if (!line) continue
    const separator = line.lastIndexOf(' ')
    if (separator <= 0) continue
    const count = Number(line.slice(separator + 1))
    if (!Number.isFinite(count) || count <= 0) continue
    const frames = line.slice(0, separator).split(';').filter(Boolean)
    root.value += count
    let node = root
    const path = []
    for (const frame of frames) {
      path.push(frame)
      if (!node.children.has(frame)) node.children.set(frame, { name: frame, value: 0, children: new Map(), path: path.join(';') })
      node = node.children.get(frame)
      node.value += count
    }
  }
  return root
}

function findNode(root, path) {
  if (!path) return root
  let node = root
  for (const name of path.split(';')) {
    node = node.children.get(name)
    if (!node) return root
  }
  return node
}

function frameColor(name, depth) {
  let hash = depth * 131
  for (let i = 0; i < name.length; i += 1) hash = ((hash << 5) - hash + name.charCodeAt(i)) | 0
  const hue = 9 + Math.abs(hash % 35)
  const saturation = 72 + Math.abs(hash % 18)
  const lightness = 53 + Math.abs((hash >> 3) % 14)
  return `hsl(${hue} ${saturation}% ${lightness}%)`
}

function layoutFlameGraph(root, zoomPath, search) {
  const width = 1200
  const rowHeight = 29
  const focus = findNode(root, zoomPath)
  const total = focus.value || 1
  const frames = []
  let maxDepth = 0
  const visit = (node, x, depth) => {
    const children = [...node.children.values()].sort((a, b) => b.value - a.value || a.name.localeCompare(b.name))
    let cursor = x
    for (const child of children) {
      const frameWidth = child.value / total * width
      if (frameWidth < 0.35) {
        cursor += frameWidth
        continue
      }
      maxDepth = Math.max(maxDepth, depth)
      frames.push({
        ...child, x: cursor, y: depth * rowHeight, width: frameWidth, height: rowHeight - 2,
        color: frameColor(child.name, depth),
        percent: child.value / total * 100,
        matched: search && child.name.toLowerCase().includes(search.toLowerCase())
      })
      visit(child, cursor, depth + 1)
      cursor += frameWidth
    }
  }
  visit(focus, 0, 0)
  return { frames, width, height: Math.max(90, (maxDepth + 1) * rowHeight), total, focus }
}

export default {
  name: 'FlameGraphPanel',
  props: {
    cluster: { type: Object, required: true },
    machines: { type: Array, default: () => [] }
  },
  setup(props) {
    const section = ref('profiles')
    const profiles = ref([])
    const schedules = ref([])
    const selected = ref(null)
    const loading = ref(false)
    const error = ref('')
    const notice = ref('')
    const captureOpen = ref(false)
    const scheduleOpen = ref(false)
    const search = ref('')
    const zoomPath = ref('')
    const captureForm = ref({ machine_id: '', target_type: 'system', target: '', duration_seconds: 30, frequency_hz: 99, backend: 'auto' })
    const scheduleForm = ref({ name: '', machine_id: '', target_type: 'system', target: '', duration_seconds: 30, frequency_hz: 99, backend: 'auto', schedule_type: 'once', interval_minutes: 60, start_at: localDateTime(new Date(Date.now() + 5 * 60000)), enabled: true })
    let refreshTimer = null

    const clusterName = computed(() => machineField(props.cluster, 'Name', 'name'))
    const eligibleMachines = computed(() => props.machines.filter(machine => {
      const machineCluster = machineField(machine, 'Cluster', 'cluster')
      return !clusterName.value || !machineCluster || machineCluster === clusterName.value
    }))
    const tree = computed(() => parseFoldedStacks(selected.value?.folded_stacks))
    const chart = computed(() => layoutFlameGraph(tree.value, zoomPath.value, search.value.trim()))
    const hasPending = computed(() => profiles.value.some(item => ['pending', 'sent', 'running'].includes(item.status)))

    function ensureMachineDefaults() {
      const first = eligibleMachines.value[0]
      const id = first ? machineField(first, 'ID', 'id') : ''
      if (!captureForm.value.machine_id) captureForm.value.machine_id = id
      if (!scheduleForm.value.machine_id) scheduleForm.value.machine_id = id
    }

    async function loadProfiles(silent = false) {
      if (!silent) loading.value = true
      try {
        const query = new URLSearchParams({ cluster: clusterName.value, limit: '100' })
        const payload = await request(`/performance/flamegraphs?${query}`)
        profiles.value = Array.isArray(payload.items) ? payload.items : []
        if (selected.value) {
          const current = profiles.value.find(item => item.id === selected.value.id)
          if (current && current.status !== selected.value.status) await openProfile(current)
        }
      } catch (err) {
        if (!silent) error.value = err.message
      } finally {
        if (!silent) loading.value = false
      }
    }

    async function loadSchedules(silent = false) {
      try {
        const query = new URLSearchParams({ cluster: clusterName.value })
        const payload = await request(`/performance/flamegraphs/schedules?${query}`)
        schedules.value = Array.isArray(payload.items) ? payload.items : []
      } catch (err) {
        if (!silent) error.value = err.message
      }
    }

    async function refresh(silent = false) {
      ensureMachineDefaults()
      await Promise.all([loadProfiles(silent), loadSchedules(silent)])
    }

    async function submitCapture() {
      error.value = ''
      loading.value = true
      try {
        if (captureForm.value.target_type === 'system' && captureForm.value.backend === 'procfs') captureForm.value.backend = 'auto'
        await request('/performance/flamegraphs', { method: 'POST', body: JSON.stringify(captureForm.value) })
        captureOpen.value = false
        notice.value = '采集任务已创建，Agent 完成采样后会自动显示。'
        await loadProfiles(true)
      } catch (err) {
        error.value = err.message
      } finally {
        loading.value = false
      }
    }

    async function submitSchedule() {
      error.value = ''
      loading.value = true
      try {
        if (scheduleForm.value.target_type === 'system' && scheduleForm.value.backend === 'procfs') scheduleForm.value.backend = 'auto'
        const payload = { ...scheduleForm.value, start_at: new Date(scheduleForm.value.start_at).toISOString() }
        await request('/performance/flamegraphs/schedules', { method: 'POST', body: JSON.stringify(payload) })
        scheduleOpen.value = false
        notice.value = '自动采集任务已保存。'
        await loadSchedules(true)
      } catch (err) {
        error.value = err.message
      } finally {
        loading.value = false
      }
    }

    async function openProfile(item) {
      error.value = ''
      try {
        selected.value = await request(`/performance/flamegraphs/${encodeURIComponent(item.id)}`)
        zoomPath.value = ''
        search.value = ''
      } catch (err) {
        error.value = err.message
      }
    }

    async function removeProfile(item) {
      if (!window.confirm(`删除火焰图记录 ${item.id}？`)) return
      try {
        await request(`/performance/flamegraphs/${encodeURIComponent(item.id)}`, { method: 'DELETE' })
        if (selected.value?.id === item.id) selected.value = null
        await loadProfiles(true)
      } catch (err) { error.value = err.message }
    }

    async function runSchedule(item) {
      try {
        await request(`/performance/flamegraphs/schedules/${encodeURIComponent(item.id)}/run`, { method: 'POST' })
        notice.value = '已立即发起一次采集。'
        section.value = 'profiles'
        await loadProfiles(true)
      } catch (err) { error.value = err.message }
    }

    async function removeSchedule(item) {
      if (!window.confirm(`删除自动任务“${item.name}”？`)) return
      try {
        await request(`/performance/flamegraphs/schedules/${encodeURIComponent(item.id)}`, { method: 'DELETE' })
        await loadSchedules(true)
      } catch (err) { error.value = err.message }
    }

    function exportFolded() {
      if (!selected.value?.folded_stacks) return
      const blob = new Blob([selected.value.folded_stacks], { type: 'text/plain;charset=utf-8' })
      const url = URL.createObjectURL(blob)
      const link = document.createElement('a')
      link.href = url
      link.download = `${selected.value.id}.folded`
      link.click()
      URL.revokeObjectURL(url)
    }

    function statusLabel(status) {
      return ({ pending: '等待下发', sent: '已下发', running: '采集中', success: '已完成', failed: '失败' })[status] || status
    }
    function targetLabel(item) {
      if (item.target_type === 'system') return '全系统'
      return item.target_type === 'pid' ? `PID ${item.target}` : `进程 ${item.target}`
    }
    function backendLabel(value) {
      return ({ auto: '自动选择', perf: 'perf', procfs: '/proc 兼容模式' })[value] || value || '等待采集'
    }
    function scheduleLabel(item) {
      if (item.schedule_type === 'once') return '单次'
      if (item.schedule_type === 'daily') return '每日'
      return `每 ${item.interval_minutes} 分钟`
    }
    function date(value) { return value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '—' }

    onMounted(() => {
      refresh()
      refreshTimer = window.setInterval(() => {
        if (hasPending.value) loadProfiles(true)
      }, 4000)
    })
    onUnmounted(() => { if (refreshTimer) window.clearInterval(refreshTimer) })

    return {
      section, profiles, schedules, selected, loading, error, notice, captureOpen, scheduleOpen,
      search, zoomPath, captureForm, scheduleForm, eligibleMachines, chart, refresh, submitCapture,
      submitSchedule, openProfile, removeProfile, runSchedule, removeSchedule, exportFolded,
      statusLabel, targetLabel, backendLabel, scheduleLabel, date, machineField
    }
  },
  template: `
    <section class="flamegraph-panel">
      <header class="flamegraph-toolbar">
        <div>
          <b>Linux 火焰图</b>
          <span>按采样窗口记录 CPU 调用栈，支持离线 Agent、持久化回看与自动执行。</span>
        </div>
        <div class="flamegraph-actions">
          <button type="button" class="secondary" :disabled="loading" @click="refresh()">刷新</button>
          <button type="button" class="secondary" @click="scheduleOpen=true">新建自动任务</button>
          <button type="button" class="primary" :disabled="!eligibleMachines.length" @click="captureOpen=true">立即采集</button>
        </div>
      </header>
      <nav class="flamegraph-sections">
        <button type="button" :class="{active:section==='profiles'}" @click="section='profiles'">采集记录 <em>{{ profiles.length }}</em></button>
        <button type="button" :class="{active:section==='schedules'}" @click="section='schedules'">自动任务 <em>{{ schedules.length }}</em></button>
      </nav>
      <div v-if="error" class="flamegraph-message error"><b>操作失败</b><span>{{ error }}</span><button type="button" @click="error=''">×</button></div>
      <div v-if="notice" class="flamegraph-message success"><span>{{ notice }}</span><button type="button" @click="notice=''">×</button></div>

      <template v-if="section==='profiles'">
        <div class="flamegraph-layout">
          <aside class="flamegraph-history">
            <article v-for="item in profiles" :key="item.id" :class="{active:selected?.id===item.id}" @click="openProfile(item)">
              <header><b>{{ machineField(item,'MachineName','machine_name') || item.machine_ip || item.machine_id }}</b><em :class="item.status">{{ statusLabel(item.status) }}</em></header>
              <p>{{ targetLabel(item) }} · {{ item.duration_seconds }} 秒 · {{ item.frequency_hz }} Hz</p>
              <small>{{ date(item.created_at) }}</small>
              <button type="button" title="删除记录" @click.stop="removeProfile(item)">×</button>
            </article>
            <div v-if="!profiles.length" class="flamegraph-empty"><span>▥</span><b>还没有火焰图</b><small>选择集群机器并发起第一次采集。</small></div>
          </aside>
          <main class="flamegraph-viewer">
            <template v-if="selected">
              <header class="flamegraph-viewer-head">
                <div><p>{{ targetLabel(selected) }} · {{ selected.duration_seconds }} 秒</p><h4>{{ selected.machine_name || selected.machine_ip || selected.machine_id }}</h4><span>{{ backendLabel(selected.backend || selected.requested_backend) }} · {{ Number(selected.sample_count || 0).toLocaleString('zh-CN') }} 样本 · {{ selected.stack_count || 0 }} 条唯一栈</span></div>
                <div><button v-if="zoomPath" type="button" class="secondary" @click="zoomPath=''">重置缩放</button><button type="button" class="secondary" :disabled="!selected.folded_stacks" @click="exportFolded">导出 folded</button></div>
              </header>
              <div v-if="selected.status==='success' && selected.folded_stacks" class="flamegraph-canvas-wrap">
                <div class="flamegraph-search"><label>查找函数<input v-model.trim="search" type="search" placeholder="输入函数名"></label><span>单击色块可下钻，宽度代表样本占比。</span></div>
                <div class="flamegraph-zoom-path" v-if="zoomPath">当前根节点：{{ zoomPath.split(';').slice(-1)[0] }}</div>
                <div class="flamegraph-scroll">
                  <svg :viewBox="'0 0 '+chart.width+' '+chart.height" :style="{minWidth:'900px',height:Math.max(120,chart.height)+'px'}" role="img" aria-label="Linux CPU 火焰图">
                    <g v-for="frame in chart.frames" :key="frame.path">
                      <rect :x="frame.x" :y="chart.height-frame.y-frame.height" :width="Math.max(0,frame.width-0.7)" :height="frame.height" :fill="frame.color" :class="{matched:frame.matched}" @click="zoomPath=frame.path"><title>{{ frame.name }} · {{ frame.value }} 样本 · {{ frame.percent.toFixed(2) }}%</title></rect>
                      <text v-if="frame.width>34" :x="frame.x+5" :y="chart.height-frame.y-9" @click="zoomPath=frame.path">{{ frame.name }}</text>
                    </g>
                  </svg>
                </div>
              </div>
              <div v-else class="flamegraph-waiting" :class="selected.status">
                <span>{{ selected.status==='failed' ? '!' : '◌' }}</span>
                <b>{{ selected.status==='failed' ? '采集失败' : 'Agent 正在生成火焰图' }}</b>
                <p>{{ selected.error || '完成后页面会自动加载并展示持久化的调用栈。' }}</p>
              </div>
            </template>
            <div v-else class="flamegraph-empty large"><span>▥</span><b>选择一条采集记录</b><small>可查看、缩放、搜索并导出原始 folded stacks。</small></div>
          </main>
        </div>
      </template>

      <section v-else class="flamegraph-schedule-list">
        <article v-for="item in schedules" :key="item.id">
          <header><div><b>{{ item.name }}</b><span :class="{disabled:!item.enabled}">{{ item.enabled ? '已启用' : '已停用' }}</span></div><em>{{ scheduleLabel(item) }}</em></header>
          <dl><div><dt>目标机器</dt><dd>{{ item.machine_id }}</dd></div><div><dt>采集对象</dt><dd>{{ targetLabel(item) }}</dd></div><div><dt>采样窗口</dt><dd>{{ item.duration_seconds }} 秒 · {{ item.frequency_hz }} Hz</dd></div><div><dt>下次执行</dt><dd>{{ date(item.next_run_at) }}</dd></div></dl>
          <footer><small>最近执行 {{ date(item.last_run_at) }}</small><div><button type="button" @click="runSchedule(item)">立即执行</button><button type="button" class="danger-link" @click="removeSchedule(item)">删除</button></div></footer>
        </article>
        <div v-if="!schedules.length" class="flamegraph-empty large"><span>◷</span><b>还没有自动任务</b><small>可以按指定时间、固定间隔或每天自动生成火焰图。</small></div>
      </section>

      <div v-if="captureOpen" class="modal-mask flamegraph-modal-mask" @click.self="captureOpen=false">
        <form class="modal flamegraph-modal" @submit.prevent="submitCapture">
          <div class="modal-head"><div><p>ON-DEMAND PROFILING</p><h2>立即生成火焰图</h2></div><button type="button" @click="captureOpen=false">×</button></div>
          <p class="form-note">自动模式优先使用 perf；PID/进程采集在 perf 不可用时会回退到 Linux /proc，无需联网安装。</p>
          <div class="flamegraph-form-grid">
            <label>目标机器<select v-model="captureForm.machine_id" required><option value="">请选择</option><option v-for="machine in eligibleMachines" :key="machineField(machine,'ID','id')" :value="machineField(machine,'ID','id')">{{ machineField(machine,'Name','name') }} · {{ machineField(machine,'IP','ip') }}</option></select></label>
            <label>采集对象<select v-model="captureForm.target_type"><option value="system">全系统</option><option value="pid">指定 PID</option><option value="process">进程名称</option></select></label>
            <label v-if="captureForm.target_type!=='system'">{{ captureForm.target_type==='pid' ? 'PID' : '进程名称' }}<input v-model.trim="captureForm.target" required :placeholder="captureForm.target_type==='pid'?'例如 1234':'例如 mysqld'"></label>
            <label>采集后端<select v-model="captureForm.backend"><option value="auto">自动选择（推荐）</option><option value="perf">仅 perf</option><option v-if="captureForm.target_type!=='system'" value="procfs">仅 /proc 兼容模式</option></select></label>
            <label>采集时长（秒）<input v-model.number="captureForm.duration_seconds" type="number" min="1" max="600" required></label>
            <label>采样频率（Hz）<input v-model.number="captureForm.frequency_hz" type="number" min="1" max="999" required></label>
          </div>
          <div class="modal-actions"><button type="button" class="secondary" @click="captureOpen=false">取消</button><button class="primary" :disabled="loading">{{ loading ? '正在创建…' : '开始采集' }}</button></div>
        </form>
      </div>

      <div v-if="scheduleOpen" class="modal-mask flamegraph-modal-mask" @click.self="scheduleOpen=false">
        <form class="modal flamegraph-modal wide" @submit.prevent="submitSchedule">
          <div class="modal-head"><div><p>AUTOMATED PROFILING</p><h2>新建火焰图自动任务</h2></div><button type="button" @click="scheduleOpen=false">×</button></div>
          <div class="flamegraph-form-grid">
            <label>任务名称<input v-model.trim="scheduleForm.name" required placeholder="例如 每日 mysqld CPU 画像"></label>
            <label>目标机器<select v-model="scheduleForm.machine_id" required><option value="">请选择</option><option v-for="machine in eligibleMachines" :key="machineField(machine,'ID','id')" :value="machineField(machine,'ID','id')">{{ machineField(machine,'Name','name') }} · {{ machineField(machine,'IP','ip') }}</option></select></label>
            <label>采集对象<select v-model="scheduleForm.target_type"><option value="system">全系统</option><option value="pid">指定 PID</option><option value="process">进程名称</option></select></label>
            <label v-if="scheduleForm.target_type!=='system'">{{ scheduleForm.target_type==='pid' ? 'PID' : '进程名称' }}<input v-model.trim="scheduleForm.target" required></label>
            <label>计划类型<select v-model="scheduleForm.schedule_type"><option value="once">单次</option><option value="interval">固定间隔</option><option value="daily">每日</option></select></label>
            <label>首次执行时间<input v-model="scheduleForm.start_at" type="datetime-local" required></label>
            <label v-if="scheduleForm.schedule_type==='interval'">间隔（分钟）<input v-model.number="scheduleForm.interval_minutes" type="number" min="1" required></label>
            <label>采集后端<select v-model="scheduleForm.backend"><option value="auto">自动选择（推荐）</option><option value="perf">仅 perf</option><option v-if="scheduleForm.target_type!=='system'" value="procfs">仅 /proc 兼容模式</option></select></label>
            <label>采集时长（秒）<input v-model.number="scheduleForm.duration_seconds" type="number" min="1" max="600" required></label>
            <label>采样频率（Hz）<input v-model.number="scheduleForm.frequency_hz" type="number" min="1" max="999" required></label>
          </div>
          <div class="modal-actions"><button type="button" class="secondary" @click="scheduleOpen=false">取消</button><button class="primary" :disabled="loading">{{ loading ? '正在保存…' : '保存自动任务' }}</button></div>
        </form>
      </div>
    </section>
  `
}
