import { computed, onMounted, ref, watch } from 'vue/dist/vue.esm-bundler.js'

const request = async (path, options = {}) => {
  const response = await fetch(`/api/v1${path}`, { ...options, headers: { 'Content-Type': 'application/json', ...(options.headers || {}) } })
  const payload = await response.json().catch(() => ({}))
  if (!response.ok) throw new Error(payload.error || `请求失败（${response.status}）`)
  return payload
}

const emptyForm = () => ({ id: '', username: '', name: '', email: '', phone: '', password: '', role: 'operator', permissions: [], enabled: true })

export default {
  props: { currentUser: { type: Object, required: true } },
  setup(props) {
    const users = ref([]), catalog = ref({ roles: [], permissions: [] }), loading = ref(false), error = ref(''), notice = ref(''), keyword = ref(''), form = ref(null)
    const filteredUsers = computed(() => {
      const key = keyword.value.trim().toLowerCase()
      if (!key) return users.value
      return users.value.filter(item => [item.username, item.name, item.email, item.phone, roleName(item.role)].join(' ').toLowerCase().includes(key))
    })
    const enabledCount = computed(() => users.value.filter(item => item.enabled).length)
    const adminCount = computed(() => users.value.filter(item => item.role === 'admin').length)
    const roleName = role => catalog.value.roles.find(item => item.id === role)?.name || role
    const permissionName = id => id === '*' ? '全部权限' : (catalog.value.permissions.find(item => item.id === id)?.name || id)
    const isBuiltinAdmin = item => item?.username === 'admin'
    const canEdit = item => !isBuiltinAdmin(item) || props.currentUser.username === 'admin'
    const load = async () => {
      loading.value = true; error.value = ''
      try {
        const [accounts, definitions] = await Promise.all([request('/accounts'), request('/accounts/catalog')])
        users.value = accounts.items || []; catalog.value = definitions || { roles: [], permissions: [] }
      } catch (err) { error.value = err.message }
      finally { loading.value = false }
    }
    const openCreate = () => {
      const next = emptyForm(), role = catalog.value.roles.find(item => item.id === next.role)
      next.permissions = [...(role?.permissions || [])]
      form.value = next
    }
    const openEdit = item => {
      if (!canEdit(item)) return
      form.value = { ...JSON.parse(JSON.stringify(item)), password: '' }
    }
    const applyRoleDefaults = () => {
      const role = catalog.value.roles.find(item => item.id === form.value.role)
      form.value.permissions = [...(role?.permissions || [])]
    }
    const togglePermission = id => {
      if (form.value.role === 'admin') return
      form.value.permissions = form.value.permissions.includes(id) ? form.value.permissions.filter(item => item !== id) : [...form.value.permissions, id]
    }
    const save = async () => {
      loading.value = true; error.value = ''
      try {
        const payload = { username: form.value.username, name: form.value.name, email: form.value.email, phone: form.value.phone, password: form.value.password, role: form.value.role, permissions: form.value.permissions, enabled: form.value.enabled }
        await request(form.value.id ? `/accounts/${encodeURIComponent(form.value.id)}` : '/accounts', { method: form.value.id ? 'PUT' : 'POST', body: JSON.stringify(payload) })
        notice.value = form.value.id ? '账号信息已更新' : '账号已创建'; form.value = null; await load()
      } catch (err) { error.value = err.message }
      finally { loading.value = false }
    }
    const remove = async item => {
      if (!confirm(`确认删除账号“${item.username}”？删除后该账号的登录会话会立即失效。`)) return
      loading.value = true; error.value = ''
      try { await request(`/accounts/${encodeURIComponent(item.id)}`, { method: 'DELETE' }); notice.value = '账号已删除'; await load() }
      catch (err) { error.value = err.message }
      finally { loading.value = false }
    }
    watch(() => form.value?.role, (value, old) => { if (form.value && value !== old && old !== undefined) applyRoleDefaults() })
    onMounted(load)
    return { users, catalog, loading, error, notice, keyword, form, filteredUsers, enabledCount, adminCount, roleName, permissionName, isBuiltinAdmin, canEdit, openCreate, openEdit, togglePermission, save, remove, load }
  },
  template: `
    <section class="account-management">
      <header class="account-hero"><div><p>ACCOUNT & ACCESS</p><h2>账号管理</h2><span>统一维护平台身份、角色和功能权限。只有管理员可以进入本页。</span></div><button class="primary" type="button" @click="openCreate">＋ 创建账号</button></header>
      <div v-if="error" class="alert error"><b>操作未完成</b><span>{{ error }}</span><button @click="error=''">×</button></div>
      <div v-if="notice" class="alert success"><b>操作成功</b><span>{{ notice }}</span><button @click="notice=''">×</button></div>
      <section class="account-summary-grid">
        <article><i class="users">人</i><span><small>平台账号</small><b>{{ users.length }}</b></span></article>
        <article><i class="online">✓</i><span><small>正常使用</small><b>{{ enabledCount }}</b></span></article>
        <article><i class="admin">盾</i><span><small>管理员</small><b>{{ adminCount }}</b></span></article>
      </section>
      <section class="account-panel">
        <header><div><h3>全部账号</h3><p>账号资料、角色与权限在卡片中集中展示。</p></div><label class="account-search"><span>⌕</span><input v-model.trim="keyword" placeholder="搜索账号、姓名、邮箱或电话"></label><button class="secondary" :disabled="loading" @click="load">{{ loading?'刷新中…':'刷新' }}</button></header>
        <div class="account-card-grid">
          <article v-for="item in filteredUsers" :key="item.id" :class="['account-card',{disabled:!item.enabled,builtin:isBuiltinAdmin(item)}]">
            <header><span class="account-avatar">{{ (item.name || item.username).slice(0,1).toUpperCase() }}</span><div><b>{{ item.name }}</b><small>@{{ item.username }}</small></div><em :class="{on:item.enabled}"><i></i>{{ item.enabled?'正常':'已停用' }}</em></header>
            <dl><div><dt>角色</dt><dd><span :class="['account-role',item.role]">{{ roleName(item.role) }}</span></dd></div><div><dt>邮箱</dt><dd>{{ item.email || '未填写' }}</dd></div><div><dt>电话</dt><dd>{{ item.phone || '未填写' }}</dd></div><div><dt>最近登录</dt><dd>{{ item.last_login_at ? new Date(item.last_login_at).toLocaleString('zh-CN',{hour12:false}) : '尚未登录' }}</dd></div></dl>
            <section><small>功能权限</small><div><span v-for="permission in item.permissions.slice(0,4)" :key="permission">{{ permissionName(permission) }}</span><em v-if="item.permissions.length>4">+{{ item.permissions.length-4 }}</em><span v-if="!item.permissions.length">未分配</span></div></section>
            <footer><small v-if="isBuiltinAdmin(item)">内置最高权限账号</small><small v-else>创建于 {{ new Date(item.created_at).toLocaleDateString('zh-CN') }}</small><div><button class="text-button" :disabled="!canEdit(item)" @click="openEdit(item)">编辑</button><button v-if="!isBuiltinAdmin(item)" class="danger-link" :disabled="item.id===currentUser.id" @click="remove(item)">删除</button></div></footer>
          </article>
          <p v-if="!filteredUsers.length" class="account-empty">没有符合条件的账号。</p>
        </div>
      </section>
      <div v-if="form" class="modal-mask account-modal-mask" @click.self="form=null"><form class="modal account-editor" @submit.prevent="save">
        <div class="modal-head"><div><p>PLATFORM ACCOUNT</p><h2>{{ form.id?'编辑账号':'创建平台账号' }}</h2><span>{{ form.id?'调整资料、角色、权限或登录密码':'填写账号资料并一次性赋予角色和权限' }}</span></div><button type="button" @click="form=null">×</button></div>
        <div class="account-editor-body">
          <section><header><i>1</i><div><b>身份信息</b><small>账号用于登录，姓名用于操作记录与界面展示</small></div></header><div class="account-form-grid"><label>登录账号<input v-model.trim="form.username" required :disabled="!!form.id" minlength="3" maxlength="32" placeholder="例如 zhangsan"></label><label>姓名<input v-model.trim="form.name" required placeholder="例如 张三"></label><label>邮箱<input v-model.trim="form.email" type="email" placeholder="name@example.com"></label><label>电话<input v-model.trim="form.phone" type="tel" placeholder="13800000000"></label><label class="wide">{{ form.id?'新密码（留空表示不修改）':'登录密码' }}<input v-model="form.password" type="password" :required="!form.id" :minlength="form.id?0:8" autocomplete="new-password" placeholder="至少 8 位"></label></div></section>
          <section><header><i>2</i><div><b>角色与状态</b><small>角色提供推荐权限模板，之后仍可逐项调整</small></div></header><div class="account-role-grid"><label v-for="role in catalog.roles" :key="role.id" :class="{active:form.role===role.id,locked:isBuiltinAdmin(form)&&role.id!=='admin'}"><input v-model="form.role" type="radio" :value="role.id" :disabled="isBuiltinAdmin(form)&&role.id!=='admin'"><span><b>{{ role.name }}</b><small>{{ role.description }}</small></span></label></div><label class="account-enabled"><input v-model="form.enabled" type="checkbox" :disabled="isBuiltinAdmin(form)"><span><b>允许登录</b><small>{{ form.enabled?'账号保存后可以正常登录':'停用后现有会话会立即失效' }}</small></span></label></section>
          <section><header><i>3</i><div><b>功能权限</b><small>管理员角色自动拥有全部权限；其他角色可按职责精细调整</small></div></header><div class="account-permission-grid"><button v-for="permission in catalog.permissions" :key="permission.id" type="button" :disabled="form.role==='admin'" :class="{active:form.role==='admin'||form.permissions.includes(permission.id)}" @click="togglePermission(permission.id)"><i></i><span><b>{{ permission.name }}</b><small>{{ permission.description }}</small></span></button></div></section>
        </div>
        <div class="modal-actions"><button type="button" class="secondary" @click="form=null">取消</button><button class="primary" :disabled="loading">{{ loading?'保存中…':'保存账号' }}</button></div>
      </form></div>
    </section>`
}
