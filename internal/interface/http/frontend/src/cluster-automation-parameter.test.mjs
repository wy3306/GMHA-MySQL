import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'

const source = readFileSync(new URL('./main.js', import.meta.url), 'utf8')

test('multi-cluster parameters use the shared catalog and batch parameter API', () => {
  assert.match(source, /createMySQLParameterCatalog/)
  assert.match(source, /automationFilteredParameters/)
  assert.match(source, /api\('\/tasks\/mysql-parameters'/)
  assert.match(source, /restart_targets:\s*requiresRestart\s*\?\s*targets\s*:\s*\[\]/)
  assert.doesNotMatch(source, /参数名称<input v-model\.trim="automationForm\.parameter_name"/)
})

test('multi-cluster parameter workflow prevents unsafe server_id fan-out', () => {
  assert.match(source, /definition\.name === 'server_id' && automationParameterTargets\.value\.length !== 1/)
  assert.match(source, /server_id 必须逐实例设置唯一值/)
  assert.match(source, /parameter_restart_acknowledged/)
})

test('multi-cluster workflows expose single-cluster safety and result detail', () => {
  assert.match(source, /automationPrivilegeOptions/)
  assert.match(source, /逐项检查明细/)
  assert.match(source, /automationInspectionChecks/)
  assert.match(source, /automationUpgradePlans/)
  assert.match(source, /生成逐集群升级计划/)
  assert.match(source, /逐集群实时升级计划/)
})

test('automation privilege compatibility does not read a later binding during setup', () => {
  const compatibilityBlock = source.match(/const automationPrivilegeOptions = computed\(\(\) => \{[\s\S]*?\n    \}\)/)?.[0] || ''
  assert.match(compatibilityBlock, /automationParameterTargetInstances\(\)/)
  assert.doesNotMatch(compatibilityBlock, /automationParameterVersions\.value/)
})
