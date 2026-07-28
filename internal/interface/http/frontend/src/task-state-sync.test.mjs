import test from 'node:test'
import assert from 'node:assert/strict'
import { mergeTaskSummary } from './task-state-sync.js'

test('task detail terminal state replaces the stale list summary', () => {
  const items = [
    { ID: 'task-install', Status: 'failed', ProgressPercent: 80, CurrentStep: 'install_pt_tools' },
    { ID: 'task-other', Status: 'running', ProgressPercent: 50 }
  ]
  const detail = {
    task: {
      ID: 'task-install',
      Status: 'success',
      ProgressPercent: 100,
      CurrentStep: 'setup_agent_collect_config',
      FinishedAt: '2026-07-28T07:40:48Z'
    }
  }

  const updated = mergeTaskSummary(items, detail)

  assert.deepEqual(updated[0], {
    ID: 'task-install',
    Status: 'success',
    ProgressPercent: 100,
    CurrentStep: 'setup_agent_collect_config',
    FinishedAt: '2026-07-28T07:40:48Z'
  })
  assert.equal(updated[1], items[1])
})
