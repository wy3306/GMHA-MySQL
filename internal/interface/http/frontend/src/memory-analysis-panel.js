import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import './memory-analysis-panel.css'

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

const value = (item, upper, lower) => item?.[upper] ?? item?.[lower] ?? ''
const localDateTime = date => {
  const parsed = date instanceof Date ? date : new Date(date)
  return new Date(parsed.getTime() - parsed.getTimezoneOffset() * 60000).toISOString().slice(0, 16)
}
const bytes = raw => {
  const number = Number(raw)
  if (!Number.isFinite(number)) return '—'
  if (Math.abs(number) < 1024) return `${number.toFixed(0)} B`
  const units = ['KB', 'MB', 'GB', 'TB', 'PB']
  let sized = number / 1024
  let index = 0
  while (Math.abs(sized) >= 1024 && index < units.length - 1) {
    sized /= 1024
    index += 1
  }
  return `${sized >= 100 ? sized.toFixed(0) : sized >= 10 ? sized.toFixed(1) : sized.toFixed(2)} ${units[index]}`
}
const numberValue = sample => Number(sample?.numeric_value ?? sample?.value ?? 0)
const moduleRowKey = item => `${item?.machineID || ''}\u0000${item?.instance || ''}\u0000${item?.eventName || ''}`

function buildModuleTrendChart(payload) {
  const rows = payload?.series || []
  if (!rows.length) return { points: [], polyline: '', labels: [], ticks: [], min: 0, max: 0 }
  const width = 760
  const left = 72
  const right = 24
  const top = 28
  const bottom = 220
  const values = rows.map(row => Number(row.value || 0))
  const rawMin = Math.min(...values)
  const rawMax = Math.max(...values)
  const rawSpan = rawMax - rawMin
  const padding = Math.max(rawSpan * 0.18, rawMax * 0.005, 1)
  const min = Math.max(0, rawMin - padding)
  const max = Math.max(min + 1, rawMax + padding)
  const start = Math.min(...rows.map(row => new Date(row.timestamp).getTime()))
  const end = Math.max(...rows.map(row => new Date(row.timestamp).getTime()))
  const points = rows.map(row => {
    const value = Number(row.value || 0)
    return {
      x: left + (new Date(row.timestamp).getTime() - start) / Math.max(1, end - start) * (width - left - right),
      y: bottom - (value - min) / (max - min) * (bottom - top),
      value,
      timestamp: row.timestamp
    }
  })
  const labelTime = at => {
    const date = new Date(at)
    return end - start > 24 * 60 * 60 * 1000
      ? date.toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false })
      : date.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit', hour12: false })
  }
  const labels = [start, start + (end - start) / 2, end].map(at => ({
    x: left + (at - start) / Math.max(1, end - start) * (width - left - right),
    text: labelTime(at)
  }))
  const ticks = [max, min + (max - min) / 2, min].map((value, index) => ({
    y: top + index * (bottom - top) / 2,
    value
  }))
  return { points, polyline: points.map(point => `${point.x.toFixed(1)},${point.y.toFixed(1)}`).join(' '), labels, ticks, min, max }
}

const machineMetrics = [
  'host_memory_total_bytes', 'host_memory_used_bytes', 'host_memory_available_bytes',
  'host_memory_free_bytes', 'host_memory_buffers_bytes', 'host_memory_cached_bytes',
  'host_memory_anon_bytes', 'host_memory_slab_bytes', 'host_memory_page_tables_bytes',
  'host_memory_kernel_stack_bytes', 'host_memory_swap_used_bytes', 'host_mysql_process_rss_bytes'
]
const mysqlMetrics = [
  'mysql_memory_tracked_bytes', 'mysql_memory_high_water_bytes', 'mysql_memory_module_count',
  'mysql_memory_module_bytes', 'mysql_memory_module_high_bytes', 'mysql_memory_module_allocations'
]

export default {
  name: 'MemoryAnalysisPanel',
  props: {
    cluster: { type: Object, required: true },
    machines: { type: Array, default: () => [] },
    instances: { type: Array, default: () => [] }
  },
  setup(props) {
    const loading = ref(false)
    const saving = ref(false)
    const error = ref('')
    const notice = ref('')
    const range = ref('60')
    const customStart = ref(localDateTime(new Date(Date.now() - 60 * 60000)))
    const customEnd = ref(localDateTime(new Date()))
    const machineID = ref('')
    const instance = ref('')
    const group = ref('all')
    const search = ref('')
    const payloads = ref({})
    const selectedModule = ref(null)
    const moduleTrend = ref(null)
    const moduleLoading = ref(false)
    const moduleError = ref('')
    const hostConfig = ref(null)
    const mysqlConfig = ref(null)
    const configOpen = ref(false)
    const settings = ref({ hostEnabled: true, mysqlEnabled: true, hostInterval: 15, mysqlInterval: 60 })
    const updatedAt = ref('')
    let refreshTimer = null
    let moduleRequestToken = 0

    const clusterName = computed(() => value(props.cluster, 'Name', 'name'))
    const clusterMachines = computed(() => props.machines.filter(item => {
      const itemCluster = value(item, 'Cluster', 'cluster')
      return !itemCluster || itemCluster === clusterName.value
    }))
    const clusterInstances = computed(() => {
      const machineIDs = new Set(clusterMachines.value.map(item => value(item, 'ID', 'id')))
      return props.instances.filter(item => {
        const itemCluster = value(item, 'Cluster', 'cluster')
        return itemCluster ? itemCluster === clusterName.value : machineIDs.has(value(item, 'MachineID', 'machine_id'))
      })
    })
    const instanceOptions = computed(() => {
      const seen = new Set()
      const out = []
      for (const item of clusterInstances.value) {
        const port = Number(value(item, 'Port', 'port') || 3306)
        const metricValue = `127.0.0.1:${port}`
        const label = `${value(item, 'MachineName', 'machine_name') || value(item, 'MachineIP', 'machine_ip') || 'MySQL'} · ${value(item, 'MachineIP', 'machine_ip') || '127.0.0.1'}:${port}`
        const key = `${value(item, 'MachineID', 'machine_id')}:${metricValue}`
        if (!seen.has(key)) {
          seen.add(key)
          out.push({ value: metricValue, machineID: value(item, 'MachineID', 'machine_id'), label })
        }
      }
      for (const sample of payloads.value.mysql_memory_tracked_bytes?.latest_values || []) {
        const key = `${sample.machine_id}:${sample.instance}`
        if (sample.instance && !seen.has(key)) {
          seen.add(key)
          out.push({ value: sample.instance, machineID: sample.machine_id, label: `${machineName(sample.machine_id)} · ${sample.instance}` })
        }
      }
      return out.filter(item => !machineID.value || !item.machineID || item.machineID === machineID.value)
    })

    const latest = metric => Number(payloads.value[metric]?.statistics?.current ?? 0)
    const total = computed(() => latest('host_memory_total_bytes'))
    const used = computed(() => latest('host_memory_used_bytes'))
    const available = computed(() => latest('host_memory_available_bytes'))
    const usedPercent = computed(() => total.value > 0 ? used.value / total.value * 100 : 0)
    const mysqlTracked = computed(() => latest('mysql_memory_tracked_bytes'))
    const mysqlHigh = computed(() => latest('mysql_memory_high_water_bytes'))
    const mysqlRSS = computed(() => latest('host_mysql_process_rss_bytes'))
    const collectionEnabled = computed(() => settings.value.hostEnabled || settings.value.mysqlEnabled)

    const allModuleRows = computed(() => {
      const current = payloads.value.mysql_memory_module_bytes?.latest_values || []
      const highs = new Map((payloads.value.mysql_memory_module_high_bytes?.latest_values || []).map(item => [sampleKey(item), numberValue(item)]))
      const allocations = new Map((payloads.value.mysql_memory_module_allocations?.latest_values || []).map(item => [sampleKey(item), numberValue(item)]))
      return current.map(item => ({
        machineID: item.machine_id,
        instance: item.instance,
        eventName: item.labels?.event_name || '',
        module: item.labels?.module || item.labels?.event_name || '未命名模块',
        group: item.labels?.group || 'other',
        current: numberValue(item),
        high: highs.get(sampleKey(item)) || numberValue(item),
        allocations: allocations.get(sampleKey(item)) || 0,
        collectedAt: item.collected_at
      })).sort((a, b) => b.current - a.current)
    })
    const moduleRows = computed(() => allModuleRows.value.filter(item => {
        const keyword = search.value.trim().toLowerCase()
        return (group.value === 'all' || item.group === group.value) &&
          (!keyword || `${item.module} ${item.eventName} ${item.instance} ${machineName(item.machineID)}`.toLowerCase().includes(keyword))
      }))
    const groups = computed(() => [...new Set((payloads.value.mysql_memory_module_bytes?.latest_values || []).map(item => item.labels?.group || 'other'))].sort())
    const topModule = computed(() => allModuleRows.value[0])
    const moduleTrendChart = computed(() => buildModuleTrendChart(moduleTrend.value))
    const moduleStatistics = computed(() => moduleTrend.value?.statistics || {})
    const moduleChange = computed(() => {
      const points = moduleTrendChart.value.points
      if (points.length < 2) return { bytes: 0, percent: 0 }
      const first = points[0].value
      const last = points[points.length - 1].value
      return { bytes: last - first, percent: first ? (last - first) / first * 100 : 0 }
    })

    const composition = computed(() => {
      const items = [
        ['匿名页', latest('host_memory_anon_bytes'), '#2563eb'],
        ['文件缓存', latest('host_memory_cached_bytes'), '#06b6d4'],
        ['Slab', latest('host_memory_slab_bytes'), '#8b5cf6'],
        ['Buffers', latest('host_memory_buffers_bytes'), '#f59e0b'],
        ['页表与内核栈', latest('host_memory_page_tables_bytes') + latest('host_memory_kernel_stack_bytes'), '#ec4899']
      ]
      const known = items.reduce((sum, item) => sum + item[1], 0)
      items.push(['其他已用', Math.max(0, used.value - known), '#94a3b8'])
      return items.map(([name, amount, color]) => ({ name, amount, color, percent: used.value > 0 ? amount / used.value * 100 : 0 }))
    })

    const insights = computed(() => {
      const out = []
      const availableRatio = total.value > 0 ? available.value / total.value : 1
      if (!total.value && !mysqlTracked.value) {
        out.push({ level: 'info', title: '等待首次内存采样', detail: '确认 Agent 在线并开启内存分析；机器明细默认每 15 秒、数据库模块默认每 60 秒采集一次。' })
        return out
      }
      if (availableRatio < 0.1) out.push({ level: 'critical', title: '机器可用内存低于 10%', detail: '存在 OOM 风险。优先确认 MySQL RSS、匿名页和 Slab 的增长来源，并检查是否允许发生 Swap。' })
      else if (availableRatio < 0.2) out.push({ level: 'warning', title: '机器内存余量偏低', detail: '可用内存低于 20%，建议对照趋势确认是稳定水位还是持续增长。' })
      if (latest('host_memory_swap_used_bytes') > 0) out.push({ level: 'warning', title: '主机正在使用 Swap', detail: `当前 Swap 已用 ${bytes(latest('host_memory_swap_used_bytes'))}，数据库延迟抖动时应同步检查换页活动。` })
      if (mysqlRSS.value > 0 && mysqlTracked.value > 0 && mysqlRSS.value > mysqlTracked.value * 1.5) {
        out.push({ level: 'info', title: 'MySQL RSS 明显高于模块跟踪值', detail: '差值通常来自未启用的内存 instrument、分配器碎片、线程栈或 mmap；不要把 performance_schema 合计当作进程全部内存。' })
      }
      if (mysqlHigh.value > mysqlTracked.value * 1.5 && mysqlTracked.value > 0) out.push({ level: 'info', title: '数据库峰值已明显回落', detail: `当前跟踪 ${bytes(mysqlTracked.value)}，历史高水位 ${bytes(mysqlHigh.value)}。可结合时间范围定位峰值对应的业务窗口。` })
      if (topModule.value && mysqlTracked.value > 0 && topModule.value.current / mysqlTracked.value > 0.5) {
        out.push({ level: 'warning', title: `${topModule.value.group} 模块占据主要内存`, detail: `${topModule.value.eventName} 当前占数据库跟踪内存的 ${(topModule.value.current / mysqlTracked.value * 100).toFixed(1)}%。` })
      }
      if (!out.length) out.push({ level: 'healthy', title: '当前未发现明显内存风险', detail: '可用内存、Swap 与数据库模块分布均未触发建议阈值；继续观察高水位和长期趋势。' })
      return out
    })

    const chart = computed(() => {
      const series = [
        { name: '机器已用', color: '#2563eb', rows: payloads.value.host_memory_used_bytes?.series || [] },
        { name: '机器可用', color: '#06b6d4', rows: payloads.value.host_memory_available_bytes?.series || [] },
        { name: '数据库跟踪', color: '#8b5cf6', rows: payloads.value.mysql_memory_tracked_bytes?.series || [] }
      ].filter(item => item.rows.length)
      const all = series.flatMap(item => item.rows)
      if (!all.length) return { series: [], labels: [], max: 0 }
      const start = Math.min(...all.map(item => new Date(item.timestamp).getTime()))
      const end = Math.max(...all.map(item => new Date(item.timestamp).getTime()))
      const max = Math.max(1, ...all.map(item => Number(item.max ?? item.value ?? 0)))
      const points = series.map(item => ({
        ...item,
        polyline: item.rows.map(row => {
          const x = 62 + (new Date(row.timestamp).getTime() - start) / Math.max(1, end - start) * 676
          const y = 228 - Number(row.value || 0) / max * 188
          return `${x.toFixed(1)},${y.toFixed(1)}`
        }).join(' ')
      }))
      const labels = [start, start + (end - start) / 2, end].map(at => ({ x: 62 + (at - start) / Math.max(1, end - start) * 676, text: new Date(at).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' }) }))
      return { series: points, labels, max }
    })

    function machineName(id) {
      const item = clusterMachines.value.find(machine => value(machine, 'ID', 'id') === id)
      return item ? value(item, 'Name', 'name') || value(item, 'IP', 'ip') : id || '全部机器'
    }
    function sampleKey(item) {
      return `${item.machine_id}\u0000${item.instance}\u0000${item.labels?.event_name || ''}`
    }
    function windowParams() {
      if (range.value !== 'custom') return { range_minutes: range.value }
      return { start_at: new Date(customStart.value).toISOString(), end_at: new Date(customEnd.value).toISOString() }
    }
    async function loadData(silent = false) {
      if (!clusterName.value || loading.value) return
      if (!silent) loading.value = true
      error.value = ''
      try {
        const metrics = [...machineMetrics, ...mysqlMetrics]
        const result = await Promise.all(metrics.map(async metric => {
          const query = new URLSearchParams({ cluster: clusterName.value, metric, ...windowParams() })
          if (machineID.value) query.set('machine_id', machineID.value)
          if (instance.value && metric.startsWith('mysql_')) query.set('instance', instance.value)
          return [metric, await request(`/performance/metrics?${query}`)]
        }))
        payloads.value = Object.fromEntries(result)
        await ensureModuleSelection()
        updatedAt.value = new Date().toISOString()
      } catch (err) {
        error.value = err.message
      } finally {
        loading.value = false
      }
    }
    async function ensureModuleSelection() {
      const currentKey = moduleRowKey(selectedModule.value)
      const current = allModuleRows.value.find(item => moduleRowKey(item) === currentKey)
      const next = current || allModuleRows.value[0] || null
      if (!next) {
        selectedModule.value = null
        moduleTrend.value = null
        return
      }
      await selectModule(next, true)
    }
    async function selectModule(item, silent = false) {
      if (!item) return
      selectedModule.value = item
      const requestToken = ++moduleRequestToken
      if (!silent) moduleLoading.value = true
      moduleError.value = ''
      try {
        const query = new URLSearchParams({
          cluster: clusterName.value,
          metric: 'mysql_memory_module_bytes',
          event_name: item.eventName,
          ...windowParams()
        })
        if (item.machineID) query.set('machine_id', item.machineID)
        if (item.instance) query.set('instance', item.instance)
        const payload = await request(`/performance/metrics?${query}`)
        if (requestToken === moduleRequestToken) moduleTrend.value = payload
      } catch (err) {
        if (requestToken === moduleRequestToken) {
          moduleTrend.value = null
          moduleError.value = err.message
        }
      } finally {
        if (requestToken === moduleRequestToken) moduleLoading.value = false
      }
    }
    async function loadConfig() {
      try {
        const [host, mysql] = await Promise.all([request('/dynamic-collect/config'), request('/mysql-dynamic-collect/config')])
        hostConfig.value = host
        mysqlConfig.value = mysql
        const hostTask = host.tasks?.find(item => item.name === 'host_memory_detail')
        const mysqlTask = mysql.tasks?.find(item => item.name === 'mysql_memory_modules')
        settings.value = {
          hostEnabled: hostTask?.enabled !== false,
          mysqlEnabled: mysqlTask?.enabled !== false,
          hostInterval: Number(hostTask?.interval_seconds || 15),
          mysqlInterval: Number(mysqlTask?.interval_seconds || 60)
        }
      } catch (err) {
        error.value = err.message
      }
    }
    async function saveConfig() {
      saving.value = true
      error.value = ''
      try {
        const [host, mysql] = await Promise.all([request('/dynamic-collect/config'), request('/mysql-dynamic-collect/config')])
        const update = (config, taskName, enabled, interval, fallback) => {
          const tasks = [...(config.tasks || [])]
          let task = tasks.find(item => item.name === taskName)
          if (!task) {
            task = { ...fallback }
            tasks.push(task)
          }
          task.enabled = enabled
          task.interval_seconds = Number(interval)
          task.timeout_seconds = Math.min(Number(interval), Math.max(1, Number(task.timeout_seconds || 1)))
          return { ...config, tasks }
        }
        const nextHost = update(host, 'host_memory_detail', settings.value.hostEnabled, settings.value.hostInterval, {
          name: 'host_memory_detail', type: 'builtin', category: 'memory', labels: { display_name: '机器内存明细' }
        })
        const nextMySQL = update(mysql, 'mysql_memory_modules', settings.value.mysqlEnabled, settings.value.mysqlInterval, {
          name: 'mysql_memory_modules', type: 'builtin', category: 'memory', labels: { display_name: '数据库内存模块明细' }
        })
        await Promise.all([
          request('/dynamic-collect/config', { method: 'PUT', body: JSON.stringify(nextHost) }),
          request('/mysql-dynamic-collect/config', { method: 'PUT', body: JSON.stringify(nextMySQL) })
        ])
        hostConfig.value = nextHost
        mysqlConfig.value = nextMySQL
        configOpen.value = false
        notice.value = '内存采集配置已保存，并会随下一次心跳自动下发到 Agent。'
        restartTimer()
      } catch (err) {
        error.value = err.message
      } finally {
        saving.value = false
      }
    }
    function closeConfig() {
      configOpen.value = false
      loadConfig()
    }
    function chooseRange(next) {
      range.value = next
      if (next !== 'custom') loadData()
    }
    function applyCustomRange() {
      if (!customStart.value || !customEnd.value || new Date(customStart.value) >= new Date(customEnd.value)) {
        error.value = '请选择有效的开始和结束时间。'
        return
      }
      loadData()
    }
    function restartTimer() {
      if (refreshTimer) clearInterval(refreshTimer)
      refreshTimer = collectionEnabled.value ? setInterval(() => loadData(true), 30000) : null
    }

    watch(machineID, () => {
      if (!instanceOptions.value.some(item => item.value === instance.value)) instance.value = ''
      loadData()
    })
    watch(instance, () => loadData())
    watch(clusterName, () => {
      machineID.value = ''
      instance.value = ''
      loadData()
    })
    onMounted(async () => {
      await loadConfig()
      await loadData()
      restartTimer()
    })
    onUnmounted(() => {
      if (refreshTimer) clearInterval(refreshTimer)
    })

    return {
      loading, saving, error, notice, range, customStart, customEnd, machineID, instance, group, search,
      configOpen, settings, updatedAt, clusterMachines, instanceOptions, allModuleRows, moduleRows, groups, composition,
      selectedModule, moduleTrend, moduleLoading, moduleError, moduleTrendChart, moduleStatistics, moduleChange,
      insights, chart, total, used, available, usedPercent, mysqlTracked, mysqlHigh, mysqlRSS, collectionEnabled,
      bytes, latest, machineName, moduleRowKey, selectModule, loadData, loadConfig, saveConfig, closeConfig, chooseRange, applyCustomRange
    }
  },
  template: `
    <section class="memory-analysis">
      <header class="memory-analysis-head">
        <div>
          <p>采集状态</p>
          <h3>机器与数据库内存</h3>
          <span>数据按设定周期自动采集并持久化，关闭后不影响其他性能指标。</span>
        </div>
        <div class="memory-head-actions">
          <span :class="['memory-collector-state',{off:!collectionEnabled}]"><i></i>{{ collectionEnabled ? '定时采集中' : '内存采集已关闭' }}</span>
          <button class="secondary" type="button" @click="configOpen=true; loadConfig()">采集设置</button>
          <button class="primary" type="button" :disabled="loading" @click="loadData()">{{ loading ? '读取中…' : '刷新分析' }}</button>
        </div>
      </header>

      <div v-if="error" class="memory-message error"><b>内存数据读取失败</b><span>{{ error }}</span><button type="button" @click="error=''">×</button></div>
      <div v-if="notice" class="memory-message success"><b>配置已更新</b><span>{{ notice }}</span><button type="button" @click="notice=''">×</button></div>

      <section class="memory-toolbar">
        <label><span>机器</span><select v-model="machineID"><option value="">全部机器</option><option v-for="item in clusterMachines" :key="item.ID||item.id" :value="item.ID||item.id">{{ item.Name||item.name||item.IP||item.ip }} · {{ item.IP||item.ip }}</option></select></label>
        <label><span>数据库实例</span><select v-model="instance"><option value="">全部实例</option><option v-for="item in instanceOptions" :key="item.machineID+item.value" :value="item.value">{{ item.label }}</option></select></label>
        <div class="memory-range">
          <span>分析时间</span>
          <button v-for="item in [['15','15 分钟'],['60','1 小时'],['360','6 小时'],['1440','24 小时'],['10080','7 天'],['custom','自定义']]" :key="item[0]" type="button" :class="{active:range===item[0]}" @click="chooseRange(item[0])">{{ item[1] }}</button>
        </div>
        <div v-if="range==='custom'" class="memory-custom-range">
          <input v-model="customStart" type="datetime-local" aria-label="开始时间">
          <span>至</span>
          <input v-model="customEnd" type="datetime-local" aria-label="结束时间">
          <button type="button" @click="applyCustomRange">应用</button>
        </div>
        <small>更新于 {{ updatedAt ? new Date(updatedAt).toLocaleString('zh-CN') : '—' }} · 时序保留 7 天</small>
      </section>

      <section class="memory-summary-grid" :aria-busy="loading">
        <article>
          <header><div><span>机器内存</span><strong>{{ bytes(used) }}</strong><em>已使用 · {{ usedPercent.toFixed(1) }}%</em></div><i class="memory-summary-icon host">▣</i></header>
          <i class="memory-summary-progress"><b :style="{width:Math.min(100,usedPercent)+'%'}"></b></i>
          <dl><div><dt>内存总量</dt><dd>{{ bytes(total) }}</dd></div><div><dt>当前可用</dt><dd>{{ bytes(available) }}</dd></div><div><dt>Swap 已用</dt><dd>{{ bytes(latest('host_memory_swap_used_bytes')) }}</dd></div></dl>
        </article>
        <article>
          <header><div><span>数据库内存</span><strong>{{ bytes(mysqlRSS) }}</strong><em>MySQL 进程 RSS · 操作系统视角</em></div><i class="memory-summary-icon mysql">DB</i></header>
          <dl><div><dt>模块跟踪</dt><dd>{{ bytes(mysqlTracked) }}</dd></div><div><dt>历史高水位</dt><dd>{{ bytes(mysqlHigh) }}</dd></div><div><dt>活跃模块</dt><dd>{{ latest('mysql_memory_module_count').toFixed(0) }} 个</dd></div></dl>
        </article>
      </section>

      <section class="memory-main-grid">
        <article class="memory-panel memory-trend-panel">
          <header><div><h4>内存趋势</h4><p>同一时间窗口对照机器压力与数据库内部占用</p></div><div class="memory-chart-legend"><span v-for="item in chart.series" :key="item.name"><i :style="{background:item.color}"></i>{{ item.name }}</span></div></header>
          <div v-if="chart.series.length" class="memory-trend-chart">
            <svg viewBox="0 0 760 260" role="img" aria-label="内存时序趋势">
              <g class="memory-grid"><line v-for="y in [40,87,134,181,228]" :key="y" x1="62" :y1="y" x2="738" :y2="y"/></g>
              <text x="56" y="45">{{ bytes(chart.max) }}</text><text x="56" y="233">0</text>
              <polyline v-for="item in chart.series" :key="item.name" :points="item.polyline" :style="{stroke:item.color}"/>
              <text v-for="item in chart.labels" :key="item.x" :x="item.x" y="252">{{ item.text }}</text>
            </svg>
          </div>
          <div v-else class="memory-empty"><span>⌁</span><b>当前时间段暂无趋势数据</b><small>开启采集后，首批数据会在设定的采样周期内出现。</small></div>
        </article>

        <article class="memory-panel memory-composition-panel">
          <header><div><h4>机器内存构成</h4><p>用于区分业务匿名页、文件缓存与内核占用</p></div></header>
          <div class="memory-composition-bar"><i v-for="item in composition" :key="item.name" :style="{width:item.percent+'%',background:item.color}" :title="item.name+' '+bytes(item.amount)"></i></div>
          <dl><div v-for="item in composition" :key="item.name"><dt><i :style="{background:item.color}"></i>{{ item.name }}</dt><dd>{{ bytes(item.amount) }}<small>{{ item.percent.toFixed(1) }}%</small></dd></div></dl>
        </article>
      </section>

      <section class="memory-panel memory-modules-panel">
        <header>
          <div><h4>数据库内存模块趋势</h4><p>点击模块查看独立折线图，识别持续增长、瞬时峰值与回落；数据来自 performance_schema.memory_summary_global_by_event_name。</p></div>
          <span>{{ allModuleRows.length }} 个模块</span>
        </header>
        <div class="memory-module-toolbar">
          <label><span>⌕</span><input v-model.trim="search" placeholder="搜索模块、事件名或实例"></label>
          <select v-model="group"><option value="all">全部模块组</option><option v-for="item in groups" :key="item" :value="item">{{ item }}</option></select>
        </div>
        <div v-if="moduleRows.length" class="memory-module-workbench">
          <aside class="memory-module-ranking">
            <header><div><span>TOP MODULES</span><b>当前占用排行</b></div><small>点击切换趋势</small></header>
            <button v-for="(item,index) in moduleRows.slice(0,8)" :key="moduleRowKey(item)" type="button" :class="{active:moduleRowKey(selectedModule)===moduleRowKey(item)}" @click="selectModule(item)">
              <em>{{ String(index+1).padStart(2,'0') }}</em>
              <span><b>{{ item.module }}</b><small>{{ machineName(item.machineID) }} · {{ item.group }}</small></span>
              <strong>{{ bytes(item.current) }}</strong>
              <i><b :style="{width:Math.min(100,mysqlTracked ? item.current/mysqlTracked*100 : 0)+'%'}"></b></i>
            </button>
          </aside>

          <article class="memory-module-trend-card" :aria-busy="moduleLoading">
            <header v-if="selectedModule">
              <div><span>{{ selectedModule.group }} · {{ machineName(selectedModule.machineID) }}</span><h5>{{ selectedModule.module }}</h5><p>{{ selectedModule.eventName }}</p></div>
              <span class="memory-module-live"><i></i>{{ moduleLoading ? '读取趋势' : '模块时序' }}</span>
            </header>
            <dl v-if="selectedModule" class="memory-module-stat-grid">
              <div><dt>当前占用</dt><dd>{{ bytes(moduleStatistics.current ?? selectedModule.current) }}</dd></div>
              <div><dt>区间峰值</dt><dd>{{ bytes(moduleStatistics.max ?? selectedModule.high) }}</dd></div>
              <div><dt>区间平均</dt><dd>{{ bytes(moduleStatistics.average ?? selectedModule.current) }}</dd></div>
              <div :class="{up:moduleChange.bytes>0,down:moduleChange.bytes<0}"><dt>窗口变化</dt><dd>{{ moduleChange.bytes>0 ? '+' : '' }}{{ bytes(moduleChange.bytes) }}<small>{{ moduleTrendChart.points.length>1 ? (moduleChange.percent>0?'+':'')+moduleChange.percent.toFixed(2)+'%' : '样本不足' }}</small></dd></div>
            </dl>
            <div v-if="moduleTrendChart.points.length" class="memory-module-line-chart">
              <svg viewBox="0 0 760 250" role="img" :aria-label="selectedModule.module+'内存趋势折线图'">
                <defs><linearGradient id="memoryModuleArea" x1="0" y1="0" x2="0" y2="1"><stop offset="0%" stop-color="#4f46e5" stop-opacity=".2"/><stop offset="100%" stop-color="#4f46e5" stop-opacity="0"/></linearGradient></defs>
                <g class="memory-module-chart-grid"><line v-for="item in moduleTrendChart.ticks" :key="item.y" x1="72" :y1="item.y" x2="736" :y2="item.y"/></g>
                <text v-for="item in moduleTrendChart.ticks" :key="'tick-'+item.y" x="64" :y="item.y+4" class="memory-module-y-label">{{ bytes(item.value) }}</text>
                <polygon :points="'72,220 '+moduleTrendChart.polyline+' 736,220'" fill="url(#memoryModuleArea)"/>
                <polyline :points="moduleTrendChart.polyline"/>
                <circle v-for="point in moduleTrendChart.points" :key="point.timestamp" :cx="point.x" :cy="point.y" r="3"><title>{{ new Date(point.timestamp).toLocaleString('zh-CN') }} · {{ bytes(point.value) }}</title></circle>
                <text v-for="item in moduleTrendChart.labels" :key="'label-'+item.x" :x="item.x" y="244" class="memory-module-x-label">{{ item.text }}</text>
              </svg>
            </div>
            <div v-else class="memory-module-chart-empty"><span>⌁</span><b>{{ moduleLoading ? '正在读取模块趋势…' : '当前窗口暂无模块趋势' }}</b><small>{{ moduleError || '模块至少完成两次采样后即可观察变化。' }}</small></div>
          </article>
        </div>

        <div v-if="moduleRows.length" class="memory-module-detail-head"><div><b>全部模块明细</b><span>点击任意行可在上方查看折线图</span></div><em>显示 {{ moduleRows.length }} 项</em></div>
        <div v-if="moduleRows.length" class="memory-module-table">
          <table><thead><tr><th>数据库 / 模块</th><th>模块组</th><th>当前内存</th><th>历史峰值</th><th>当前分配数</th><th>占跟踪内存</th></tr></thead>
          <tbody><tr v-for="item in moduleRows.slice(0,500)" :key="moduleRowKey(item)" :class="{selected:moduleRowKey(selectedModule)===moduleRowKey(item)}" @click="selectModule(item)">
            <td><button type="button" class="memory-module-name" @click.stop="selectModule(item)"><b>{{ item.module }}</b><small>{{ machineName(item.machineID) }} · {{ item.instance || '实例待识别' }}<br>{{ item.eventName }}</small></button></td>
            <td><span class="memory-group-tag">{{ item.group }}</span></td>
            <td><strong>{{ bytes(item.current) }}</strong><i class="memory-module-bar"><b :style="{width:Math.min(100,mysqlTracked ? item.current/mysqlTracked*100 : 0)+'%'}"></b></i></td>
            <td>{{ bytes(item.high) }}</td><td>{{ item.allocations.toLocaleString('zh-CN') }}</td>
            <td>{{ mysqlTracked ? (item.current/mysqlTracked*100).toFixed(2)+'%' : '—' }}</td>
          </tr></tbody></table>
        </div>
        <div v-else class="memory-empty"><span>◇</span><b>尚无数据库模块数据</b><small>MySQL 需要开启 performance_schema 内存 instrument；不支持时不会影响机器内存采集。</small></div>
      </section>

      <section class="memory-panel memory-insights">
        <header><div><h4>DBA 分析建议</h4><p>根据内存余量、Swap、RSS 差值、模块占比和高水位生成</p></div><span>{{ insights.length }} 条</span></header>
        <div><article v-for="item in insights" :key="item.title" :class="item.level"><i>{{ item.level==='healthy' ? '✓' : item.level==='critical' ? '!' : item.level==='warning' ? '△' : 'i' }}</i><section><b>{{ item.title }}</b><p>{{ item.detail }}</p></section></article></div>
      </section>

      <div v-if="configOpen" class="modal-mask memory-config-mask" @click.self="closeConfig">
        <form class="modal memory-config-modal" @submit.prevent="saveConfig">
          <div class="modal-head"><div><p>COLLECTION SCHEDULE</p><h2>内存采集设置</h2></div><button type="button" @click="closeConfig">×</button></div>
          <p class="form-note">开关只影响内存分析，不会停止 CPU、磁盘等其他性能指标。采样任务由 Agent 定时执行，配置会持久化并自动下发。</p>
          <section>
            <label class="memory-config-toggle"><input v-model="settings.hostEnabled" type="checkbox"><span><b>机器内存分析</b><small>Linux 内存构成、Swap 与 MySQL 进程 RSS</small></span><em>{{ settings.hostEnabled ? '已开启' : '已关闭' }}</em></label>
            <label>机器采样周期<select v-model.number="settings.hostInterval" :disabled="!settings.hostEnabled"><option :value="5">5 秒</option><option :value="15">15 秒</option><option :value="30">30 秒</option><option :value="60">1 分钟</option><option :value="300">5 分钟</option></select></label>
          </section>
          <section>
            <label class="memory-config-toggle"><input v-model="settings.mysqlEnabled" type="checkbox"><span><b>数据库模块分析</b><small>逐项采集 performance_schema 内存模块</small></span><em>{{ settings.mysqlEnabled ? '已开启' : '已关闭' }}</em></label>
            <label>数据库采样周期<select v-model.number="settings.mysqlInterval" :disabled="!settings.mysqlEnabled"><option :value="15">15 秒</option><option :value="30">30 秒</option><option :value="60">1 分钟</option><option :value="300">5 分钟</option><option :value="900">15 分钟</option></select></label>
          </section>
          <div class="modal-actions"><button class="secondary" type="button" @click="closeConfig">取消</button><button class="primary" :disabled="saving">{{ saving ? '保存中…' : '保存并下发' }}</button></div>
        </form>
      </div>
    </section>
  `
}
