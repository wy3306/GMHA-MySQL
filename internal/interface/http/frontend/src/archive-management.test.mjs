import test from 'node:test'
import assert from 'node:assert/strict'
import { archiveConfirmation, archiveRequestFingerprint, parseArchiveTaskDetail } from './archive-management.js'

test('builds an exact source-to-destination confirmation', () => {
  assert.equal(archiveConfirmation({
    source_schema: ' app ', source_table: 'orders',
    destination_schema: 'archive', destination_table: 'orders_2026 '
  }), 'app.orders->archive.orders_2026')
})

test('archive preview fingerprint changes when a safety-relevant input changes', () => {
  const base = {
    target: '10.0.0.1:3306', source_schema: 'app', source_table: 'orders',
    destination_schema: 'archive', destination_table: 'orders_2026',
    where: 'created_at < NOW() - INTERVAL 180 DAY', index: 'PRIMARY',
    batch_size: 1000, sleep_seconds: 1, run_time_seconds: 3600, delete_source: true
  }
  assert.notEqual(archiveRequestFingerprint(base), archiveRequestFingerprint({ ...base, where: 'created_at < NOW() - INTERVAL 365 DAY' }))
  assert.notEqual(archiveRequestFingerprint(base), archiveRequestFingerprint({ ...base, delete_source: false }))
})

test('parses archive precheck and PT execution statistics', () => {
  const result = parseArchiveTaskDetail({ events: [
    { content: 'GMHA_ARCHIVE_SOURCE\tapp\torders\t2500\tNO\nGMHA_ARCHIVE_TABLE\tapp\torders\tInnoDB\t10000\t1048576\t524288\t1' },
    { content: 'GMHA_ARCHIVE_DESTINATION\tarchive\torders_2026\t0\nGMHA_ARCHIVE_DESTINATION_MISSING\tarchive\torders_2026\twill create' },
    { content: 'SELECT 2500\nINSERT 2500\nDELETE 2500\nGMHA_ARCHIVE_REMAINING\tapp\torders\t0\nGMHA_ARCHIVE_DESTINATION_ROWS\tarchive\torders_2026\t2500' }
  ] })
  assert.equal(result.eligibleRows, 2500)
  assert.equal(result.eligibleRowsCapped, false)
  assert.equal(result.source.engine, 'InnoDB')
  assert.equal(result.source.primaryKeys, 1)
  assert.equal(result.destinationExists, false)
  assert.equal(result.destinationMissing, true)
  assert.equal(result.inserted, 2500)
  assert.equal(result.deleted, 2500)
  assert.equal(result.remainingRows, 0)
  assert.equal(result.destinationRows, 2500)
})
