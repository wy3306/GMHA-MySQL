import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const source = readFileSync(new URL('./main.js', import.meta.url), 'utf8')
const styles = readFileSync(new URL('./agent-resources.css', import.meta.url), 'utf8')

test('agent release panel uses structured responsive layout', () => {
  assert.match(source, /class="agent-release-copy"/)
  assert.match(source, /class="agent-release-actions"/)
  assert.match(source, /agent-release-latest/)
  assert.match(styles, /\.agent-release-publish\{display:grid/)
  assert.match(styles, /@media\(max-width:800px\).*\.agent-release-publish\{grid-template-columns:1fr\}/s)
})

test('source-less deployments provide an artifact upload action', () => {
  assert.match(source, /上传预构建制品/)
  assert.match(source, /当前 Manager 未检测到源码与 Go 工具链/)
})
