import test from 'node:test'
import assert from 'node:assert/strict'
import { apiEndpoints, instanceManagementOperations, manualModules } from './documentation.js'

test('documentation covers every registered API route family', async () => {
  const { readFile } = await import('node:fs/promises')
  const router = await readFile('../router.go', 'utf8')
  const registered = [...router.matchAll(/mux\.Handle(?:Func)?\("([^"]+)"/g)]
    .map(match => match[1])
    .filter(path => path.startsWith('/api/v1/'))
  const paths = apiEndpoints.map(item => `/api/v1${item.path.split('?')[0]}`)
  const missing = registered.filter(route => route.endsWith('/')
    ? !paths.some(path => path.startsWith(route))
    : !paths.some(path => path === route))
  assert.deepEqual(missing, [])
})

test('API examples are complete and endpoint keys are unique', () => {
  const keys = apiEndpoints.map(item => `${item.method} ${item.path}`)
  assert.equal(new Set(keys).size, keys.length)
  for (const item of apiEndpoints) {
    assert.ok(item.category)
    assert.match(item.method, /^(GET|POST|PUT|DELETE)$/)
    assert.ok(item.path.startsWith('/'))
    assert.ok(item.title)
    assert.ok(Object.hasOwn(item, 'response'))
  }
})

test('cluster management UI operations stay covered by the API manual', async () => {
  const documented = new Set(apiEndpoints.map(item => `${item.method} ${item.path.split('?')[0]}`))
  const required = [
    ['GET', '/clusters'],
    ['POST', '/clusters'],
    ['GET', '/clusters/{cluster_name}'],
    ['PUT', '/clusters/{cluster_name}'],
    ['DELETE', '/clusters/{cluster_name}'],
    ['GET', '/clusters/{cluster_name}/machines'],
    ['POST', '/clusters/{cluster_name}/members'],
    ['POST', '/clusters/{cluster_name}/cleanup'],
    ['DELETE', '/machines/{machine_id}/assign-cluster'],
    ['GET', '/clusters/{cluster_name}/topology'],
    ['GET', '/clusters/{cluster_name}/vip/config'],
    ['POST', '/clusters/{cluster_name}/vip/config'],
    ['DELETE', '/clusters/{cluster_name}/vip/config'],
    ['GET', '/clusters/{cluster_name}/vip/status'],
    ['POST', '/clusters/{cluster_name}/vip/scan'],
    ['POST', '/clusters/{cluster_name}/vip/validate'],
    ['POST', '/clusters/{cluster_name}/architecture/plan'],
    ['POST', '/clusters/{cluster_name}/architecture/start'],
    ['GET', '/clusters/{cluster_name}/architecture/{run_id}'],
    ['POST', '/clusters/{cluster_name}/architecture/{run_id}/force'],
    ['POST', '/tasks/cluster-mysql-uninstall'],
    ['POST', '/tasks/mysql-cluster-upgrade/plan'],
    ['POST', '/tasks/mysql-cluster-upgrade/start'],
    ['GET', '/tasks/mysql-cluster-upgrade'],
    ['GET', '/backup/targets'],
    ['GET', '/backup/policies'],
    ['GET', '/backup/policies/{policy_id}'],
    ['POST', '/backup/policies'],
    ['PUT', '/backup/policies/{policy_id}'],
    ['DELETE', '/backup/policies/{policy_id}'],
    ['POST', '/backup/policies/{policy_id}/run'],
    ['POST', '/backup/cluster-runs'],
    ['GET', '/backup/runs'],
    ['GET', '/backup/runs/{run_id}'],
    ['POST', '/backup/runs/{run_id}/restore'],
    ['GET', '/ai/capabilities']
  ]
  const missing = required.filter(([method, path]) => !documented.has(`${method} ${path}`))
  assert.deepEqual(missing, [])

  const { readFile } = await import('node:fs/promises')
  const source = await readFile('./src/main.js', 'utf8')
  for (const fragment of [
    '/members',
    '/cleanup',
    '/assign-cluster',
    '/topology',
    '/vip/config',
    '/vip/status',
    '/vip/scan',
    '/architecture/plan',
    '/architecture/start',
    '/backup/targets',
    '/backup/policies',
    '/backup/runs/'
  ]) {
    assert.ok(source.includes(fragment), `cluster UI no longer references documented API fragment ${fragment}`)
  }
})

test('AI capability manual covers direct actions and secure-input APIs', () => {
  const capabilities = apiEndpoints.find(item => item.method === 'GET' && item.path === '/ai/capabilities')
  assert.ok(capabilities)
  assert.ok(Array.isArray(capabilities.response.actions))
  assert.ok(Array.isArray(capabilities.response.cluster_endpoints))
  const secure = capabilities.response.cluster_endpoints.find(item => item.id === 'clusters.mysql.install')
  assert.equal(secure.invocation_mode, 'secure_input_api')
  assert.deepEqual(secure.sensitive_parameters, ['root_password', 'accounts[].password'])
  assert.ok(capabilities.note.includes('并非平台不支持'))

  const settings = apiEndpoints.find(item => item.method === 'PUT' && item.path === '/ai/settings')
  for (const action of [
    'configure_cluster_vip',
    'scan_cluster_vip',
    'run_cluster_backup',
    'rolling_upgrade_cluster_mysql',
    'uninstall_cluster_mysql'
  ]) {
    assert.ok(settings.body.allowed_actions.includes(action), `AI settings example is missing ${action}`)
  }
})

test('backup recovery examples match kernel response fields and recovery modes', () => {
  const endpointByKey = new Map(apiEndpoints
    .filter(item => item.category === '备份恢复')
    .map(item => [`${item.method} ${item.path.split('?')[0]}`, item]))
  const expected = [
    'GET /backup/targets',
    'GET /backup/policies',
    'GET /backup/policies/{policy_id}',
    'POST /backup/policies',
    'PUT /backup/policies/{policy_id}',
    'DELETE /backup/policies/{policy_id}',
    'POST /backup/policies/{policy_id}/run',
    'POST /backup/cluster-runs',
    'GET /backup/runs',
    'GET /backup/runs/{run_id}',
    'POST /backup/runs/{run_id}/restore'
  ]
  assert.deepEqual(expected.filter(key => !endpointByKey.has(key)), [])

  const restore = endpointByKey.get('POST /backup/runs/{run_id}/restore')
  assert.equal(restore.body.mode, 'physical')
  assert.equal(restore.response.task.Type, 'exec')
  assert.equal(restore.response.task.Status, 'pending')
  for (const value of ['physical', 'point_in_time', 'flashback', 'RESTORE {run_id}', 'FLASHBACK {run_id}', 'include_binlog=true']) {
    assert.ok(restore.note.includes(value), `restore API note is missing ${value}`)
  }
  const batch = endpointByKey.get('POST /backup/cluster-runs')
  assert.ok(Object.hasOwn(batch.response, 'parent_task_id'))
  assert.ok(Object.hasOwn(batch.response, 'created'))
  assert.ok(Object.hasOwn(batch.response, 'failed'))
})

test('all fourteen instance-management operations have callable and documented APIs', () => {
  const endpointKeys = new Set(apiEndpoints.map(item => `${item.method} ${item.path.split('?')[0]}`))
  assert.equal(instanceManagementOperations.length, 14)
  assert.equal(new Set(instanceManagementOperations.map(item => item.name)).size, 14)
  for (const operation of instanceManagementOperations) {
    assert.ok(operation.mode)
    assert.ok(operation.apis.length > 0, `${operation.name} has no API`)
    for (const contract of operation.apis) {
      const [method, path] = contract.split(' ')
      assert.ok(endpointKeys.has(`${method} ${path.split('?')[0]}`), `${operation.name} references undocumented API ${contract}`)
    }
  }
})

test('instance-management API examples use the implemented request contract', () => {
  const endpointByKey = new Map(apiEndpoints.map(item => [`${item.method} ${item.path.split('?')[0]}`, item]))
  assert.deepEqual(endpointByKey.get('POST /sql-diagnostics/explain').body, {
    machine_id: 'machine-01',
    port: 3306,
    database: 'app',
    sql: 'SELECT * FROM orders WHERE user_id = 42'
  })
  assert.deepEqual(endpointByKey.get('POST /mysql/histograms').body.columns, ['status'])
  assert.equal(endpointByKey.get('POST /tasks/mysql-indexes').body.kind, 'btree')
  assert.equal(endpointByKey.get('POST /tasks/mysql-parameters').body.changes[0].action, 'update')
  assert.equal(endpointByKey.get('POST /tasks/mysql-archive').body.confirmation, 'app.orders->archive.orders_2025')
  assert.equal(endpointByKey.get('GET /tasks/database-inspection/report').contentType, 'application/vnd.openxmlformats-officedocument.wordprocessingml.document')
  assert.equal(endpointByKey.get('GET /tasks/database-inspection/data').contentType, 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet')
})

test('manual includes an engineering deep dive for every functional module', () => {
  assert.ok(manualModules.length >= 20)
  assert.equal(new Set(manualModules.map(item => item.id)).size, manualModules.length)
  for (const item of manualModules) {
    assert.ok(item.steps.length >= 3)
    assert.ok(item.principle)
    assert.ok(item.implementation)
    assert.ok(item.caution)
    assert.ok(item.flow.length >= 3, `${item.id} is missing its execution flow`)
    assert.ok(item.mechanisms.length >= 4, `${item.id} is missing implementation mechanisms`)
    assert.ok(item.invariants.length >= 3, `${item.id} is missing safety invariants`)
    assert.ok(item.components.length >= 2, `${item.id} is missing component references`)
    assert.ok(item.limitations.length >= 2, `${item.id} is missing implementation boundaries`)
  }
})

test('every first-level product menu has a matching manual chapter', async () => {
  const { readFile } = await import('node:fs/promises')
  const source = await readFile('./src/main.js', 'utf8')
  const navSource = source.slice(source.indexOf('const navGroups'), source.indexOf('const UserManual'))
  const menuIDs = [...navSource.matchAll(/\{ id: '([^']+)'/g)]
    .map(match => match[1])
    .filter(id => !['manual', 'api-docs'].includes(id))
  const manualIDs = new Set(manualModules.map(item => item.id))
  assert.deepEqual(menuIDs.filter(id => !manualIDs.has(id)), [])
})

test('business VIP chapter documents the implemented ARP, BGP and split-brain barriers', () => {
  const vip = manualModules.find(item => item.id === 'cluster-architecture')
  const text = JSON.stringify(vip)
  for (const fact of ['arping -U', 'vtysh', 'advertised-routes', '零持有者', '连续两轮', 'CONFLICT', 'offline_mode', 'GTID', 'pt-table-checksum']) {
    assert.ok(text.includes(fact), `VIP chapter is missing ${fact}`)
  }
})

test('Manager VIP limitations are not presented as business VIP guarantees', () => {
  const managerHA = manualModules.find(item => item.id === 'manager-ha')
  const text = JSON.stringify(managerHA)
  for (const fact of ['不支持 BGP', '没有全节点持有者扫描', '没有连续两轮唯一持有者验证', '外部主机/网络围栏']) {
    assert.ok(text.includes(fact), `Manager HA chapter is missing ${fact}`)
  }
})

test('AI operations chapter documents the server-side safety boundary', () => {
  const ai = manualModules.find(item => item.id === 'ai-automation')
  const text = JSON.stringify(ai)
  for (const fact of ['AES-256-GCM', '白名单', '风险来自 Manager 固定目录', '30 分钟', '精确匹配', 'TaskService', '模型不得执行任意命令']) {
    assert.ok(text.includes(fact), `AI operations chapter is missing ${fact}`)
  }
})
