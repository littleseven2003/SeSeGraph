import { test } from 'node:test'
import assert from 'node:assert/strict'
import { zipSync, strToU8 } from 'fflate'
import { unpackZip } from '../src/utils/zip.ts'
import { File } from 'node:buffer'
import { coverSize, toDataUrl } from '../src/utils/cover.ts'
import { unpackArchive } from '../src/utils/zip-client.ts'

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

test('单张超限只跳过该项，不拒掉整个压缩包', () => {
  const archive = buildArchive({
    'big.jpg': new Uint8Array(500),
    'small.jpg': new Uint8Array(1),
  })
  const result = unpackZip(archive, { limits: { maxImageBytes: 100 } })
  assert.ok(result.ok)
  assert.deepEqual(result.images.map((image) => image.name), ['small.jpg'])
  assert.equal(result.skipped, 1)
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

test('说明文件超过独立上限时忽略并给出提示', () => {
  const archive = buildArchive({
    '1.jpg': new Uint8Array([1]),
    'info.txt': '标题：很长的说明'.repeat(20),
  })
  const result = expectOk(unpackZip(archive, { limits: { maxMetaBytes: 32 } }))
  assert.deepEqual(result.images.map((image) => image.name), ['1.jpg'])
  assert.equal(result.metaText, '')
  assert.equal(result.metaName, '')
  assert.match(result.metaError, /info\.txt 超过 .* 上限，已忽略/)
  assert.equal(result.skipped, 1)

  // 未超限时正常读取。
  const small = expectOk(unpackZip(buildArchive({ '1.jpg': new Uint8Array([1]), 'info.txt': '标题：小' })))
  assert.equal(small.metaText, '标题：小')
  assert.equal(small.metaError, '')
})

// crc32 供手工构造的 ZIP 使用，测试只需要标准 CRC-32。
function crc32(bytes: Uint8Array): number {
  let crc = 0xffffffff
  for (const byte of bytes) {
    crc ^= byte
    for (let bit = 0; bit < 8; bit += 1) {
      crc = crc & 1 ? (crc >>> 1) ^ 0xedb88320 : crc >>> 1
    }
  }
  return (crc ^ 0xffffffff) >>> 0
}

// buildDuplicateZip 构造允许同名条目的 ZIP（zipSync 的入参是对象，无法表达重名）。
function buildDuplicateZip(entries: { name: string; data: Uint8Array }[]): Uint8Array {
  const locals: Uint8Array[] = []
  const centrals: Uint8Array[] = []
  let offset = 0
  for (const entry of entries) {
    const name = strToU8(entry.name)
    const size = entry.data.length
    const crc = crc32(entry.data)

    const local = new Uint8Array(30 + name.length + size)
    const lv = new DataView(local.buffer)
    lv.setUint32(0, 0x04034b50, true)
    lv.setUint16(4, 20, true)
    lv.setUint32(14, crc, true)
    lv.setUint32(18, size, true)
    lv.setUint32(22, size, true)
    lv.setUint16(26, name.length, true)
    local.set(name, 30)
    local.set(entry.data, 30 + name.length)

    const central = new Uint8Array(46 + name.length)
    const cv = new DataView(central.buffer)
    cv.setUint32(0, 0x02014b50, true)
    cv.setUint16(4, 20, true)
    cv.setUint16(6, 20, true)
    cv.setUint32(16, crc, true)
    cv.setUint32(20, size, true)
    cv.setUint32(24, size, true)
    cv.setUint16(28, name.length, true)
    cv.setUint32(42, offset, true)
    central.set(name, 46)

    locals.push(local)
    centrals.push(central)
    offset += local.length
  }

  const directorySize = centrals.reduce((sum, item) => sum + item.length, 0)
  const end = new Uint8Array(22)
  const ev = new DataView(end.buffer)
  ev.setUint32(0, 0x06054b50, true)
  ev.setUint16(8, entries.length, true)
  ev.setUint16(10, entries.length, true)
  ev.setUint32(12, directorySize, true)
  ev.setUint32(16, offset, true)

  const out = new Uint8Array(offset + directorySize + end.length)
  let position = 0
  for (const part of locals) {
    out.set(part, position)
    position += part.length
  }
  for (const part of centrals) {
    out.set(part, position)
    position += part.length
  }
  out.set(end, position)
  return out
}

test('同名条目不能绕过说明文件大小上限', () => {
  const small = strToU8('标题：小')
  const large = strToU8('说明'.repeat(100))
  const archive = buildDuplicateZip([
    { name: 'a.jpg', data: new Uint8Array([1]) },
    { name: 'info.txt', data: small },
    { name: 'info.txt', data: large },
  ])

  const result = expectOk(unpackZip(archive))
  assert.deepEqual(result.images.map((image) => image.name), ['a.jpg'])
  // 只展开第一遍登记的那一份，第二份同名的大文件不会被解开。
  assert.equal(result.metaText, '标题：小')
  assert.equal(result.metaError, '')
  assert.equal(result.skipped, 1)

  // 上限很小时同名大文件同样被拦下。
  const limited = expectOk(unpackZip(archive, { limits: { maxMetaBytes: 12 } }))
  assert.deepEqual(limited.images.map((image) => image.name), ['a.jpg'])
  assert.equal(limited.metaText, '标题：小')
})

test('解压入口在没有 Worker 的环境回落逐项展开', async () => {
  const archive = buildArchive({
    'pack/10.jpg': new Uint8Array([10]),
    'pack/2.jpg': new Uint8Array([2]),
    'info.txt': '标题：回落路径',
  })
  const result = await unpackArchive(new File([archive], 'pack.zip'))
  assert.ok(result.ok)
  assert.deepEqual(result.images.map((image) => image.name), ['pack/2.jpg', 'pack/10.jpg'])
  assert.equal(result.metaText, '标题：回落路径')
  assert.equal(result.metaName, 'info.txt')
})

test('解压入口沿用同一套数量与单项限制', async () => {
  const entries: Record<string, Uint8Array> = {}
  for (let index = 1; index <= 4; index += 1) entries[`${index}.jpg`] = new Uint8Array([index])
  const overCount = await unpackArchive(new File([buildArchive(entries)], 'many.zip'), { limits: { maxImages: 3 } })
  assert.ok(!overCount.ok)
  assert.match(overCount.error, /超过 3 张/)

  const oversize = await unpackArchive(
    new File([buildArchive({ 'big.jpg': new Uint8Array(500), 'small.jpg': new Uint8Array(1) })], 'size.zip'),
    { limits: { maxImageBytes: 100 } },
  )
  assert.ok(oversize.ok)
  assert.deepEqual(oversize.images.map((image) => image.name), ['small.jpg'])
  assert.equal(oversize.skipped, 1)
})

test('逐项交接：每张图展开后立刻回调，客户端不再累积整包字节', async () => {
  const archive = buildArchive({
    'pack/2.jpg': new Uint8Array([2]),
    'pack/1.jpg': new Uint8Array([1]),
    'info.txt': '标题：逐项交接',
  })
  const handed: string[] = []
  const result = await unpackArchive(new File([archive], 'pack.zip'), {}, (image) => {
    handed.push(image.name)
  })
  assert.ok(result.ok)
  assert.deepEqual(handed, ['pack/1.jpg', 'pack/2.jpg'])
  // 有回调时不再把整包图片攒进结果，避免内存里同时留两份。
  assert.deepEqual(result.images, [])
  assert.equal(result.metaText, '标题：逐项交接')
})

test('逐项交接仍执行同一套数量与单项限制', async () => {
  const entries: Record<string, Uint8Array> = {}
  for (let index = 1; index <= 4; index += 1) entries[`${index}.jpg`] = new Uint8Array([index])
  const handed: string[] = []
  const result = await unpackArchive(new File([buildArchive(entries)], 'many.zip'), { limits: { maxImages: 3 } }, (image) => {
    handed.push(image.name)
  })
  assert.ok(!result.ok)
  assert.match(result.error, /超过 3 张/)
  assert.deepEqual(handed, [])
})
