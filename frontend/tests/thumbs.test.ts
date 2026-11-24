import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  MediaBudget,
  THUMB_CACHE_LIMIT,
  THUMB_EAGER_MAX_BYTES,
  ThumbQueue,
  canLoadThumb,
  needsExplicitPreview,
  pickEagerOrders,
  withTimeout,
} from '../src/utils/thumbs.ts'
import { isVideoBlob } from '../src/utils/video-frame.ts'

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

test('预览中的项被钉住，不被淘汰也不回收地址', async () => {
  const revoked: string[] = []
  let counter = 0
  const queue = new ThumbQueue(
    async () => new Blob(['x']),
    {
      limit: 2,
      createUrl: () => {
        counter += 1
        return `blob:pin-${counter}`
      },
      revokeUrl: (url) => revoked.push(url),
    },
  )
  queue.request(1)
  queue.request(2)
  await new Promise((resolve) => setTimeout(resolve, 60))
  queue.pin(1)
  queue.request(3)
  await new Promise((resolve) => setTimeout(resolve, 60))
  assert.equal(queue.has(1), true, '钉住的项不能被淘汰')
  assert.equal(queue.has(2), false, '未钉住的最早项照常淘汰')
  assert.deepEqual(revoked, ['blob:pin-2'])
  queue.unpin(1)
  queue.request(4)
  await new Promise((resolve) => setTimeout(resolve, 60))
  assert.equal(queue.has(1), false, '取消钉住后可以正常淘汰')
  queue.dispose()
})

test('原媒体额度同时只放行一个任务，其余按顺序等待', async () => {
  const budget = new MediaBudget(1)
  let active = 0
  let peak = 0
  const order: number[] = []
  const job = (id: number) => async () => {
    active += 1
    peak = Math.max(peak, active)
    order.push(id)
    await new Promise((resolve) => setTimeout(resolve, 5))
    active -= 1
    return id
  }
  const results = await Promise.all([budget.run(job(1)), budget.run(job(2)), budget.run(job(3))])
  assert.deepEqual(results, [1, 2, 3])
  assert.equal(peak, 1, '任何时刻只有一个原始媒体任务')
  assert.equal(budget.running, 0)
  assert.equal(budget.pending, 0)
})

test('缩略图与换封面共用额度时总并发仍为 1', async () => {
  const budget = new MediaBudget(1)
  let active = 0
  let peak = 0
  const held = async () => {
    active += 1
    peak = Math.max(peak, active)
    await new Promise((resolve) => setTimeout(resolve, 10))
    active -= 1
    return new Blob(['x'])
  }
  const queue = new ThumbQueue(async () => await held(), {
    budget,
    createUrl: () => 'blob:shared',
    revokeUrl: () => {},
  })
  queue.request(1)
  queue.request(2)
  const cover = budget.run(held)
  await new Promise((resolve) => setTimeout(resolve, 80))
  await cover
  assert.equal(peak, 1, '缩略图与封面取图不能同时占两条连接')
  assert.equal(queue.size, 2)
  queue.dispose()
})

test('排队中被取消的任务不再占用额度', async () => {
  const budget = new MediaBudget(1)
  const controller = new AbortController()
  const first = budget.run(async () => {
    await new Promise((resolve) => setTimeout(resolve, 20))
    return 'first'
  })
  const cancelled = budget.run(async () => 'second', controller.signal)
  controller.abort()
  await assert.rejects(cancelled, /已取消/)
  assert.equal(await first, 'first')
  assert.equal(budget.running, 0)
  assert.equal(budget.pending, 0)
})

test('已取消的信号不会启动任务', async () => {
  const budget = new MediaBudget(1)
  const controller = new AbortController()
  controller.abort()
  let started = false
  await assert.rejects(
    budget.run(async () => {
      started = true
      return 1
    }, controller.signal),
    /已取消/,
  )
  assert.equal(started, false)
})

test('单张读取失败时回调界面结束加载态，不影响后续项', async () => {
  const failed: number[] = []
  const stored: number[] = []
  const queue = new ThumbQueue(
    async (order) => {
      if (order === 1) throw new Error('读取失败')
      return new Blob(['x'])
    },
    {
      createUrl: () => 'blob:ok',
      revokeUrl: () => {},
      onFail: (order) => failed.push(order),
      onStore: (order) => stored.push(order),
    },
  )
  queue.request(1)
  queue.request(2)
  await new Promise((resolve) => setTimeout(resolve, 60))
  assert.deepEqual(failed, [1])
  assert.deepEqual(stored, [2])
  assert.equal(queue.has(1), false)
  queue.dispose()
})

test('取消导致的失败不回调，避免切帖后弹错误', async () => {
  const failed: number[] = []
  const queue = new ThumbQueue(
    async (_order, signal) => {
      signal.addEventListener('abort', () => {})
      await new Promise((resolve) => setTimeout(resolve, 50))
      return new Blob(['x'])
    },
    { createUrl: () => 'blob:x', revokeUrl: () => {}, onFail: (order) => failed.push(order) },
  )
  queue.request(1)
  await new Promise((resolve) => setTimeout(resolve, 5))
  queue.dispose()
  await new Promise((resolve) => setTimeout(resolve, 60))
  assert.deepEqual(failed, [])
})

test('withTimeout 到点即失败，不会无限等待解码', async () => {
  await assert.rejects(
    withTimeout(new Promise(() => {}), 10, '解码超时'),
    /解码超时/,
  )
  assert.equal(await withTimeout(Promise.resolve('ok'), 50), 'ok')
})

test('视频先取帧再缩放，isVideoBlob 只认视频类型', () => {
  assert.equal(isVideoBlob(new Blob([], { type: 'video/mp4' })), true)
  assert.equal(isVideoBlob(new Blob([], { type: 'image/jpeg' })), false)
  assert.equal(isVideoBlob(new Blob([], { type: 'image/webp' })), false)
})
