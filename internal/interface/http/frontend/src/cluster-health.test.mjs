import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const source = readFileSync(new URL('./main.js', import.meta.url), 'utf8')
const start = source.indexOf('function clusterAgentHealth(item)')
const end = source.indexOf('async function loadClusterPage', start)
function health(code, { error = '', abnormal = 0, matches = true, machines = 3, online = 3 } = {}) {
  return new Function('clusterName', 'selectedClusterDetail', 'selectedClusterTopologyMatches', 'clusterTopologyError', 'clusterTopology', 'clusterAbnormalInstanceCount', 'clusterMachineCount', 'clusterAgentCount', `${source.slice(start, end)}; return clusterAgentHealth({name:'demo'})`)(
    item => item?.name, { value: { name: 'demo' } }, () => matches,
    { value: error }, { value: { agent_health: code } }, () => abnormal,
    () => machines, () => online
  )
}

test('detail status uses cluster-wide heartbeat summary without paginated agents', () => {
  assert.equal(health('offline').label, 'Agent 离线')
  assert.equal(health('warning').code, 'warning')
  assert.equal(health('healthy').code, 'healthy')
  assert.equal(health('healthy', { abnormal: 3 }).code, 'warning')
})
test('failed refresh or mismatched snapshot falls back to current agent counts', () => {
  assert.deepEqual(health('healthy', { error: 'network unavailable', online: 2 }), { code: 'warning', label: '部分离线' })
  assert.equal(health('healthy', { matches: false, online: 0 }).code, 'offline')
  assert.equal(health(undefined).code, 'healthy')
  assert.equal(health('healthy', { machines: 0, online: 0 }).code, 'pending')
  assert.doesNotMatch(source, /<span><i><\/i>正常运行<\/span>/)
})

test('current offline agents override a stale healthy topology snapshot', () => {
  assert.deepEqual(health('healthy', { online: 2 }), { code: 'warning', label: '部分离线' })
  assert.equal(health('healthy', { online: 0 }).code, 'offline')
})
