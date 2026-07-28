import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const source = readFileSync(new URL('./instance-management.js', import.meta.url), 'utf8')

test('server_id is an instance-scoped restart parameter with guarded input', () => {
  const dynamicSetStart = source.indexOf('const dynamicMySQLParameters = new Set([')
  const dynamicSet = source.slice(dynamicSetStart, source.indexOf('])', dynamicSetStart) + 2)
  assert.match(source, /key:\s*'server_id'[\s\S]{0,120}修改后需重启/)
  assert.match(source, /server_id 必须是 1 到 4294967295 之间的整数/)
  assert.match(source, /server_id 必须逐实例设置唯一值，不能批量写入整个集群/)
  assert.match(source, /next\.name === 'server_id'[\s\S]{0,500}parameterApplyScope\.value = 'instance'/)
  assert.doesNotMatch(dynamicSet, /'server_id'/)
})
