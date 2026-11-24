import assert from 'node:assert/strict'
import { test } from 'node:test'
import { bubblePosition } from '../src/utils/bubble.ts'

const metrics = {
  viewportWidth: 1440,
  viewportHeight: 900,
  bubbleWidth: 240,
  bubbleHeight: 264,
}

test('气泡贴在缩略图左侧，间隔与安全边距固定', () => {
  const position = bubblePosition({ left: 800, top: 300, height: 44 }, metrics)
  assert.deepEqual(position, { right: 1440 - 800 + 12, top: 300 })
})

test('靠近上下边缘时钳在视口安全区内', () => {
  const top = bubblePosition({ left: 800, top: 2, height: 44 }, metrics)
  assert.equal(top?.top, 16)
  const bottom = bubblePosition({ left: 800, top: 880, height: 44 }, metrics)
  assert.equal(bottom?.top, 900 - 264 - 16)
})

test('左侧空间不足或窄屏时不弹，避免被裁切', () => {
  assert.equal(bubblePosition({ left: 200, top: 300, height: 44 }, metrics), null)
  assert.equal(
    bubblePosition({ left: 800, top: 300, height: 44 }, { ...metrics, viewportWidth: 390 }),
    null,
  )
})
