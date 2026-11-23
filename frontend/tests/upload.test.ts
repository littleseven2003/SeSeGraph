import { test } from 'node:test'
import assert from 'node:assert/strict'
import { acceptedCount, initialStatuses, planUploads, runUploadPool, UPLOAD_CONCURRENCY } from '../src/utils/upload.ts'
import type { ServerProgress } from '../src/api/posts.ts'

function progress(status: string, images: { sort_order: number; status: string }[], imageCount = 3): ServerProgress {
  return {
    id: 7,
    status,
    tags: [],
    image_count: imageCount,
    stored_count: images.filter((image) => image.status === 'stored').length,
    failed_count: images.filter((image) => image.status === 'failed').length,
    images,
  }
}

test('空进度时全部待上传', () => {
  const result = planUploads(progress('uploading', []), 3)
  assert.ok('plan' in result)
  assert.deepEqual(result.plan.pending, [
    { index: 0, sortOrder: 1 },
    { index: 1, sortOrder: 2 },
    { index: 2, sortOrder: 3 },
  ])
  assert.equal(result.plan.skipped, 0)
})

test('已落盘的序号被跳过，只补缺失', () => {
  const result = planUploads(
    progress('uploading', [
      { sort_order: 1, status: 'uploaded' },
      { sort_order: 2, status: 'stored' },
    ]),
    3,
  )
  assert.ok('plan' in result)
  assert.deepEqual(result.plan.pending, [{ index: 2, sortOrder: 3 }])
  assert.equal(result.plan.skipped, 2)
})

test('中断时中途序号不会重复上传', () => {
  const result = planUploads(
    progress('uploading', [{ sort_order: 2, status: 'uploaded' }]),
    3,
  )
  assert.ok('plan' in result)
  assert.deepEqual(result.plan.pending.map((item) => item.sortOrder), [1, 3])
})

test('已入队的任务不再补传', () => {
  const result = planUploads(
    progress('queued', [
      { sort_order: 1, status: 'uploaded' },
      { sort_order: 2, status: 'stored' },
      { sort_order: 3, status: 'stored' },
    ]),
    3,
  )
  assert.ok('plan' in result)
  assert.deepEqual(result.plan.pending, [])
  assert.equal(result.plan.skipped, 3)
})

test('数量不一致时拒绝继续，避免错位覆盖', () => {
  const result = planUploads(progress('uploading', []), 5)
  assert.ok('error' in result)
  assert.match(result.error, /数量/)
})

test('初始状态与已收录数量', () => {
  const states = initialStatuses(
    progress('uploading', [
      { sort_order: 1, status: 'uploaded' },
      { sort_order: 2, status: 'failed' },
    ]),
  )
  assert.equal(states[1], 'uploaded')
  assert.equal(states[2], 'pending')
  assert.equal(acceptedCount(states), 1)
})

test('第 6 项失败后继续只补未完成项', () => {
  const images = Array.from({ length: 8 }, (_, index) => ({
    sort_order: index + 1,
    status: index < 5 ? 'stored' : index === 5 ? 'failed' : 'pending',
  }))
  const planned = planUploads(progress('failed', images, 8), 8)
  assert.ok('plan' in planned)
  assert.deepEqual(
    planned.plan.pending.map((item) => item.sortOrder),
    [6, 7, 8],
  )
  assert.equal(planned.plan.skipped, 5)
})

test('每轮最多两路并发', async () => {
  let active = 0
  let peak = 0
  const done: number[] = []
  const failure = await runUploadPool([0, 1, 2, 3, 4, 5], UPLOAD_CONCURRENCY, async (item) => {
    active += 1
    peak = Math.max(peak, active)
    await new Promise((resolve) => setTimeout(resolve, 5))
    active -= 1
    done.push(item)
  })
  assert.equal(failure, null)
  assert.equal(peak, 2)
  assert.deepEqual(done.slice().sort((a, b) => a - b), [0, 1, 2, 3, 4, 5])
})

test('首错停止派发，已在途请求收尾', async () => {
  const started: number[] = []
  const finished: number[] = []
  const failure = await runUploadPool([0, 1, 2, 3, 4], 2, async (item) => {
    started.push(item)
    if (item === 1) {
      await new Promise((resolve) => setTimeout(resolve, 1))
      throw new Error('第 2 项失败')
    }
    await new Promise((resolve) => setTimeout(resolve, 10))
    finished.push(item)
  })
  assert.notEqual(failure, null)
  assert.equal(failure?.index, 1)
  assert.deepEqual(started.slice().sort((a, b) => a - b), [0, 1])
  assert.deepEqual(finished, [0])
})

test('队列长度小于并发上限时按实际数量执行', async () => {
  let peak = 0
  let active = 0
  const failure = await runUploadPool(['a'], 2, async () => {
    active += 1
    peak = Math.max(peak, active)
    await new Promise((resolve) => setTimeout(resolve, 1))
    active -= 1
  })
  assert.equal(failure, null)
  assert.equal(peak, 1)
})

test('空队列直接返回成功', async () => {
  const failure = await runUploadPool([], UPLOAD_CONCURRENCY, async () => {
    throw new Error('不应执行')
  })
  assert.equal(failure, null)
})

test('慢传中主动取消：只停在途两路，不再派发后续项', async () => {
  const controller = new AbortController()
  const started: number[] = []
  const pool = runUploadPool([0, 1, 2, 3, 4], UPLOAD_CONCURRENCY, async (item) => {
    started.push(item)
    await new Promise<void>((resolve, reject) => {
      if (controller.signal.aborted) {
        reject(new Error('上传已取消'))
        return
      }
      controller.signal.addEventListener('abort', () => reject(new Error('上传已取消')), { once: true })
    })
    void resolve
  })
  setTimeout(() => controller.abort(), 5)
  const failure = await pool
  assert.notEqual(failure, null)
  assert.deepEqual(started.slice().sort((a, b) => a - b), [0, 1])
})
