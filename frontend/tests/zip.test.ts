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

function expectOk(result: ReturnType<typeof unpackZip>) {
  assert.ok(result.ok, `解压失败: ${result.ok ? '' : result.error}`)
  return result
}

test('解压后按自然排序返回图片', () => {
  const archive = buildArchive({
    'pack/10.jpg': new Uint8Array([1]),
    'pack/2.jpg': new Uint8Array([2]),
    'pack/1.jpg': new Uint8Array([3]),
  })
  const result = expectOk(unpackZip(archive))
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
  const result = expectOk(unpackZip(archive))
  assert.deepEqual(result.images.map((image) => image.name), ['1.jpg'])
  assert.equal(result.metaText, '标题：测试\n标签：a,b')
  assert.equal(result.metaName, 'INFO.TXT')
  assert.equal(result.skipped, 2)
})

test('跳过目录条目与系统附带文件', () => {
  const archive = buildArchive({
    'pack/': new Uint8Array(),
    'pack/1.png': new Uint8Array([1]),
    '__MACOSX/._1.png': new Uint8Array([0]),
    'pack/.DS_Store': new Uint8Array([0]),
  })
  const result = expectOk(unpackZip(archive))
  assert.deepEqual(result.images.map((image) => image.name), ['pack/1.png'])
})

test('自定义元信息文件名', () => {
  const archive = buildArchive({ 'a.jpg': new Uint8Array([1]), 'custom.txt': '自定义说明' })
  assert.equal(expectOk(unpackZip(archive, { metaNames: ['custom.txt'] })).metaText, '自定义说明')
  assert.equal(expectOk(unpackZip(archive)).metaText, '')
})

test('图片数量超限时在展开前拒绝', () => {
  const entries: Record<string, Uint8Array> = {}
  for (let index = 1; index <= 4; index += 1) {
    entries[`${index}.jpg`] = new Uint8Array([index])
  }
  const archive = buildArchive(entries)
  const result = unpackZip(archive, { limits: { maxImages: 3 } })
  assert.ok(!result.ok)
  assert.match(result.error, /超过 3 张/)
})

test('单张超限时只报告该文件且不展开', () => {
  const archive = buildArchive({
    'big.jpg': new Uint8Array(500),
    'small.jpg': new Uint8Array(1),
  })
  const result = unpackZip(archive, { limits: { maxImageBytes: 100 } })
  assert.ok(!result.ok)
  assert.match(result.error, /big\.jpg 超过/)
})

test('展开总量超限时拒绝', () => {
  const archive = buildArchive({
    '1.jpg': new Uint8Array(80),
    '2.jpg': new Uint8Array(80),
  })
  const result = unpackZip(archive, { limits: { maxTotalBytes: 100 } })
  assert.ok(!result.ok)
  assert.match(result.error, /超过/)
})

test('默认上限下超过 500 张的包在展开前被拒', () => {
  const entries: Record<string, Uint8Array> = {}
  for (let index = 1; index <= 501; index += 1) {
    entries[`${index}.jpg`] = new Uint8Array([index % 256])
  }
  const result = unpackZip(buildArchive(entries))
  assert.ok(!result.ok)
  assert.match(result.error, /超过 500 张/)
})

test('包内没有图片时给出说明', () => {
  const archive = buildArchive({ 'info.txt': '只有说明' })
  const result = unpackZip(archive)
  assert.ok(!result.ok)
  assert.match(result.error, /没有找到受支持的图片/)
})

test('封面按 400px 宽度等比缩放，小图不放大', () => {
  assert.deepEqual(coverSize(1200, 800), { width: 400, height: 267 })
  assert.deepEqual(coverSize(300, 200), { width: 300, height: 200 })
  assert.deepEqual(coverSize(0, 0), { width: 400, height: 400 })
})

test('二进制转 data URL 可用于请求体', () => {
  assert.equal(toDataUrl(new Uint8Array([104, 105]), 'image/webp'), 'data:image/webp;base64,aGk=')
})
