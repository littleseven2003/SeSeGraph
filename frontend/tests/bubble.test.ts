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
  assert.deepEqual(position, { side: 'left', right: 1440 - 800 + 12, top: 300 })
})

test('靠近上下边缘时钳在视口安全区内', () => {
  const top = bubblePosition({ left: 800, top: 2, height: 44 }, metrics)
  assert.equal(top?.top, 16)
  const bottom = bubblePosition({ left: 800, top: 880, height: 44 }, metrics)
  assert.equal(bottom?.top, 900 - 264 - 16)
})

test('窄屏改为视口内居中预览，不再直接放弃', () => {
  const position = bubblePosition({ left: 20, top: 300, height: 44 }, { ...metrics, viewportWidth: 390 })
  assert.deepEqual(position, { side: 'center', left: 75, top: 300, width: 240 })
})

test('视口窄到放不下时气泡按可用宽度收缩并留边距', () => {
  const position = bubblePosition({ left: 12, top: 300, height: 44 }, { ...metrics, viewportWidth: 260 })
  assert.equal(position?.side, 'center')
  assert.equal(position?.width, 228)
  assert.equal(position?.left, 16)
})

test('桌面左侧空间不足时同样回退到居中，不压住抽屉', () => {
  const position = bubblePosition({ left: 200, top: 300, height: 44 }, metrics)
  assert.equal(position?.side, 'center')
  assert.equal(position?.left, Math.round((1440 - 240) / 2))
})

test('视口尺寸不可用时不定位', () => {
  assert.equal(bubblePosition({ left: 800, top: 300, height: 44 }, { ...metrics, viewportWidth: 0 }), null)
  assert.equal(bubblePosition({ left: 800, top: 300, height: 44 }, { ...metrics, viewportHeight: 0 }), null)
})
