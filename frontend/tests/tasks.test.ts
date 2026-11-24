import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  TASK_FILTERS,
  badgeOf,
  canContinueUpload,
  canPause,
  canResume,
  canRetryStore,
  canStop,
} from '../src/utils/tasks.ts'

// 进行中：已接收 2/3，其中 1 张已入频道，Telegraph 尚未开始。
const running = {
  status: 'uploading',
  publish_state: 'idle',
  control_state: 'running',
  image_count: 3,
  stored_count: 2,
  received_count: 2,
  channel_count: 1,
}

test('筛选值与后端参数一致且顺序固定', () => {
  assert.deepEqual(
    TASK_FILTERS.map((item) => item.value),
    ['', 'active', 'paused', 'stopped', 'finished'],
  )
  assert.deepEqual(
    TASK_FILTERS.map((item) => item.label),
    ['全部', '进行中', '已暂停', '已停止', '已完成'],
  )
})

test('整体状态胶囊暂停与停止优先', () => {
  assert.deepEqual(badgeOf(running), { label: '传输中', tone: 'run' })
  assert.deepEqual(badgeOf({ ...running, control_state: 'paused' }), { label: '已暂停', tone: 'warn' })
  assert.deepEqual(badgeOf({ ...running, control_state: 'stopped' }), { label: '已停止', tone: 'danger' })
  assert.deepEqual(badgeOf({ ...running, publish_state: 'published', status: 'completed' }), { label: '已完成', tone: 'ok' })
  assert.deepEqual(badgeOf({ ...running, publish_state: 'failed' }), { label: '失败', tone: 'danger' })
  assert.deepEqual(badgeOf({ ...running, status: 'queued' }), { label: '存频道中', tone: 'run' })
})

test('控制入口：已完成无停止，暂停可继续', () => {
  assert.equal(canPause(running), true)
  assert.equal(canResume(running), false)
  assert.equal(canStop(running), true)
  assert.equal(canContinueUpload(running), true)

  const paused = { ...running, control_state: 'paused' }
  assert.equal(canPause(paused), false)
  assert.equal(canResume(paused), true)
  // 暂停时不能直接续传，必须先继续任务。
  assert.equal(canContinueUpload(paused), false)
  assert.equal(canStop(paused), true)

  const published = { ...running, status: 'completed', publish_state: 'published', stored_count: 3, received_count: 3, channel_count: 3 }
  assert.equal(canStop(published), false)
  assert.equal(canContinueUpload(published), false)

  const stopped = { ...running, control_state: 'stopped' }
  assert.equal(canStop(stopped), false)
  assert.equal(canResume(stopped), false)
  assert.equal(canPause(stopped), false)
})

test('仅发布中的任务有明确胶囊文案', () => {
  const publishing = { ...running, stored_count: 3, received_count: 3, channel_count: 3, publish_state: 'publishing' }
  assert.deepEqual(badgeOf(publishing), { label: '发布中', tone: 'run' })
})

test('存频道失败不倒退已接收，并给出频道重试入口', () => {
  // 3 张都收下，2 张入库、1 张存频道失败：第一段仍是已完成。
  const channelFailed = {
    status: 'failed',
    publish_state: 'idle',
    control_state: 'running',
    image_count: 3,
    stored_count: 2,
    received_count: 3,
    channel_count: 2,
  }
  // 已收齐的失败任务不再显示「继续上传」，改为频道重试。
  assert.equal(canContinueUpload(channelFailed), false)
  assert.equal(canRetryStore(channelFailed), true)
  // 已停止的任务不给重试入口；仍在上传的任务不叫重试存图。
  assert.equal(canRetryStore({ ...channelFailed, control_state: 'stopped' }), false)
  assert.equal(canRetryStore({ ...channelFailed, status: 'uploading' }), false)
  // 只有正在往服务器传才叫「继续上传」。
  assert.equal(canContinueUpload({ ...channelFailed, status: 'uploading', received_count: 2 }), true)
  assert.equal(canContinueUpload({ ...channelFailed, status: 'queued' }), false)
})
