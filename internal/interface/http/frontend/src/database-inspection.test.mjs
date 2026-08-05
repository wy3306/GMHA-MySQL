import test from 'node:test'
import assert from 'node:assert/strict'
import { inspectionHistoryDataURL, inspectionHistoryRecordFromTasks } from './database-inspection.js'

test('builds an Excel export URL for a retained inspection record', () => {
  assert.equal(
    inspectionHistoryDataURL({ task_ids: ['inspection-1', 'inspection-2'] }),
    '/api/v1/tasks/database-inspection/data?task_ids=inspection-1%2Cinspection-2'
  )
})

test('rebuilds retained inspection history from legacy task detail APIs', () => {
  const record = inspectionHistoryRecordFromTasks(
    {
      ID: 'batch-1', Type: 'batch_operation', Status: 'success', ProgressPercent: 100,
      SpecJSON: { operation: 'cluster_automation', target: 'Demo01' }, CreatedAt: '2026-08-05T10:00:00Z'
    },
    [{ ID: 'inspection-1', Type: 'exec', SpecJSON: { operation: 'database_deep_inspection' } }],
    { ready: true, failed: 0, targets: [{ cluster: 'Demo01', status: 'success', score: 92, passed: 10, warnings: 2, critical: 1 }] }
  )
  assert.equal(record.id, 'batch-1')
  assert.equal(record.level, 'deep')
  assert.deepEqual(record.task_ids, ['inspection-1'])
  assert.equal(record.average_score, 92)
  assert.equal(record.warnings, 2)
  assert.deepEqual(record.clusters, ['Demo01'])
})
