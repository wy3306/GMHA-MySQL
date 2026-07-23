import test from 'node:test'
import assert from 'node:assert/strict'
import { histogramBars, supportsHistograms } from './histogram-management.js'

test('histogram compatibility excludes MySQL 5.7 and non-MySQL families', () => {
  assert.equal(supportsHistograms('5.7.44'), false)
  assert.equal(supportsHistograms('8.0.40'), true)
  assert.equal(supportsHistograms('8.4.10'), true)
  assert.equal(supportsHistograms('9.7.1'), true)
  assert.equal(supportsHistograms('10.11.6-MariaDB'), false)
  assert.equal(supportsHistograms(''), true)
})

test('histogram bars convert cumulative frequencies into relative heights', () => {
  const bars = histogramBars({ raw: { buckets: [['a', 0.2], ['b', 0.5], ['c', 1]] } })
  assert.deepEqual(bars, [40, 60, 100])
  assert.deepEqual(histogramBars({ raw: {} }), [])
})
