import { test } from 'node:test'
import assert from 'node:assert/strict'
import { acceptedCount, initialStatuses, planUploads } from '../src/utils/upload.ts'
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
