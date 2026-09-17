import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
const source = readFileSync(new URL('./main.js', import.meta.url), 'utf8')
const start = source.indexOf('function upgradeAgentSelectable(agent)')
const end = source.indexOf('function upgradeCurrentPageSelected()', start)
const select = new Function('upgradeAgentRelation', 'state', `${source.slice(start,end)};return upgradeAgentSelectable`)(a => a.relation, x => String(x || '').toLowerCase())
test('SSH upgrades accept offline and degraded agents but reject busy and non-upgrade targets', () => {
  for (const heartbeat of ['OFFLINE', 'DEGRADED', 'ONLINE']) assert.equal(select({relation:'upgrade', install_state:'error', heartbeat_state:heartbeat}), true)
  assert.equal(select({relation:'upgrade', install_state:'installing'}), false)
  assert.equal(select({relation:'upgrade', install_state:'-'}), false)
  for (const relation of ['current','downgrade','unknown']) assert.equal(select({relation,install_state:'online'}), false)
})
