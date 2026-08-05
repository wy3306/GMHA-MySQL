import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'

const source = readFileSync(new URL('./main.js', import.meta.url), 'utf8')

test('task center exposes a filter-independent clear-all action', () => {
  assert.match(source, /清理全部任务记录/)
  assert.match(source, /deleteTaskRecords\('all'\)/)
  assert.match(source, /all:\s*allRecords/)
  assert.match(source, /keyword:\s*allRecords\s*\?\s*''/)
  assert.match(source, /status:\s*allRecords\s*\?\s*'all'/)
  assert.match(source, /type:\s*allRecords\s*\?\s*'all'/)
})

test('task cleanup uses a compact secondary action menu', () => {
  assert.match(source, /class="task-bulk-summary"|\['task-bulk-summary'/)
  assert.match(source, /class="task-cleanup-more"/)
  assert.match(source, /<summary>更多清理/)
  assert.match(source, /清理当前筛选结果/)
})

test('clear-all keeps active tasks and reports the protected count', () => {
  assert.match(source, /仍在等待或执行中的任务会安全保留/)
  assert.match(source, /安全保留 \$\{result\.failed\} 条仍在执行或尚未结束的任务/)
})

test('task cleanup replaces stale success and error feedback', () => {
  assert.match(source, /error\.value = ''\s+notice\.value = ''\s+try \{\s+await api\(`\/tasks\?id=/)
  assert.match(source, /catch \(err\) \{ notice\.value = ''; error\.value = err\.message \}/)
})
