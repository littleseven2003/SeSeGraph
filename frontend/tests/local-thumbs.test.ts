import assert from 'node:assert/strict'
import { test } from 'node:test'
import { LocalThumbScheduler, THUMB_MAX_BYTES, isWebpThumb } from '../src/utils/local-thumbs.ts'
import { createSerialQueue } from '../src/utils/serial.ts'

const tick = () => new Promise((resolve) => setTimeout(resolve, 5))

// webpBlob 造一个内容随意的 WebP Blob：校验只看文件头与体积。
function webpBlob(size = 64): Blob {
  const bytes = new Uint8Array(Math.max(12, size))
  bytes.set([0x52, 0x49, 0x46, 0x46], 0)
  bytes.set([0x57, 0x45, 0x42, 0x50], 8)
  return new Blob([bytes], { type: 'image/webp' })
}

// pngBlob 造一个非 WebP 的 Blob，用于验证回写前的格式校验。
function pngBlob(): Blob {
  return new Blob([new Uint8Array([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0])], { type: 'image/png' })
}

interface Harness {
  queue: ReturnType<typeof createSerialQueue>
  alive: Set<string>
  attached: { id: string; blob: Blob }[]
  renders: string[]
  gate: { resolve: (blob: Blob) => void } | null
  scheduler: LocalThumbScheduler
}

function harness(options: { gated?: boolean; blob?: () => Blob } = {}): Harness {
  const queue = createSerialQueue()
  const alive = new Set<string>()
  const attached: { id: string; blob: Blob }[] = []
  const renders: string[] = []
  let gate: { resolve: (blob: Blob) => void } | null = null
  const scheduler = new LocalThumbScheduler(queue, {
    isAlive: (id) => alive.has(id),
    render: (id) => {
      renders.push(id)
      const blob = options.blob?.() ?? webpBlob()
      if (options.gated !== true) return Promise.resolve(blob)
      return new Promise<Blob>((resolve) => {
        gate = { resolve }
      })
    },
    attach: (id, blob) => attached.push({ id, blob }),
  })
  return { queue, alive, attached, renders, get gate() { return gate }, scheduler } as Harness
}

test('同一项重复请求只生成一次，重排后结果仍挂回原 ID', async () => {
  const h = harness()
  h.alive.add('a')
  // 重排会复制卡片对象，同一 ID 会再次进入排队。
  h.scheduler.request('a')
  h.scheduler.request('a')
  await tick()
  assert.deepEqual(h.renders, ['a'])
  assert.equal(h.attached.length, 1)
  assert.equal(h.attached[0].id, 'a')
  assert.equal(h.scheduler.size(), 0)
})

test('生成中被删除的项不再挂回网格', async () => {
  const h = harness({ gated: true })
  h.alive.add('a')
  h.scheduler.request('a')
  await tick()
  assert.deepEqual(h.renders, ['a'])
  // 生成尚未结束时删掉该项。
  h.alive.delete('a')
  h.gate?.resolve(webpBlob())
  await tick()
  assert.equal(h.attached.length, 0)
  assert.equal(await h.queue.idle().then(() => h.scheduler.size()), 0)
})

test('清空后迟到的结果直接丢弃，新 ID 照常生成', async () => {
  const h = harness({ gated: true })
  h.alive.add('a')
  h.scheduler.request('a')
  await tick()
  // 清空：旧结果作废，去重集合清空。
  h.alive.clear()
  h.scheduler.reset()
  h.gate?.resolve(webpBlob())
  await tick()
  assert.equal(h.attached.length, 0)

  // 重新导入的项照常排队并挂回。
  h.alive.add('b')
  h.scheduler.request('b')
  await tick()
  assert.deepEqual(h.renders, ['a', 'b'])
})

test('回写前校验 WebP 与体积上限，不合格的保留占位', async () => {
  const h = harness({ blob: () => pngBlob() })
  h.alive.add('a')
  h.scheduler.request('a')
  await tick()
  assert.equal(h.attached.length, 0)

  // 超过 150 KB 的 WebP 同样不挂。
  const oversized = harness({ blob: () => webpBlob(THUMB_MAX_BYTES + 1) })
  oversized.alive.add('a')
  oversized.scheduler.request('a')
  await tick()
  assert.equal(oversized.attached.length, 0)
})

test('生成前已失效的项不进入渲染', async () => {
  const h = harness()
  h.scheduler.request('missing')
  await tick()
  assert.deepEqual(h.renders, [])
})

test('离页后不再接受新请求，在途结果作废', async () => {
  const h = harness({ gated: true })
  h.alive.add('a')
  h.scheduler.request('a')
  await tick()
  h.scheduler.dispose()
  h.gate?.resolve(webpBlob())
  await tick()
  assert.equal(h.attached.length, 0)

  h.alive.add('b')
  h.scheduler.request('b')
  await tick()
  assert.deepEqual(h.renders, ['a'])
})

test('WebP 校验按文件头与体积判定', async () => {
  assert.equal(await isWebpThumb(webpBlob()), true)
  assert.equal(await isWebpThumb(pngBlob()), false)
  assert.equal(await isWebpThumb(webpBlob(THUMB_MAX_BYTES + 1)), false)
  assert.equal(await isWebpThumb(new Blob([new Uint8Array(0)], { type: 'image/webp' })), false)
  assert.equal(await isWebpThumb(new Blob([new Uint8Array(12)], { type: 'image/webp' })), false)
})
