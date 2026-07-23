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

export const formatBytes = raw => {
  let value = Number(raw || 0)
  if (!Number.isFinite(value) || value <= 0) return '—'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let unit = 0
  while (value >= 1024 && unit < units.length - 1) { value /= 1024; unit++ }
  return `${value >= 100 || unit === 0 ? value.toFixed(0) : value.toFixed(1)} ${units[unit]}`
}

export const parseIndexTaskDetail = detail => {
  const rows = []
  for (const event of detail?.events || []) {
    for (const line of String(field(event, 'Content', 'content') || '').split('\n')) {
      if (!line.startsWith('GMHA_MYSQL_INDEX\t')) continue
      const [, schema = '', table = '', name = '', type = '', unique = 'NO', columns = '', bytes = '0', tableRows = '0', dataBytes = '0', tableIndexBytes = '0'] = line.split('\t')
      rows.push({
        schema, table, name, type: String(type || 'BTREE').toUpperCase(), unique: String(unique).toUpperCase() === 'YES',
        columns: columns ? columns.split(',').filter(Boolean) : [], bytes: Number(bytes || 0), tableRows: Number(tableRows || 0),
        dataBytes: Number(dataBytes || 0), tableIndexBytes: Number(tableIndexBytes || 0)
      })
    }
  }
  return rows
}

const comparableColumns = item => (item.columns || []).map(column => normalized(column).replace(/\s+(asc|desc)$/i, ''))
const isPrefix = (left, right) => left.length <= right.length && left.every((column, index) => column === right[index])

// A non-unique BTREE index is redundant when another BTREE/PRIMARY index in
// the same table has the same leading columns. Unique indexes are only marked
// when another unique index has the exact same definition; a wider unique
// index does not guarantee uniqueness of a shorter prefix.
export const analyzeRedundantIndexes = input => {
  const items = (input || []).map((item, index) => ({ ...item, redundant: false, redundantReason: '', coveredBy: '', _order: index }))
  for (const item of items) {
    if (normalized(item.name) === 'primary') continue
    const columns = comparableColumns(item)
    if (!columns.length) continue
    const candidates = items.filter(candidate => {
      if (candidate === item || candidate.schema !== item.schema || candidate.table !== item.table) return false
      const candidateColumns = comparableColumns(candidate)
      if (!candidateColumns.length) return false
      if (['FULLTEXT', 'SPATIAL'].includes(item.type)) {
        return candidate.type === item.type && columns.length === candidateColumns.length && isPrefix(columns, candidateColumns)
      }
      if (!['BTREE', ''].includes(normalized(item.type).toUpperCase()) && normalized(item.name) !== 'primary') return false
      if (!['BTREE', ''].includes(normalized(candidate.type).toUpperCase()) && normalized(candidate.name) !== 'primary') return false
      if (item.unique) return candidate.unique && columns.length === candidateColumns.length && isPrefix(columns, candidateColumns)
      return isPrefix(columns, candidateColumns)
    }).sort((left, right) => {
      const leftExact = comparableColumns(left).length === columns.length ? 0 : 1
      const rightExact = comparableColumns(right).length === columns.length ? 0 : 1
      if (leftExact !== rightExact) return leftExact - rightExact
      const leftPrimary = normalized(left.name) === 'primary' ? 0 : 1
      const rightPrimary = normalized(right.name) === 'primary' ? 0 : 1
      if (leftPrimary !== rightPrimary) return leftPrimary - rightPrimary
      if (left.unique !== right.unique) return left.unique ? -1 : 1
      const lengthDiff = comparableColumns(right).length - comparableColumns(left).length
      return lengthDiff || left._order - right._order
    })
    const candidate = candidates.find(other => {
      const same = comparableColumns(other).length === columns.length
      return !same || other._order < item._order || normalized(other.name) === 'primary' || (other.unique && !item.unique)
    })
    if (!candidate) continue
    item.redundant = true
    item.coveredBy = candidate.name
    item.redundantReason = comparableColumns(candidate).length === columns.length ? '定义重复' : `被 ${candidate.name} 的左前缀覆盖`
  }
  return items.map(({ _order, ...item }) => item)
}

export default {
  name: 'IndexManagement',
  props: { instances: { type: Array, default: () => [] } },
  emits: ['open-task'],
  setup(props, { emit }) {
    const target = ref(''), rows = ref([]), loaded = ref(false), loading = ref(false), error = ref(''), notice = ref('')
    const keyword = ref(''), schemaFilter = ref('all'), typeFilter = ref('all'), redundantOnly = ref(false)
    const page = ref(1), pageSize = 15
    const progress = ref({ active: false, task: null, id: '', status: '', percent: 0, step: '', startedAt: 0 })
    const elapsedSeconds = ref(0)
    const createDialog = ref({
      open: false, schema: '', table: '', name: '', kind: 'btree', lock_mode: 'none', online_with_pt: false, purpose: '', impact: '',
      lock_acknowledged: false, columns: [{ name: '', prefix_length: 0, direction: '' }]
    })
    const renameDialog = ref({ open: false, item: null, new_name: '' })
    const deleteDialog = ref({ open: false, item: null, confirmation: '' })
    let elapsedTimer = null

    const instanceOptions = computed(() => (props.instances || []).map(item => ({
      value: `${field(item, 'MachineIP', 'machine_ip')}:${field(item, 'Port', 'port')}`,
      label: `${field(item, 'MachineName', 'machine_name') || field(item, 'MachineIP', 'machine_ip')} · ${field(item, 'MachineIP', 'machine_ip')}:${field(item, 'Port', 'port')}`,
      item
    })))
    watch(instanceOptions, options => {
      if (!options.some(option => option.value === target.value)) target.value = options[0]?.value || ''
    }, { immediate: true })

    const analyzedRows = computed(() => analyzeRedundantIndexes(rows.value))
    const schemas = computed(() => [...new Set(analyzedRows.value.map(item => item.schema))].sort())
    const types = computed(() => [...new Set(analyzedRows.value.map(item => item.type))].sort())
    const filteredRows = computed(() => {
      const query = normalized(keyword.value).trim()
      return analyzedRows.value.filter(item => {
        if (schemaFilter.value !== 'all' && item.schema !== schemaFilter.value) return false
        if (typeFilter.value !== 'all' && item.type !== typeFilter.value) return false
        if (redundantOnly.value && !item.redundant) return false
        return !query || [item.schema, item.table, item.name, item.type, ...(item.columns || [])].some(value => normalized(value).includes(query))
      })
    })
    const pageCount = computed(() => Math.max(1, Math.ceil(filteredRows.value.length / pageSize)))
    const pagedRows = computed(() => filteredRows.value.slice((page.value - 1) * pageSize, page.value * pageSize))
    watch([keyword, schemaFilter, typeFilter, redundantOnly], () => { page.value = 1 })
    watch(pageCount, count => { if (page.value > count) page.value = count })
    const redundantCount = computed(() => analyzedRows.value.filter(item => item.redundant).length)
    const totalBytes = computed(() => analyzedRows.value.reduce((sum, item) => sum + Number(item.bytes || 0), 0))
    const tablesCount = computed(() => new Set(analyzedRows.value.map(item => `${item.schema}.${item.table}`)).size)
    const lockDescription = computed(() => ({
      none: '只允许在线 DDL；若存储引擎无法做到 LOCK=NONE，任务会失败，不会升级锁级别。',
      shared: '允许读取但会阻塞写入，请仅在已确认的低峰维护窗口使用。',
      exclusive: '会阻塞读写，风险最高，只适用于停机维护窗口。',
      default: '由 MySQL 选择锁级别，可能阻塞业务，不建议在未知负载下使用。'
    })[createDialog.value.lock_mode])
    const createSQLPreview = computed(() => {
      const form = createDialog.value
      const columns = form.columns.filter(item => item.name.trim()).map(item => `\`${item.name.trim()}\`${Number(item.prefix_length) > 0 ? `(${Number(item.prefix_length)})` : ''}${item.direction ? ` ${item.direction}` : ''}`).join(', ')
      const prefix = ({ unique: 'UNIQUE INDEX', fulltext: 'FULLTEXT INDEX', spatial: 'SPATIAL INDEX' })[form.kind] || 'INDEX'
      const alter = `ADD ${prefix} \`${form.name || 'index_name'}\` (${columns || 'columns'})`
      return form.online_with_pt
        ? `pt-online-schema-change --alter "${alter}" --dry-run/--execute D=${form.schema || 'schema'},t=${form.table || 'table'}`
        : `ALTER TABLE \`${form.schema || 'schema'}\`.\`${form.table || 'table'}\` ${alter}, LOCK=${String(form.lock_mode || 'none').toUpperCase()};`
    })

    const parseTarget = () => {
      if (!target.value) throw new Error('请选择 MySQL 实例')
      const separator = target.value.lastIndexOf(':')
      return { machine: target.value.slice(0, separator), port: Number(target.value.slice(separator + 1)) }
    }
    const updateProgress = detail => {
      progress.value = {
        ...progress.value, active: !['success', 'failed'].includes(taskStatus(detail)), task: detail, id: taskID(detail),
        status: taskStatus(detail), percent: Number(field(detail?.task, 'ProgressPercent', 'progress_percent') || 0),
        step: field(detail?.task, 'CurrentStep', 'current_step') || '等待执行'
      }
    }
    const waitTask = async created => {
      const id = taskID(created)
      if (!id) throw new Error('任务创建失败：未返回任务 ID')
      progress.value = { active: true, task: created, id, status: 'pending', percent: 0, step: '等待 Agent 接收', startedAt: Date.now() }
      elapsedSeconds.value = 0
      for (let attempt = 0; attempt < 900; attempt++) {
        const detail = await request(`/tasks?id=${encodeURIComponent(id)}`)
        updateProgress(detail)
        if (['success', 'failed'].includes(taskStatus(detail))) return detail
        await new Promise(resolve => setTimeout(resolve, 1000))
      }
      throw new Error('等待索引任务超时，请到任务中心继续查看')
    }
    const taskFailure = detail => {
      const failedEvent = [...(detail?.events || [])].reverse().find(event => normalized(field(event, 'EventType', 'event_type')) === 'error')
      const failedStep = (detail?.steps || []).find(step => normalized(field(step, 'Status', 'status')) === 'failed')
      return field(failedEvent, 'Content', 'content') || field(failedStep, 'Message', 'message') || '索引任务执行失败'
    }
    const dispatch = async payload => {
      const created = await request('/tasks/mysql-indexes', { method: 'POST', body: JSON.stringify({ ...parseTarget(), ...payload }) })
      const detail = await waitTask(created)
      if (taskStatus(detail) !== 'success') throw new Error(taskFailure(detail))
      return detail
    }
    const loadIndexes = async (announce = true, force = false) => {
      if (!target.value || (loading.value && !force)) return
      const ownsLoadingState = !loading.value
      if (ownsLoadingState) loading.value = true
      error.value = ''
      try {
        const detail = await dispatch({ action: 'list' })
        rows.value = parseIndexTaskDetail(detail)
        loaded.value = true
        if (announce) notice.value = `已读取 ${rows.value.length} 个索引，发现 ${analyzeRedundantIndexes(rows.value).filter(item => item.redundant).length} 个疑似冗余索引`
      } catch (err) { error.value = err.message }
      finally { if (ownsLoadingState) loading.value = false }
    }
    watch(target, () => { rows.value = []; loaded.value = false; schemaFilter.value = 'all'; page.value = 1 })

    const openCreate = () => {
      createDialog.value = {
        open: true, schema: schemas.value[0] || '', table: '', name: '', kind: 'btree', lock_mode: 'none', online_with_pt: false, purpose: '', impact: '',
        lock_acknowledged: false, columns: [{ name: '', prefix_length: 0, direction: '' }]
      }
    }
    const addColumn = () => createDialog.value.columns.push({ name: '', prefix_length: 0, direction: '' })
    const removeColumn = index => { if (createDialog.value.columns.length > 1) createDialog.value.columns.splice(index, 1) }
    const submitCreate = async () => {
      const form = createDialog.value
      if (!form.columns.some(item => item.name.trim())) { error.value = '请至少填写一个索引列'; return }
      loading.value = true
      error.value = ''
      try {
        await dispatch({ ...form, action: 'create', columns: form.columns.filter(item => item.name.trim()).map(item => ({ ...item, prefix_length: Number(item.prefix_length || 0) })) })
        form.open = false
        notice.value = `索引 ${form.schema}.${form.table}.${form.name} 创建并核验成功`
        await loadIndexes(false, true)
      } catch (err) { error.value = err.message }
      finally { loading.value = false }
    }
    const openRename = item => { renameDialog.value = { open: true, item, new_name: item.name } }
    const submitRename = async () => {
      const dialog = renameDialog.value
      loading.value = true
      error.value = ''
      try {
        await dispatch({ action: 'rename', schema: dialog.item.schema, table: dialog.item.table, name: dialog.item.name, new_name: dialog.new_name })
        dialog.open = false
        notice.value = `索引已重命名为 ${dialog.new_name}`
        await loadIndexes(false, true)
      } catch (err) { error.value = err.message }
      finally { loading.value = false }
    }
    const openDelete = item => { deleteDialog.value = { open: true, item, confirmation: '' } }
    const deleteTarget = computed(() => {
      const item = deleteDialog.value.item
      return item ? `${item.schema}.${item.table}.${item.name}` : ''
    })
    const submitDelete = async () => {
      const dialog = deleteDialog.value
      loading.value = true
      error.value = ''
      try {
        await dispatch({ action: 'delete', schema: dialog.item.schema, table: dialog.item.table, name: dialog.item.name, confirmation: dialog.confirmation })
        dialog.open = false
        notice.value = `索引 ${deleteTarget.value} 已删除并核验`
        await loadIndexes(false, true)
      } catch (err) { error.value = err.message }
      finally { loading.value = false }
    }
    const openCurrentTask = () => { if (progress.value.task) emit('open-task', progress.value.task) }
    const closeProgress = () => { if (!progress.value.active) progress.value = { active: false, task: null, id: '', status: '', percent: 0, step: '', startedAt: 0 } }

    onMounted(() => {
      elapsedTimer = setInterval(() => {
        if (progress.value.startedAt && progress.value.active) elapsedSeconds.value = Math.floor((Date.now() - progress.value.startedAt) / 1000)
      }, 1000)
    })
    onUnmounted(() => { if (elapsedTimer) clearInterval(elapsedTimer) })

    return {
      target, rows, loaded, loading, error, notice, keyword, schemaFilter, typeFilter, redundantOnly, page, pageCount, pagedRows, filteredRows,
      progress, elapsedSeconds, createDialog, renameDialog, deleteDialog, deleteTarget, instanceOptions, schemas, types, analyzedRows,
      redundantCount, totalBytes, tablesCount, lockDescription, createSQLPreview, formatBytes, loadIndexes, openCreate, addColumn,
      removeColumn, submitCreate, openRename, submitRename, openDelete, submitDelete, openCurrentTask, closeProgress
    }
  },
  template: `
    <main class="index-management-page">
      <section class="index-hero">
        <div><p>INDEX GOVERNANCE</p><h3>索引管理</h3><span>查看索引类型与空间，识别疑似冗余，并通过可审计任务安全执行 DDL。</span></div>
        <div class="index-target"><label>目标实例<select v-model="target"><option value="">请选择实例</option><option v-for="option in instanceOptions" :key="option.value" :value="option.value">{{ option.label }}</option></select></label><button class="secondary" :disabled="loading || !target" @click="loadIndexes()">{{ loading ? '执行中…' : '刷新索引' }}</button><button class="primary" :disabled="loading || !target" @click="openCreate">创建索引</button></div>
      </section>

      <div v-if="error" class="index-message error"><b>操作未完成</b><span>{{ error }}</span><button @click="error=''">×</button></div>
      <div v-if="notice" class="index-message success"><b>操作完成</b><span>{{ notice }}</span><button @click="notice=''">×</button></div>

      <section v-if="progress.task" :class="['index-progress-card', progress.status]">
        <header><div><b>{{ progress.status==='failed' ? '索引任务失败' : progress.status==='success' ? '索引任务已完成' : '索引任务执行中' }}</b><small>{{ progress.id }} · 已用 {{ elapsedSeconds }} 秒</small></div><div><button class="text-button" @click="openCurrentTask">查看任务详情</button><button v-if="!progress.active" class="index-progress-close" @click="closeProgress">×</button></div></header>
        <div class="index-progress-track"><i :style="{width:progress.percent+'%'}"></i></div>
        <footer><span>{{ progress.step }}</span><b>{{ progress.percent }}%</b></footer>
      </section>

      <section class="index-kpis">
        <article><i>▤</i><div><small>索引总数</small><b>{{ analyzedRows.length }}</b><span>{{ tablesCount }} 张业务表</span></div></article>
        <article><i>◫</i><div><small>估算占用空间</small><b>{{ formatBytes(totalBytes) }}</b><span>基于 InnoDB 索引页统计</span></div></article>
        <article class="warning"><i>!</i><div><small>疑似冗余</small><b>{{ redundantCount }}</b><span>删除前必须结合查询负载复核</span></div></article>
        <article><i>◇</i><div><small>索引种类</small><b>{{ types.length }}</b><span>{{ types.join(' / ') || '尚未采集' }}</span></div></article>
      </section>

      <section class="index-workspace">
        <header class="index-toolbar">
          <label class="index-search"><span>⌕</span><input v-model.trim="keyword" placeholder="搜索库、表、索引或字段"></label>
          <select v-model="schemaFilter"><option value="all">全部数据库</option><option v-for="schema in schemas" :key="schema">{{ schema }}</option></select>
          <select v-model="typeFilter"><option value="all">全部类型</option><option v-for="type in types" :key="type">{{ type }}</option></select>
          <label class="redundant-switch"><input v-model="redundantOnly" type="checkbox"><span>仅看疑似冗余</span><b>{{ redundantCount }}</b></label>
        </header>
        <div class="index-analysis-note"><i>i</i><span>冗余识别规则：同表同列重复，或非唯一 BTREE 索引被另一个索引的左前缀完整覆盖。该结果是候选提示，不会自动删除索引。</span></div>
        <div class="index-table-wrap">
          <table><thead><tr><th>数据库 / 表</th><th>索引名称</th><th>种类</th><th>索引列</th><th>估算空间</th><th>冗余分析</th><th>操作</th></tr></thead>
            <tbody>
              <tr v-for="item in pagedRows" :key="item.schema+'.'+item.table+'.'+item.name" :class="{redundant:item.redundant}">
                <td><b>{{ item.schema }}</b><small>{{ item.table }} · 约 {{ item.tableRows.toLocaleString() }} 行</small></td>
                <td><code>{{ item.name }}</code><small v-if="item.name==='PRIMARY'">主键索引受保护</small></td>
                <td><span :class="['index-kind',item.type.toLowerCase()]">{{ item.unique ? 'UNIQUE ' : '' }}{{ item.type }}</span></td>
                <td><div class="index-columns"><code v-for="column in item.columns" :key="column">{{ column }}</code></div></td>
                <td><b>{{ formatBytes(item.bytes) }}</b><small>表索引共 {{ formatBytes(item.tableIndexBytes) }}</small></td>
                <td><span v-if="item.redundant" class="redundant-badge">疑似冗余</span><small v-if="item.redundant">{{ item.redundantReason }} · 保留 {{ item.coveredBy }}</small><span v-else class="healthy-index">未发现覆盖</span></td>
                <td><div class="index-actions"><button class="text-button" :disabled="item.name==='PRIMARY' || loading" @click="openRename(item)">重命名</button><button class="danger-link" :disabled="item.name==='PRIMARY' || loading" @click="openDelete(item)">删除</button></div></td>
              </tr>
              <tr v-if="!pagedRows.length"><td colspan="7" class="index-empty">{{ loaded ? '没有匹配的索引。' : '选择实例并刷新，读取索引、空间和冗余分析。' }}</td></tr>
            </tbody>
          </table>
        </div>
        <footer class="index-pager"><span>显示 {{ pagedRows.length }} / {{ filteredRows?.length || analyzedRows.length }} 项</span><div><button class="secondary" :disabled="page<=1" @click="page--">上一页</button><span>{{ page }} / {{ pageCount }}</span><button class="secondary" :disabled="page>=pageCount" @click="page++">下一页</button></div></footer>
      </section>

      <div v-if="createDialog.open" class="modal-mask index-modal-mask" @click.self="createDialog.open=false">
        <form class="modal index-create-modal" @submit.prevent="submitCreate">
          <header><div><p>CREATE INDEX</p><h2>创建索引</h2><span>先说明业务目标和预期影响，再选择明确的锁策略。</span></div><button type="button" @click="createDialog.open=false">×</button></header>
          <div class="index-create-layout">
            <section>
              <h4>1. 明确目标</h4>
              <div class="index-form-grid"><label>数据库<input v-model.trim="createDialog.schema" required placeholder="例如 orders"></label><label>数据表<input v-model.trim="createDialog.table" required placeholder="例如 order_items"></label><label>索引名称<input v-model.trim="createDialog.name" required placeholder="例如 idx_order_status"></label><label>索引种类<select v-model="createDialog.kind"><option value="btree">BTREE 普通索引</option><option value="unique">UNIQUE 唯一索引</option><option value="fulltext">FULLTEXT 全文索引</option><option value="spatial">SPATIAL 空间索引</option></select></label><label class="wide">创建目标<textarea v-model.trim="createDialog.purpose" required placeholder="例如：降低订单状态查询的扫描行数，支撑 API P95 目标"></textarea></label><label class="wide">预期影响<textarea v-model.trim="createDialog.impact" required placeholder="例如：预计增加 1.2 GB 空间；低峰执行；验证查询延迟和写入 TPS"></textarea></label></div>
              <h4>2. 定义索引列</h4>
              <div class="index-column-editor"><div v-for="(column,index) in createDialog.columns" :key="index"><input v-model.trim="column.name" required placeholder="字段名"><input v-model.number="column.prefix_length" type="number" min="0" max="3072" placeholder="前缀长度"><select v-model="column.direction" :disabled="['fulltext','spatial'].includes(createDialog.kind)"><option value="">默认顺序</option><option value="ASC">ASC</option><option value="DESC">DESC</option></select><button type="button" :disabled="createDialog.columns.length===1" @click="removeColumn(index)">×</button></div><button type="button" class="secondary" :disabled="createDialog.columns.length>=16" @click="addColumn">+ 添加字段</button></div>
            </section>
            <aside>
              <h4>3. 锁与执行策略</h4>
              <label class="index-pt-switch"><input v-model="createDialog.online_with_pt" type="checkbox"><span><b>使用 PT 工具在线创建</b><small>使用 pt-online-schema-change 影子表分块复制，先 dry-run 再正式执行。</small></span></label>
              <label>DDL 锁策略<select v-model="createDialog.lock_mode" :disabled="createDialog.online_with_pt"><option value="none">LOCK=NONE（推荐）</option><option value="shared">LOCK=SHARED</option><option value="exclusive">LOCK=EXCLUSIVE</option><option value="default">LOCK=DEFAULT</option></select></label>
              <div v-if="createDialog.online_with_pt" class="index-lock-impact pt"><b>PT 在线变更风险</b><p>会创建影子表和同步触发器，临时空间可能接近原表大小，并增加写放大与 binlog；已有触发器、缺少主键/唯一键、复制过滤或高负载会使预演或执行失败。</p><span>任务按 0.5 秒分块目标复制；Threads_running 超过 25 暂停、超过 50 中止，复制延迟超过 10 秒暂停，并实时上报复制百分比。</span></div>
              <div v-else :class="['index-lock-impact',createDialog.lock_mode]"><b>{{ createDialog.lock_mode==='none' ? '在线 DDL 门禁' : '可能阻塞业务' }}</b><p>{{ lockDescription }}</p><span>获取元数据锁的瞬间仍可能等待长事务。任务将使用 10 秒 lock_wait_timeout，超时直接失败。</span></div>
              <label class="index-lock-ack"><input v-model="createDialog.lock_acknowledged" type="checkbox"><span><b>我已确认目标表、空间增长和锁影响</b><small>已检查长事务与维护窗口；失败时不会自动改用更强锁。</small></span></label>
              <div class="index-sql-preview"><small>执行预览</small><code>{{ createSQLPreview }}</code></div>
              <div class="index-flow-preview"><span>1 影响预检</span><i>→</i><span v-if="createDialog.online_with_pt">2 PT 预演</span><i v-if="createDialog.online_with_pt">→</i><span>{{ createDialog.online_with_pt ? '3 在线复制' : '2 执行 DDL' }}</span><i>→</i><span>{{ createDialog.online_with_pt ? '4 结果核验' : '3 结果核验' }}</span></div>
            </aside>
          </div>
          <footer><span>提交后可在本页实时查看任务步骤与进度。</span><div><button type="button" class="secondary" @click="createDialog.open=false">取消</button><button class="primary" :disabled="loading || !createDialog.lock_acknowledged">{{ loading ? '正在执行…' : '创建并跟踪进度' }}</button></div></footer>
        </form>
      </div>

      <div v-if="renameDialog.open" class="modal-mask index-modal-mask" @click.self="renameDialog.open=false"><form class="modal index-small-modal" @submit.prevent="submitRename"><header><div><p>RENAME INDEX</p><h2>重命名索引</h2></div><button type="button" @click="renameDialog.open=false">×</button></header><div><p>目标：<b>{{ renameDialog.item?.schema }}.{{ renameDialog.item?.table }}.{{ renameDialog.item?.name }}</b></p><label>新索引名称<input v-model.trim="renameDialog.new_name" required autofocus></label><div class="index-lock-impact none"><b>元数据锁提示</b><p>重命名是元数据操作，但仍需短暂获取表级元数据锁；长事务可能导致等待或失败。</p></div></div><footer><button type="button" class="secondary" @click="renameDialog.open=false">取消</button><button class="primary" :disabled="loading || renameDialog.new_name===renameDialog.item?.name">确认重命名</button></footer></form></div>

      <div v-if="deleteDialog.open" class="modal-mask index-modal-mask" @click.self="deleteDialog.open=false"><form class="modal index-small-modal danger" @submit.prevent="submitDelete"><header><div><p>DESTRUCTIVE INDEX DDL</p><h2>删除索引</h2></div><button type="button" @click="deleteDialog.open=false">×</button></header><div><p>删除可能使查询退化，并在执行时获取元数据锁。系统不会自动删除任何疑似冗余索引。</p><label>输入 <b>{{ deleteTarget }}</b> 确认<input v-model.trim="deleteDialog.confirmation" required autocomplete="off"></label><div class="index-delete-impact"><span>当前估算空间</span><b>{{ formatBytes(deleteDialog.item?.bytes) }}</b><span>冗余分析</span><b>{{ deleteDialog.item?.redundant ? deleteDialog.item?.redundantReason : '未识别为冗余' }}</b></div></div><footer><button type="button" class="secondary" @click="deleteDialog.open=false">取消</button><button class="danger-button" :disabled="loading || deleteDialog.confirmation!==deleteTarget">{{ loading ? '正在删除…' : '确认删除索引' }}</button></footer></form></div>
    </main>`
}
