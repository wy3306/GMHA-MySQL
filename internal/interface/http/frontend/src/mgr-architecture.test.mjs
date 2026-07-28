import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { isValidMGRMemberCount, mgrMemberRequirement } from './mgr-architecture.js'

const mainSource = readFileSync(new URL('./main.js', import.meta.url), 'utf8')

test('MGR draft accepts only 3 to 9 odd members', () => {
  for (const count of [3, 5, 7, 9]) assert.equal(isValidMGRMemberCount(count), true)
  for (const count of [0, 1, 2, 4, 6, 8, 10]) assert.equal(isValidMGRMemberCount(count), false)
})

test('MGR member blocker explains the exact next action', () => {
  assert.match(mgrMemberRequirement(2), /还需添加 1 个实例/)
  assert.match(mgrMemberRequirement(4), /再添加 1 个实例或移除 1 个/)
  assert.match(mgrMemberRequirement(10), /先移除 1 个/)
  assert.equal(mgrMemberRequirement(3), '')
})

test('MGR plan can repair missing tools and explains automatic server_id repair', () => {
  assert.match(mainSource, /一键补齐 MGR 制品/)
  assert.match(mainSource, /计划自动修复/)
  assert.match(mainSource, /首次构建 MGR 时平台会在冻结业务后自动调整为唯一值/)
})
