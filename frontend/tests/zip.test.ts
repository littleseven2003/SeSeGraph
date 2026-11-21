import { test } from 'node:test'
import assert from 'node:assert/strict'
import { zipSync, strToU8 } from 'fflate'
import { unpackZip } from '../src/utils/zip.ts'
import { coverSize, toDataUrl } from '../src/utils/cover.ts'

function buildArchive(entries: Record<string, Uint8Array | string>): Uint8Array {
  const payload: Record<string, Uint8Array> = {}
  for (const [name, value] of Object.entries(entries)) {
    payload[name] = typeof value === 'string' ? strToU8(value) : value
  }
  return zipSync(payload)
}

test('解压后按自然排序返回图片', () => {
  const archive = buildArchive({
    'pack/10.jpg': new Uint8Array([1]),
    'pack/2.jpg': new Uint8Array([2]),
    'pack/1.jpg': new Uint8Array([3]),
  })
  const result = unpackZip(archive)
  assert.deepEqual(result.images.map((image) => image.name), ['pack/1.jpg', 'pack/2.jpg', 'pack/10.jpg'])
  assert.equal(result.metaText, '')
  assert.equal(result.skipped, 0)
})

test('识别 info.txt 并过滤非图片文件', () => {
  const archive = buildArchive({
    '1.jpg': new Uint8Array([1]),
    'INFO.TXT': '标题：测试\n标签：a,b',
    'notes.md': '# 说明',
    '2.bmp': new Uint8Array([2]),
  })
  const result = unpackZip(archive)
  assert.deepEqual(result.images.map((image) => image.name), ['1.jpg'])
  assert.equal(result.metaText, '标题：测试\n标签：a,b')
  assert.equal(result.skipped, 2)
})

test('跳过目录条目与系统附带文件', () => {
  const archive = buildArchive({
    'pack/': new Uint8Array(),
    'pack/1.png': new Uint8Array([1]),
    '__MACOSX/._1.png': new Uint8Array([0]),
    'pack/.DS_Store': new Uint8Array([0]),
  })
  const result = unpackZip(archive)
  assert.deepEqual(result.images.map((image) => image.name), ['pack/1.png'])
})

test('自定义元信息文件名', () => {
  const archive = buildArchive({ 'a.jpg': new Uint8Array([1]), 'custom.txt': '自定义说明' })
  assert.equal(unpackZip(archive, ['custom.txt']).metaText, '自定义说明')
  assert.equal(unpackZip(archive).metaText, '')
})

test('封面按 400px 宽度等比缩放，小图不放大', () => {
  assert.deepEqual(coverSize(1200, 800), { width: 400, height: 267 })
  assert.deepEqual(coverSize(300, 200), { width: 300, height: 200 })
  assert.deepEqual(coverSize(0, 0), { width: 400, height: 400 })
})

test('二进制转 data URL 可用于请求体', () => {
  assert.equal(toDataUrl(new Uint8Array([104, 105]), 'image/webp'), 'data:image/webp;base64,aGk=')
})
