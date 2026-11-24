import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  MediaBudget,
  MediaReadTimeoutError,
  THUMB_CACHE_LIMIT,
  THUMB_EAGER_MAX_BYTES,
  ThumbQueue,
  canLoadThumb,
  assertDecodable,
  hasStoredThumb,
  needsExplicitPreview,
  pixelBudgetExceeded,
  readImageSize,
  pickEagerOrders,
  shouldWaitExplicit,
  readWithinBudget,
  withTimeout,
} from '../src/utils/thumbs.ts'
import { isVideoBlob } from '../src/utils/video-frame.ts'

const image = (
  order: number,
  extra: Partial<{ status: string; mime_type: string; file_size: number; thumb_url: string }> = {},
) => ({
  sort_order: order,
  short_code: `code-${order}`,
  mime_type: 'image/jpeg',
  file_size: 800_000,
  status: 'stored',
  thumb_url: '',
  media_version: '0123456789ab',
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

test('小图未命中时视频与大图只在明确预览时才回源', () => {
  const video = { mime_type: 'video/mp4', file_size: 1000 }
  const big = { mime_type: 'image/jpeg', file_size: THUMB_EAGER_MAX_BYTES + 1 }
  const small = { mime_type: 'image/jpeg', file_size: 1000 }
  assert.equal(shouldWaitExplicit(video, false), true)
  assert.equal(shouldWaitExplicit(big, false), true)
  assert.equal(shouldWaitExplicit(small, false), false)
  assert.equal(shouldWaitExplicit(video, true), false)
  assert.equal(shouldWaitExplicit(big, true), false)
})

test('已有落盘小图的视频与大图也会自动加载', () => {
  const images = [
    image(1, { mime_type: 'video/mp4', thumb_url: '/api/posts/1/media/1/thumb?v=0123456789ab' }),
    image(2, { file_size: THUMB_EAGER_MAX_BYTES * 10, thumb_url: '/api/posts/1/media/2/thumb?v=0123456789ab' }),
    image(3, { file_size: THUMB_EAGER_MAX_BYTES + 1 }),
    image(4, { mime_type: 'video/mp4' }),
  ]
  assert.equal(hasStoredThumb(images[0]), true)
  assert.equal(hasStoredThumb(images[2]), false)
  assert.deepEqual(pickEagerOrders([1, 2, 3, 4], images), [1, 2])
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

test('缩略图与换封面取图共用额度时总并发仍为 1，解码也在额度内', async () => {
  const budget = new MediaBudget(1)
  let active = 0
  let peak = 0
  const read = async (kind: string) =>
    await readWithinBudget(
      budget,
      async () => {
        active += 1
        peak = Math.max(peak, active)
        await new Promise((resolve) => setTimeout(resolve, 10))
        active -= 1
        return new Blob([kind])
      },
      async () => {
        // 解码同样计数：如果额度提前释放，这里会出现并发。
        active += 1
        peak = Math.max(peak, active)
        await new Promise((resolve) => setTimeout(resolve, 10))
        active -= 1
        return kind
      },
      { fetchTimeout: 500, decodeTimeout: 500 },
    )
  const results = await Promise.all([read('thumb'), read('cover')])
  assert.deepEqual(results, ['thumb', 'cover'])
  assert.equal(peak, 1, '取流与解码都不能并发')
  assert.equal(budget.running, 0, '结束后额度必须归还')
})

test('慢响应到点即中断并让出额度，下一项可以继续', async () => {
  const budget = new MediaBudget(1)
  let aborted = false
  const slow = readWithinBudget(
    budget,
    (_signal) =>
      // 模拟响应体一直不结束：只有中断信号能结束它。
      new Promise<Blob>((_resolve, reject) => {
        _signal.addEventListener('abort', () => {
          aborted = true
          reject(new Error('aborted'))
        })
      }),
    async () => 'never',
    { fetchTimeout: 40, decodeTimeout: 500 },
  )
  await assert.rejects(slow, (error: Error) => error instanceof MediaReadTimeoutError && error.message === '读取超时')
  assert.equal(aborted, true, '超时必须中断在途请求')
  assert.equal(budget.running, 0, '超时后额度要归还')
  const next = await readWithinBudget(
    budget,
    async () => new Blob(['ok']),
    async () => 'next',
    { fetchTimeout: 200, decodeTimeout: 200 },
  )
  assert.equal(next, 'next', '下一项应立刻能拿到通道')
})

test('解码超时同样释放额度并标记为超时错误', async () => {
  const budget = new MediaBudget(1)
  const result = readWithinBudget(
    budget,
    async () => new Blob(['x']),
    () => new Promise<string>(() => {}),
    { fetchTimeout: 200, decodeTimeout: 30 },
  )
  await assert.rejects(result, (error: Error) => error instanceof MediaReadTimeoutError)
  assert.equal(budget.running, 0)
  assert.equal(budget.pending, 0)
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

// pngBlob 构造只带 IHDR 的 PNG 头，用于尺寸预检测试。
function pngBlob(width: number, height: number): Blob {
  const bytes = new Uint8Array(33)
  bytes.set([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a], 0)
  bytes.set([0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52], 8)
  const view = new DataView(bytes.buffer)
  view.setUint32(16, width, false)
  view.setUint32(20, height, false)
  return new Blob([bytes])
}

// jpegBlob 构造只带 SOF0 段的 JPEG 头。
function jpegBlob(width: number, height: number): Blob {
  const bytes = new Uint8Array(30)
  bytes.set([0xff, 0xd8, 0xff, 0xc0, 0x00, 0x11, 0x08], 0)
  const view = new DataView(bytes.buffer)
  view.setUint16(7, height, false)
  view.setUint16(9, width, false)
  return new Blob([bytes])
}

test('解码前只读文件头取宽高', async () => {
  assert.deepEqual(await readImageSize(pngBlob(8000, 6000)), { width: 8000, height: 6000 })
  assert.deepEqual(await readImageSize(jpegBlob(4032, 3024)), { width: 4032, height: 3024 })
  assert.equal(await readImageSize(new Blob([new Uint8Array([1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16])])), null)
})

test('超出像素预算的源图在解码前被拦下', async () => {
  assert.equal(pixelBudgetExceeded({ width: 8000, height: 6000 }), true)
  assert.equal(pixelBudgetExceeded({ width: 4032, height: 3024 }), false)
  assert.equal(pixelBudgetExceeded(null), false)
  await assert.rejects(async () => assertDecodable(pngBlob(9000, 7000)))
  await assert.doesNotReject(async () => assertDecodable(pngBlob(1200, 900)))
  // 读不出尺寸时同样跳过：否则上限拦不住未知格式。
  await assert.rejects(async () => assertDecodable(new Blob([new Uint8Array([9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9])])))
})

test('尺寸无法确认时跳过生成，队列继续处理下一项', async () => {
  const failed: number[] = []
  const unknown = new Blob([new Uint8Array(32)])
  const queue = new ThumbQueue(
    async (order) => {
      const blob = order === 1 ? unknown : pngBlob(512, 512)
      await assertDecodable(blob)
      return new Blob([String(order)])
    },
    { createUrl: () => 'blob:' + String(Math.random()), revokeUrl: () => {}, onFail: (order) => failed.push(order) },
  )
  queue.request(1)
  queue.request(2)
  for (let tick = 0; tick < 40 && !queue.has(2); tick += 1) {
    await new Promise((resolve) => setTimeout(resolve, 5))
  }
  assert.deepEqual(failed, [1])
  assert.equal(queue.has(2), true)
})

test('单张被拒绝后队列继续处理下一项', async () => {
  const failed: number[] = []
  const queue = new ThumbQueue(
    async (order) => {
      if (order === 1) throw new Error('原图像素超出预算')
      return new Blob([String(order)])
    },
    { createUrl: () => 'blob:' + String(Math.random()), revokeUrl: () => {}, onFail: (order) => failed.push(order) },
  )
  queue.request(1)
  queue.request(2)
  for (let tick = 0; tick < 40 && !queue.has(2); tick += 1) {
    await new Promise((resolve) => setTimeout(resolve, 5))
  }
  assert.deepEqual(failed, [1])
  assert.equal(queue.has(2), true)
  assert.equal(queue.has(1), false)
})
