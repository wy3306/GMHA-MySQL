import test from 'node:test'
import assert from 'node:assert/strict'
import { buildChartPoints, binlogProgressPercent, formatDurationMicros } from './binlog-analysis.js'

test('binlog chart points stay inside the chart and preserve bucket order', () => {
  const points = buildChartPoints([
    { insert_rows: 0 },
    { insert_rows: 10 },
    { insert_rows: 5 }
  ], 'insert_rows', 100, 50)
  assert.equal(points, '0.0,50.0 50.0,0.0 100.0,25.0')
})

test('binlog task progress uses completed files and clamps the result', () => {
  assert.equal(binlogProgressPercent({ files_total: 4, files_completed: 1 }), 25)
  assert.equal(binlogProgressPercent({ files_total: 4, files_completed: 9 }), 100)
  assert.equal(binlogProgressPercent({ phase: 'running' }), 8)
  assert.equal(binlogProgressPercent({ phase: 'completed' }), 100)
})

test('historical replication delay keeps useful precision', () => {
  assert.equal(formatDurationMicros(420), '420 μs')
  assert.equal(formatDurationMicros(12500), '12.5 ms')
  assert.equal(formatDurationMicros(2500000), '2.50 s')
  assert.equal(formatDurationMicros(undefined), '—')
})
