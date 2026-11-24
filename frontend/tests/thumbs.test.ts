import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  THUMB_CACHE_LIMIT,
  THUMB_EAGER_MAX_BYTES,
  ThumbQueue,
  canLoadThumb,
  needsExplicitPreview,
  pickEagerOrders,
} from '../src/utils/thumbs.ts'

const image = (order: number, extra: Partial<{ status: string; mime_type: string; file_size: number }> = {}) => ({
  sort_order: order,
  short_code: `code-${order}`,
  mime_type: 'image/jpeg',
  file_size: 800_000,
  status: 'stored',
  ...extra,
})

test('只有已存入频道、且不需要明确预览的项才自动加载', () => {
  assert.equal(canLoadThumb({ status: 'stored' }), true)
  assert.equal(canLoadThumb({ status: 'uploaded' }), false)
  assert.equal(canLoadThumb({ status: 'failed' }), false)

  assert.equal(needsExplicitPreview({ file_size: THUMB_EAGER_MAX_BYTES, mime_type: 'image/webp' }), false)
  assert.equal(needsExplicitPreview({ file_size: THUMB_EAGER_MAX_BYTES + 1, mime_type: 'image/webp' }), true)
  assert.equal(needsExplicitPreview({ file_size: 1000, mime_type: 'video/mp4' }), true)
})

test('可视区只挑选可自动加载的项', () => {
  const images = [
    image(1),
    image(2, { status: 'uploaded' }),
    image(3, { file_size: THUMB_EAGER_MAX_BYTES + 1 }),
    image(4, { mime_type: 'video/mp4' }),
    image(5),
  ]
  assert.deepEqual(pickEagerOrders([1, 2, 3, 4, 5, 9], images), [1, 5])
})

test('同一时间最多一个回源请求，重复请求被合并', async () => {
  let active = 0
  let peak = 0
  const started: number[] = []
  const queue = new ThumbQueue(
    async (order) => {
      active += 1
      peak = Math.max(peak, active)
      started.push(order)
      await new Promise((resolve) => setTimeout(resolve, 5))
      active -= 1
      return new Blob([String(order)])
    },
    { createUrl: () => `blob:${Math.random()}`, revokeUrl: () => {} },
  )
  for (const order of [3, 1, 2]) {
    queue.request(order)
    queue.request(order)
  }
  await new Promise((resolve) => setTimeout(resolve, 120))
  assert.equal(peak, 1, '并发必须为 1')
  assert.deepEqual(started, [3, 1, 2], '按请求顺序逐张处理')
  assert.equal(queue.size, 3)
  queue.dispose()
})

test('缓存超过上限时淘汰最早的一张并释放地址', async () => {
  const revoked: string[] = []
  let counter = 0
  const created: string[] = []
  const queue = new ThumbQueue(
    async () => new Blob(['x']),
    {
      limit: 2,
      createUrl: () => {
        counter += 1
        const url = `blob:thumb-${counter}`
        created.push(url)
        return url
      },
      revokeUrl: (url) => revoked.push(url),
    },
  )
  for (const order of [1, 2, 3]) queue.request(order)
  await new Promise((resolve) => setTimeout(resolve, 80))
  assert.equal(queue.size, 2)
  assert.equal(queue.has(1), false, '最早的一张应被淘汰')
  assert.equal(queue.has(3), true)
  assert.deepEqual(revoked, [created[0]])
  queue.dispose()
})

test('onStore 在每张入缓存后回调，便于界面刷新小图', async () => {
  const stored: number[] = []
  const queue = new ThumbQueue(async () => new Blob(['x']), {
    createUrl: () => 'blob:ok',
    revokeUrl: () => {},
    onStore: (order) => stored.push(order),
  })
  assert.equal(THUMB_CACHE_LIMIT, 16)
  queue.request(2)
  queue.request(4)
  await new Promise((resolve) => setTimeout(resolve, 60))
  assert.deepEqual(stored, [2, 4])
  assert.equal(queue.loadedCount, 2)
  assert.equal(queue.get(2), 'blob:ok')
  queue.dispose()
})

test('dispose 取消在途请求并清空缓存', async () => {
  let aborted = false
  const queue = new ThumbQueue(
    async (_order, signal) => {
      signal.addEventListener('abort', () => {
        aborted = true
      })
      await new Promise((resolve) => setTimeout(resolve, 50))
      return new Blob(['x'])
    },
    { createUrl: () => 'blob:x', revokeUrl: () => {} },
  )
  queue.request(1)
  await new Promise((resolve) => setTimeout(resolve, 5))
  queue.dispose()
  await new Promise((resolve) => setTimeout(resolve, 60))
  assert.equal(aborted, true, '在途请求应被取消')
  assert.equal(queue.size, 0)
  // 关闭后再请求不应重新发起。
  queue.request(2)
  await new Promise((resolve) => setTimeout(resolve, 20))
  assert.equal(queue.size, 0)
})
