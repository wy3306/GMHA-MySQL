import test from 'node:test'
import assert from 'node:assert/strict'
import { buildOnlineDDLFingerprint, parseOnlineDDLTaskDetail } from './online-ddl-management.js'

test('online DDL fingerprint changes when safety-relevant input changes', () => {
  const base = {
    target: '10.0.0.8:3306', schema: 'app', table: 'orders',
    alter: 'ADD COLUMN status varchar(32) NULL', purpose: '业务字段', impact: '复制原表',
    max_load_threads_running: 25, critical_threads_running: 50, max_lag_seconds: 10,
    chunk_time_seconds: 0.5, check_interval_seconds: 1, alter_foreign_keys_method: 'auto'
  }
  assert.equal(
    buildOnlineDDLFingerprint(base),
    buildOnlineDDLFingerprint({ ...base, alter: '  ADD   COLUMN status varchar(32) NULL  ' })
  )
  assert.notEqual(
    buildOnlineDDLFingerprint(base),
    buildOnlineDDLFingerprint({ ...base, max_load_threads_running: 20 })
  )
  assert.notEqual(
    buildOnlineDDLFingerprint(base),
    buildOnlineDDLFingerprint({ ...base, target: '10.0.0.9:3306' })
  )
})

test('parses PT precheck and verification markers from task events', () => {
  const detail = {
    events: [
      { content: "pt-online-schema-change 3.7.1\nGMHA_ONLINE_DDL_TARGET\tapp\torders\tInnoDB\t120000\t1048576\t524288\t2\t0\t1\t新增字段\t复制原表\nGMHA_ONLINE_DDL_LOAD\t8.0.44\tROW\t0\t7\t2" },
      { content: 'GMHA_ONLINE_DDL_VERIFIED\tapp\torders\tInnoDB\t120100\t1179648\t524288' }
    ]
  }
  const parsed = parseOnlineDDLTaskDetail(detail)
  assert.equal(parsed.target.schema, 'app')
  assert.equal(parsed.target.uniqueIndexes, 2)
  assert.equal(parsed.target.foreignKeys, 1)
  assert.equal(parsed.load.threadsRunning, 7)
  assert.equal(parsed.load.transactions, 2)
  assert.equal(parsed.verified.dataBytes, 1179648)
})
