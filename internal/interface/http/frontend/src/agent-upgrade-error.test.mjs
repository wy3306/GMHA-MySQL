import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const source = readFileSync(new URL('./main.js', import.meta.url), 'utf8')
const start = source.indexOf('function errorSummary(message')
const end = source.indexOf('function machineLastError', start)
const errorSummary = new Function('safeLog', `${source.slice(start, end)}; return errorSummary`)(value => String(value || ''))

test('SSH timeout explains online push fallback and network checks', () => {
  const result = errorSummary('请求失败（400）：dial tcp 192.168.31.204:22: i/o timeout')
  assert.match(result, /192\.168\.31\.204:22/)
  assert.match(result, /在线推送/)
  assert.match(result, /防火墙/)
})
