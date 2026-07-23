import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const source = readFileSync(new URL('./manager-console.js', import.meta.url), 'utf8')
const styles = readFileSync(new URL('./manager-console.css', import.meta.url), 'utf8')

test('Manager console uses stable subpages instead of mixed-height cards', () => {
  for (const view of ['overview', 'database', 'ha', 'maintenance']) {
    assert.match(source, new RegExp(`setView\\('${view}'\\)`))
  }
  assert.match(styles, /\.manager-database-editor\{display:flex;height:540px/)
  assert.doesNotMatch(styles, /linear-gradient\(125deg,#10233f/)
})

test('Manager VIP has one workspace with scanned interface selection', () => {
  assert.equal((source.match(/class="panel manager-vip-workspace"/g) || []).length, 1)
  assert.match(source, /\/manager\/ha\/interfaces\?/)
  assert.match(source, /v-model="vipSelectedInterface"/)
  assert.match(source, /item\.recommended \? ' · 推荐'/)
  assert.doesNotMatch(source, /VIP 漂移至此/)
})
