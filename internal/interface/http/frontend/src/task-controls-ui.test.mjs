import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const source = await readFile(new URL('./main.js', import.meta.url), 'utf8')
const styles = await readFile(new URL('./enterprise-layout.css', import.meta.url), 'utf8')

test('unavailable task controls remain clickable so users can inspect the reason', () => {
  assert.doesNotMatch(source, /:disabled="!selectedTaskControls\(\)\.(skip|retry|rollback)\?\.allowed/)
  assert.match(source, /查看不可跳过原因/)
  assert.match(source, /查看不可续跑原因/)
  assert.match(source, /查看不可恢复原因/)
})

test('task detail controls and flow nodes use responsive layout containers', () => {
  assert.match(source, /class="task-control-actions"/)
  assert.match(styles, /\.task-control-actions\s*\{[^}]*grid-template-columns:\s*repeat\(3,/s)
  assert.match(styles, /\.task-flow-switcher\s*\{[^}]*grid-template-columns:\s*repeat\(auto-fit,/s)
  assert.match(styles, /@media \(max-width: 760px\)[\s\S]*?\.task-control-actions\s*\{\s*grid-template-columns:\s*1fr;/)
})
