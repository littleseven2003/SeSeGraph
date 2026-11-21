import { test } from 'node:test'
import assert from 'node:assert/strict'
import { checkFile, baseName, extensionOf, isImageName, mimeForName, limitForMode, MAX_IMAGE_COUNT } from '../src/utils/image.ts'

test('支持 jpg/jpeg/png/webp/gif', () => {
  for (const name of ['a.jpg', 'b.JPEG', 'c.png', 'd.webp', 'e.gif']) {
    assert.equal(isImageName(name), true, name)
  }
})

test('过滤非图片与无扩展名文件', () => {
  for (const name of ['info.txt', 'cover.bmp', 'noext', 'archive.zip']) {
    assert.equal(isImageName(name), false, name)
  }
})

test('扩展名与基础名解析兼容目录结构', () => {
  assert.equal(baseName('pack/sub/01.png'), '01.png')
  assert.equal(extensionOf('pack/sub/01.PNG'), 'png')
  assert.equal(extensionOf('noext'), '')
  assert.equal(mimeForName('x.webp'), 'image/webp')
})

test('画质模式决定单张上限', () => {
  assert.equal(limitForMode('photo'), 10 * 1024 * 1024)
  assert.equal(limitForMode('document'), 20 * 1024 * 1024)
})

test('超过当前模式上限时给出提示', () => {
  assert.equal(checkFile({ name: 'a.jpg', size: 11 * 1024 * 1024 }, 'photo', 1), 'a.jpg 超过 10MB 上限')
  assert.equal(checkFile({ name: 'a.jpg', size: 11 * 1024 * 1024 }, 'document', 1), null)
  assert.equal(checkFile({ name: 'a.txt', size: 1 }, 'photo', 1), 'a.txt 不是支持的图片格式')
  assert.equal(checkFile({ name: 'a.jpg', size: 1 }, 'photo', MAX_IMAGE_COUNT + 1), `单个帖子最多 ${MAX_IMAGE_COUNT} 张图片`)
})

test('全原图批次在 document 模式下全部通过，photo 模式全部被拒', () => {
  const batch = [
    { name: '1.jpg', size: 12 * 1024 * 1024 },
    { name: '2.jpg', size: 14 * 1024 * 1024 },
    { name: '3.jpg', size: 18 * 1024 * 1024 },
  ]
  assert.deepEqual(
    batch.map((file, index) => checkFile(file, 'document', index + 1)),
    [null, null, null],
  )
  assert.ok(batch.every((file, index) => checkFile(file, 'photo', index + 1) !== null))
})

test('切换到原图模式后 10–20MiB 的图片不再被拒', () => {
  const file = { name: 'big.jpg', size: 15 * 1024 * 1024 }
  assert.match(checkFile(file, 'photo', 1) ?? '', /10MB/)
  assert.equal(checkFile(file, 'document', 1), null)
})
