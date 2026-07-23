import test from 'node:test'
import assert from 'node:assert/strict'
import { createParameterWorkbook } from './parameter-export.js'

const unzipStoredFiles = bytes => {
  const files = new Map()
  const decoder = new TextDecoder()
  let offset = 0
  while (new DataView(bytes.buffer, bytes.byteOffset + offset, 4).getUint32(0, true) === 0x04034b50) {
    const view = new DataView(bytes.buffer, bytes.byteOffset + offset, 30)
    const size = view.getUint32(18, true)
    const nameLength = view.getUint16(26, true)
    const extraLength = view.getUint16(28, true)
    const nameStart = offset + 30
    const dataStart = nameStart + nameLength + extraLength
    const name = decoder.decode(bytes.slice(nameStart, nameStart + nameLength))
    files.set(name, decoder.decode(bytes.slice(dataStart, dataStart + size)))
    offset = dataStart + size
  }
  return files
}

test('creates a valid Excel workbook with parameter details and a safe filename', () => {
  const result = createParameterWorkbook({
    parameters: [
      { name: 'max_connections', category: '连接与缓存', value: '500<&', collected: true, compatible: true, dynamic: true },
      { name: 'innodb_redo_log_capacity', category: 'InnoDB', value: '', collected: false, compatible: false, dynamic: false }
    ],
    changes: [{ name: 'max_connections', action: 'update', value: '800' }],
    cluster: '生产/集群',
    instance: 'db01:3306',
    version: '8.0.36',
    exportedAt: new Date(2026, 6, 23, 9, 8, 7)
  })

  assert.equal(result.mimeType, 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet')
  assert.equal(result.filename, '生产_集群_db01_3306_运行参数_20260723-090807.xlsx')
  assert.equal(new DataView(result.bytes.buffer).getUint32(0, true), 0x04034b50)

  const files = unzipStoredFiles(result.bytes)
  assert.deepEqual([...files.keys()], [
    '[Content_Types].xml',
    '_rels/.rels',
    'xl/workbook.xml',
    'xl/_rels/workbook.xml.rels',
    'xl/styles.xml',
    'xl/worksheets/sheet1.xml'
  ])
  const sheet = files.get('xl/worksheets/sheet1.xml')
  assert.match(sheet, /<autoFilter ref="A1:L3"\/>/)
  assert.match(sheet, /max_connections/)
  assert.match(sheet, /500&lt;&amp;/)
  assert.match(sheet, />800</)
  assert.match(sheet, /版本不兼容/)
})
