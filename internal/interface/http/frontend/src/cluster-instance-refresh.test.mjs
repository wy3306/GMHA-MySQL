import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const source = readFileSync(new URL('./main.js', import.meta.url), 'utf8')

test('cluster topology refresh also synchronizes the registered MySQL instance list', () => {
  const refreshStart = source.indexOf('async function refreshClusterTopology')
  const refreshEnd = source.indexOf('function startClusterTopologyAutoRefresh', refreshStart)
  const refreshSource = source.slice(refreshStart, refreshEnd)

  assert.match(refreshSource, /api\('\/mysql\/instances'\)/)
  assert.match(refreshSource, /data\.value\.mysqlInstances = asList\(mysqlInstances, 'instances'\)/)
})

test('cluster instance summary uses synchronized data with a topology fallback', () => {
  assert.match(source, /function clusterMySQLInstanceCount\(item\)/)
  assert.match(source, /Math\.max\(registeredCount, \(clusterTopology\.value\.nodes \|\| \[\]\)\.length\)/)
  assert.match(source, /\{\{ clusterMySQLInstanceCount\(selectedClusterDetail\) \}\}/)
  assert.match(source, /\{\{ clusterAbnormalInstanceCount\(selectedClusterDetail\) \}\}/)
})
