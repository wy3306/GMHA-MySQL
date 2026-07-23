import { computed, ref, watch } from 'vue/dist/vue.esm-bundler.js'

const request = async (path, options = {}) => {
  const response = await fetch(`/api/v1${path}`, { headers: { 'Content-Type': 'application/json' }, ...options })
  const payload = await response.json().catch(() => ({}))
  if (!response.ok) throw new Error(payload.error || '请求失败')
  return payload
}

const field = (item, upper, lower) => item?.[upper] ?? item?.[lower]
export const supportsHistograms = version => {
  const match = String(version || '').trim().match(/^(\d+)\.(\d+)/)
  return !match || [8, 9].includes(Number(match[1]))
}

export const histogramBars = histogram => {
  const buckets = histogram?.raw?.buckets || histogram?.Raw?.buckets || []
  if (!Array.isArray(buckets) || !buckets.length) return []
  const frequencies = []
  let previous = 0
  for (const bucket of buckets) {
    if (!Array.isArray(bucket) || bucket.length < 2) continue
    const cumulative = Number(bucket[bucket.length - 1])
    if (!Number.isFinite(cumulative)) continue
    frequencies.push(Math.max(0, cumulative - previous))
    previous = cumulative
  }
  if (!frequencies.length) return []
  const sampleSize = 32
  const sampled = frequencies.length <= sampleSize
    ? frequencies
    : Array.from({ length: sampleSize }, (_, index) => {
        const start = Math.floor(index * frequencies.length / sampleSize)
        const end = Math.max(start + 1, Math.floor((index + 1) * frequencies.length / sampleSize))
        return frequencies.slice(start, end).reduce((sum, value) => sum + value, 0)
      })
  const maximum = Math.max(...sampled, 0.000001)
  return sampled.map(value => Math.max(5, Math.round(value / maximum * 100)))
}

export default {
  name: 'HistogramManagement',
  props: { instances: { type: Array, default: () => [] } },
  setup(props) {
    const target = ref(''), schema = ref(''), table = ref(''), selectedColumns = ref([])
    const buckets = ref(100), catalog = ref({ schemas: [], tables: [], columns: [], histograms: [] })
    const loading = ref(false), error = ref(''), notice = ref(''), loaded = ref(false)

    const instanceOptions = computed(() => (props.instances || []).map(item => {
      const machineID = field(item, 'MachineID', 'machine_id')
      const port = Number(field(item, 'Port', 'port'))
      const version = field(item, 'Version', 'version') || ''
      return {
        value: `${machineID}|${port}`, item, version,
        supported: supportsHistograms(version),
        label: `${field(item, 'MachineName', 'machine_name') || field(item, 'MachineIP', 'machine_ip')} · ${field(item, 'MachineIP', 'machine_ip')}:${port} · MySQL ${version || '待检测'}`
      }
    }))
    const selectedInstance = computed(() => instanceOptions.value.find(option => option.value === target.value)?.item || null)
    const compatibleCount = computed(() => instanceOptions.value.filter(option => option.supported).length)
    const existingForTable = computed(() => (catalog.value.histograms || []).filter(item => !schema.value || (item.schema === schema.value && (!table.value || item.table === table.value))))
    const eligibleColumns = computed(() => (catalog.value.columns || []).filter(item => item.eligible))
    const selectedExistingColumns = computed(() => selectedColumns.value.filter(column => (catalog.value.columns || []).find(item => item.name === column)?.has_histogram))
    const query = () => {
      if (!selectedInstance.value) throw new Error('请选择 MySQL 8.0 及以上实例')
      const params = new URLSearchParams({
        machine_id: String(field(selectedInstance.value, 'MachineID', 'machine_id')),
        port: String(field(selectedInstance.value, 'Port', 'port'))
      })
      if (schema.value) params.set('schema', schema.value)
      if (table.value) params.set('table', table.value)
      return params
    }
    const load = async ({ announce = false, force = false } = {}) => {
      if (!target.value || (loading.value && !force)) return
      loading.value = true
      error.value = ''
      try {
        catalog.value = await request(`/mysql/histograms?${query()}`)
        loaded.value = true
        selectedColumns.value = selectedColumns.value.filter(name => (catalog.value.columns || []).some(column => column.name === name))
        if (announce) notice.value = `已从 MySQL ${catalog.value.server_version} 读取 ${catalog.value.histograms?.length || 0} 个直方图`
      } catch (err) {
        error.value = err.message
        loaded.value = false
      } finally { loading.value = false }
    }
    const targetChanged = async () => {
      schema.value = ''
      table.value = ''
      selectedColumns.value = []
      catalog.value = { schemas: [], tables: [], columns: [], histograms: [] }
      await load()
    }
    const schemaChanged = async () => {
      table.value = ''
      selectedColumns.value = []
      await load()
    }
    const tableChanged = async () => {
      selectedColumns.value = []
      await load()
    }
    const payload = columns => ({
      machine_id: field(selectedInstance.value, 'MachineID', 'machine_id'),
      port: Number(field(selectedInstance.value, 'Port', 'port')),
      schema: schema.value, table: table.value, columns
    })
    const update = async () => {
      if (!schema.value || !table.value) { error.value = '请先选择数据库和数据表'; return }
      if (!selectedColumns.value.length) { error.value = '请至少选择一个可管理列'; return }
      const bucketCount = Number(buckets.value)
      if (!Number.isInteger(bucketCount) || bucketCount < 1 || bucketCount > 1024) { error.value = '桶数必须是 1–1024 的整数'; return }
      if (!confirm(`将在 ${schema.value}.${table.value} 上更新 ${selectedColumns.value.length} 个列直方图。\n\nANALYZE TABLE 会读取样本并短暂持有表读锁，建议在业务低峰执行。是否继续？`)) return
      loading.value = true
      error.value = ''
      try {
        const result = await request('/mysql/histograms', {
          method: 'POST',
          body: JSON.stringify({ ...payload(selectedColumns.value), buckets: bucketCount })
        })
        notice.value = `已更新 ${result.columns?.length || selectedColumns.value.length} 个列直方图，桶数 ${bucketCount}`
        await load({ force: true })
      } catch (err) { error.value = err.message }
      finally { loading.value = false }
    }
    const drop = async item => {
      const name = `${item.schema}.${item.table}.${item.column}`
      if (!confirm(`确认删除直方图 ${name}？\n该操作只删除优化器列统计，不会删除表数据或索引。`)) return
      loading.value = true
      error.value = ''
      try {
        await request('/mysql/histograms', {
          method: 'DELETE',
          body: JSON.stringify(payload([item.column]))
        })
        notice.value = `已删除直方图 ${name}`
        selectedColumns.value = selectedColumns.value.filter(column => column !== item.column)
        await load({ force: true })
      } catch (err) { error.value = err.message }
      finally { loading.value = false }
    }
    const dropSelected = async () => {
      const columns = selectedExistingColumns.value
      if (!columns.length) { error.value = '当前选择中没有已存在的直方图'; return }
      if (!confirm(`确认删除 ${schema.value}.${table.value} 的 ${columns.length} 个列直方图？`)) return
      loading.value = true
      error.value = ''
      try {
        await request('/mysql/histograms', { method: 'DELETE', body: JSON.stringify(payload(columns)) })
        notice.value = `已删除 ${columns.length} 个列直方图`
        selectedColumns.value = []
        await load({ force: true })
      } catch (err) { error.value = err.message }
      finally { loading.value = false }
    }
    watch(instanceOptions, options => {
      if (!options.some(option => option.value === target.value && option.supported)) {
        target.value = options.find(option => option.supported)?.value || ''
        if (target.value) targetChanged()
      }
    }, { immediate: true })

    return {
      target, schema, table, selectedColumns, buckets, catalog, loading, error, notice, loaded,
      instanceOptions, selectedInstance, compatibleCount, existingForTable, eligibleColumns, selectedExistingColumns,
      load, targetChanged, schemaChanged, tableChanged, update, drop, dropSelected, histogramBars, field
    }
  },
  template: `
    <main class="histogram-management-page">
      <section class="histogram-hero">
        <div><p>OPTIMIZER STATISTICS</p><h3>直方图管理</h3><span>管理 MySQL 优化器的列值分布统计，改善无索引列、数据倾斜场景下的行数估算。</span></div>
        <div class="histogram-version-gate"><b>MySQL 8.0+</b><span>不兼容 MySQL 5.7</span></div>
      </section>

      <div v-if="error" class="histogram-message error"><b>操作未完成</b><span>{{ error }}</span><button @click="error=''">×</button></div>
      <div v-if="notice" class="histogram-message success"><b>操作完成</b><span>{{ notice }}</span><button @click="notice=''">×</button></div>
      <div v-if="!compatibleCount" class="histogram-compatibility-empty"><i>8.0+</i><div><b>当前集群没有可管理的 MySQL 8.0+ 实例</b><span>MySQL 5.7 不提供 INFORMATION_SCHEMA.COLUMN_STATISTICS，也不支持 ANALYZE TABLE 的 HISTOGRAM 语法。请先完成版本升级。</span></div></div>

      <section class="histogram-workspace">
        <header class="histogram-target-bar">
          <label><span>目标实例</span><select v-model="target" @change="targetChanged"><option value="">请选择实例</option><option v-for="option in instanceOptions" :key="option.value" :value="option.value" :disabled="!option.supported">{{ option.label }}{{ option.supported ? '' : ' · 不支持直方图' }}</option></select></label>
          <label><span>数据库</span><select v-model="schema" :disabled="loading || !target" @change="schemaChanged"><option value="">全部数据库</option><option v-for="name in catalog.schemas || []" :key="name">{{ name }}</option></select></label>
          <label><span>数据表</span><select v-model="table" :disabled="loading || !schema" @change="tableChanged"><option value="">请选择数据表</option><option v-for="item in catalog.tables || []" :key="item.name" :value="item.name">{{ item.name }} · 约 {{ Number(item.estimated_rows || 0).toLocaleString() }} 行</option></select></label>
          <button class="secondary" :disabled="loading || !target" @click="load({announce:true})">{{ loading ? '读取中…' : '刷新' }}</button>
        </header>
        <div class="histogram-safety-note"><i>i</i><span>直方图按实例保存，本页面使用 <code>NO_WRITE_TO_BINLOG</code>，不会复制到其他节点。更新操作需要目标表的 SELECT、INSERT 权限，并可能短暂持有读锁。</span></div>

        <div v-if="table" class="histogram-editor">
          <section class="histogram-column-picker">
            <header><div><b>选择统计列</b><small>{{ schema }}.{{ table }} · {{ eligibleColumns.length }} 个可管理列</small></div><span>已选 {{ selectedColumns.length }}</span></header>
            <div class="histogram-columns">
              <label v-for="column in catalog.columns || []" :key="column.name" :class="{selected:selectedColumns.includes(column.name),disabled:!column.eligible}">
                <input v-model="selectedColumns" type="checkbox" :value="column.name" :disabled="!column.eligible">
                <span><b>{{ column.name }}</b><small>{{ column.column_type }} · {{ column.indexed ? '已有索引' : '无索引' }}</small><em v-if="column.has_histogram">已有直方图</em><em v-else-if="!column.eligible">{{ column.ineligible_reason }}</em></span>
              </label>
            </div>
          </section>
          <aside class="histogram-action-card">
            <header><b>生成设置</b><span>ANALYZE TABLE</span></header>
            <label>桶数量<input v-model.number="buckets" type="number" min="1" max="1024"><small>范围 1–1024，默认 100；分布越复杂可适当增加。</small></label>
            <div><b>{{ selectedColumns.length }}</b><span>个待更新列</span></div>
            <button class="primary" :disabled="loading || !selectedColumns.length" @click="update">{{ loading ? '执行中…' : '更新直方图' }}</button>
            <button class="danger-link" :disabled="loading || !selectedExistingColumns.length" @click="dropSelected">删除所选已有直方图</button>
          </aside>
        </div>

        <section class="histogram-list">
          <header><div><b>现有直方图</b><small>来自 INFORMATION_SCHEMA.COLUMN_STATISTICS</small></div><span>{{ existingForTable.length }} 项</span></header>
          <div class="histogram-card-grid">
            <article v-for="item in existingForTable" :key="item.schema+'.'+item.table+'.'+item.column">
              <header><div><b>{{ item.column }}</b><small>{{ item.schema }}.{{ item.table }}</small></div><span>{{ item.histogram_type || 'histogram' }}</span></header>
              <div class="histogram-mini-chart" :title="'实际桶数 '+item.bucket_count"><i v-for="(height,index) in histogramBars(item)" :key="index" :style="{height:height+'%'}"></i><em v-if="!histogramBars(item).length">暂无可视化桶数据</em></div>
              <dl><div><dt>实际桶数</dt><dd>{{ item.bucket_count }}</dd></div><div><dt>设定桶数</dt><dd>{{ item.specified_buckets || '—' }}</dd></div><div><dt>采样率</dt><dd>{{ (Number(item.sampling_rate || 0)*100).toFixed(1) }}%</dd></div><div><dt>NULL 比例</dt><dd>{{ (Number(item.null_values || 0)*100).toFixed(1) }}%</dd></div></dl>
              <footer><small>更新于 {{ item.last_updated || '—' }}</small><button class="danger-link" :disabled="loading || !table || item.table!==table" :title="!table ? '选择对应数据表后才能删除' : ''" @click="drop(item)">删除</button></footer>
            </article>
            <div v-if="loaded && !existingForTable.length" class="histogram-empty"><i>▥</i><b>当前范围尚无直方图</b><span>选择数据库和数据表后，可为适合的非唯一列创建优化器统计。</span></div>
            <div v-else-if="!loaded" class="histogram-empty"><i>⌁</i><b>请选择目标实例</b><span>系统会实时读取 MySQL 版本和列统计元数据。</span></div>
          </div>
        </section>
      </section>
    </main>`
}
