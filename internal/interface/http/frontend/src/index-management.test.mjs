import test from 'node:test'
import assert from 'node:assert/strict'
import { analyzeRedundantIndexes, formatBytes, parseIndexTaskDetail } from './index-management.js'

test('parses index task rows and byte estimates', () => {
  const rows = parseIndexTaskDetail({ events: [{ content: 'noise\nGMHA_MYSQL_INDEX\tapp\torders\tidx_status\tBTREE\tNO\tstatus,created_at\t32768\t1000\t65536\t49152' }] })
  assert.equal(rows.length, 1)
  assert.deepEqual(rows[0].columns, ['status', 'created_at'])
  assert.equal(rows[0].bytes, 32768)
  assert.equal(formatBytes(rows[0].bytes), '32.0 KB')
})

test('marks duplicate and left-prefix non-unique indexes but protects unique semantics', () => {
  const rows = analyzeRedundantIndexes([
    { schema: 'app', table: 'orders', name: 'idx_status', type: 'BTREE', unique: false, columns: ['status'] },
    { schema: 'app', table: 'orders', name: 'idx_status_created', type: 'BTREE', unique: false, columns: ['status', 'created_at'] },
    { schema: 'app', table: 'orders', name: 'uk_status', type: 'BTREE', unique: true, columns: ['status'] },
    { schema: 'app', table: 'orders', name: 'uk_status_created', type: 'BTREE', unique: true, columns: ['status', 'created_at'] }
  ])
  assert.equal(rows.find(item => item.name === 'idx_status').redundant, true)
  assert.equal(rows.find(item => item.name === 'idx_status').coveredBy, 'uk_status')
  assert.equal(rows.find(item => item.name === 'uk_status').redundant, false)
  assert.equal(rows.find(item => item.name === 'uk_status_created').redundant, false)
})

test('keeps fulltext and btree coverage separate', () => {
  const rows = analyzeRedundantIndexes([
    { schema: 'app', table: 'docs', name: 'ft_body', type: 'FULLTEXT', unique: false, columns: ['body'] },
    { schema: 'app', table: 'docs', name: 'idx_body', type: 'BTREE', unique: false, columns: ['body', 'created_at'] }
  ])
  assert.equal(rows[0].redundant, false)
})
