import test from 'node:test'
import assert from 'node:assert/strict'
import { createMySQLParameterCatalog, mysqlParameterCategory, mysqlParameterIsDynamic } from './mysql-parameter-catalog.js'

test('shares parameter apply-mode metadata across management surfaces', () => {
  assert.equal(mysqlParameterIsDynamic('MAX_CONNECTIONS'), true)
  assert.equal(mysqlParameterIsDynamic('server_id'), false)
  assert.equal(mysqlParameterIsDynamic('skip_name_resolve'), false)
  assert.equal(mysqlParameterCategory('innodb_buffer_pool_size'), 'InnoDB')
})

test('merges built-in and package-specific parameters into one stable catalog', () => {
  const catalog = createMySQLParameterCatalog({
    groups: [{ name: '基础参数', fields: [{ key: 'max_connections', default: '500' }] }],
    packages: [{ runtime_parameter_groups: [{ name: 'MySQL 8.4', fields: [{ key: 'temptable_max_ram', description: 'TempTable 内存上限' }] }] }]
  })
  const byName = new Map(catalog.map(item => [item.name, item]))
  assert.equal(byName.get('max_connections').dynamic, true)
  assert.equal(byName.get('max_connections').defaultValue, '500')
  assert.equal(byName.get('server_id').dynamic, false)
  assert.equal(byName.get('temptable_max_ram').description, 'TempTable 内存上限')
  assert.equal(new Set(catalog.map(item => item.name)).size, catalog.length)
})
