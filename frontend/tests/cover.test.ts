import assert from 'node:assert/strict'
import { test } from 'node:test'
import { COVER_DECODE_MAX_BYTES, COVER_DECODE_MAX_WIDTH, coverSize, toDataUrl } from '../src/utils/cover.ts'

test('封面按 400px 宽等比缩放，只缩不放', () => {
  assert.deepEqual(coverSize(2000, 1000), { width: 400, height: 200 })
  assert.deepEqual(coverSize(400, 900), { width: 400, height: 900 })
  assert.deepEqual(coverSize(120, 90), { width: 120, height: 90 })
  assert.deepEqual(coverSize(0, 0), { width: 400, height: 400 })
})

test('大图解码按 1600px 宽封顶，小图不受影响', () => {
  assert.equal(COVER_DECODE_MAX_BYTES, 4_000_000)
  assert.equal(COVER_DECODE_MAX_WIDTH, 1600)
  // 卸码上限要明显大于成品宽度，保留缩小余量。
  assert.ok(COVER_DECODE_MAX_WIDTH > 400 * 2)
})

test('toDataUrl 输出可直接放进 JSON 的 data URL', () => {
  const url = toDataUrl(new Uint8Array([1, 2, 3, 4]), 'image/webp')
  assert.ok(url.startsWith('data:image/webp;base64,'))
  assert.equal(Buffer.from(url.split(',')[1], 'base64').length, 4)
})
