import test from 'node:test'
import assert from 'node:assert/strict'
import { executionPlanAccessTone, executionPlanColumnLabel, summarizeExecutionPlan } from './execution-plan.js'

test('summarizes index usage, estimated rows and full scans', () => {
  const summary = summarizeExecutionPlan([
    { id: 1, table: 'customers', type: 'const', key: 'PRIMARY', rows: 1 },
    { id: 1, table: 'orders', type: 'ref', key: 'idx_customer', rows: 12 },
    { id: 1, table: 'order_items', type: 'ALL', key: null, rows: 5000 }
  ])
  assert.deepEqual(summary, { steps: 3, estimatedRows: 5013, indexSteps: 2, scanSteps: 1 })
})

test('classifies access methods for visual risk treatment', () => {
  assert.equal(executionPlanAccessTone('eq_ref'), 'good')
  assert.equal(executionPlanAccessTone('range'), 'medium')
  assert.equal(executionPlanAccessTone('ALL'), 'risk')
  assert.equal(executionPlanAccessTone(null), 'neutral')
})

test('provides localized labels while preserving unknown columns', () => {
  assert.equal(executionPlanColumnLabel('possible_keys'), '候选索引')
  assert.equal(executionPlanColumnLabel('Extra'), '优化器说明')
  assert.equal(executionPlanColumnLabel('custom_metric'), 'custom_metric')
})
