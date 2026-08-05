import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const source = readFileSync(new URL('./main.js', import.meta.url), 'utf8')

test('offline MGR metadata does not select MGR as the current architecture', () => {
  const start = source.indexOf('function detectCurrentArchitecture')
  const end = source.indexOf('function architectureCurrentNodeRole', start)
  const detectSource = source.slice(start, end)

  assert.match(detectSource, /group_state[^\n]+ONLINE/)
  assert.match(detectSource, /nodes\.some\(onlineMGRPrimary\)/)
  assert.doesNotMatch(detectSource, /nodes\.some\(node => String\(node\.group_role/)
})

test('MGR management visibility recovers from topology, a completed run, or a live MGR probe', () => {
  assert.match(source, /const topologyHasLiveMGR = computed/)
  assert.match(source, /role === 'MGR_PRIMARY'/)
  assert.match(source, /groupState === 'ONLINE'/)
  assert.match(source, /const mgrNavigationAvailable = computed/)
  assert.match(source, /architectureRun\.value\?\.request\?\.architecture === 'mgr_router'/)
  assert.match(source, /mgrManagement\.value\?\.available && mgrManagement\.value\?\.architecture === 'mgr_router'/)
  assert.match(source, /<button v-if="mgrNavigationAvailable"[^>]+>MGR 管理<\/button>/)

  const start = source.indexOf('async function loadMGRManagement')
  const end = source.indexOf('function mgrRouterItems', start)
  const loadSource = source.slice(start, end)
  assert.match(loadSource, /mgrManagement\.value\?\.available && mgrManagement\.value\?\.architecture === 'mgr_router'/)
})

test('MGR workspace opens from the topology snapshot before live AdminAPI refresh finishes', () => {
  const start = source.indexOf('async function openMGRManagement')
  const end = source.indexOf('async function loadMGRManagement', start)
  const openSource = source.slice(start, end)
  assert.match(openSource, /mgrSnapshotFromTopology\(\)/)
  assert.match(openSource, /data\.value\.clusterSection = 'mgr'/)
  assert.match(openSource, /void loadMGRManagement\(true\)/)
  assert.doesNotMatch(openSource, /await loadMGRManagement\(true\)/)
})

test('architecture monitoring retries after a transient page interruption', () => {
  const start = source.indexOf('async function pollArchitectureRun')
  const end = source.indexOf('async function confirmArchitectureForce', start)
  const pollSource = source.slice(start, end)
  assert.match(pollSource, /architecturePollFailures\+\+/)
  assert.match(pollSource, /setTimeout\(pollArchitectureRun, retrySeconds \* 1000\)/)
  assert.match(pollSource, /后台架构任务仍在继续/)
  assert.match(source, /window\.addEventListener\('online', reconcileArchitectureUI\)/)
  assert.match(source, /document\.addEventListener\('visibilitychange', handleArchitectureVisibility\)/)
})
