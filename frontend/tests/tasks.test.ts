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
  channelStage,
  publishStage,
  uploadStage,
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

test('三阶段各自独立，不互相冒充', () => {
  // 已接收 2 张，但只有 1 张真正进了频道。
  assert.deepEqual(uploadStage(running), { value: '2/3 张', tone: 'run' })
  assert.deepEqual(channelStage(running), { value: '1/3 张', tone: 'run' })
  assert.deepEqual(publishStage(running), { value: '未开始', tone: 'muted' })

  // 已存入频道但未发布：前两段完成，第三段仍待发布。
  const stored = { ...running, stored_count: 3, received_count: 3, channel_count: 3, publish_state: 'pending' }
  assert.deepEqual(uploadStage(stored), { value: '已完成', tone: 'ok' })
  assert.deepEqual(channelStage(stored), { value: '已完成', tone: 'ok' })
  assert.deepEqual(publishStage(stored), { value: '待发布', tone: 'run' })

  // 发布失败不能把前两段也标成失败。
  const failedPublish = { ...stored, publish_state: 'failed' }
  assert.equal(uploadStage(failedPublish).tone, 'ok')
  assert.deepEqual(publishStage(failedPublish), { value: '发布失败', tone: 'danger' })
})

test('传图失败只影响未完成的那一段', () => {
  const failed = { ...running, status: 'failed', stored_count: 1, received_count: 1, channel_count: 0 }
  assert.deepEqual(uploadStage(failed), { value: '1/3 张', tone: 'danger' })
  assert.deepEqual(channelStage(failed), { value: '0/3 张', tone: 'danger' })
  assert.deepEqual(publishStage(failed), { value: '未开始', tone: 'muted' })
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

test('空任务与仅发布中的任务也有明确文案', () => {
  const empty = { ...running, image_count: 0, stored_count: 0, received_count: 0, channel_count: 0 }
  assert.deepEqual(uploadStage(empty), { value: '无图片', tone: 'muted' })
  assert.deepEqual(channelStage(empty), { value: '无图片', tone: 'muted' })
  const publishing = { ...running, stored_count: 3, received_count: 3, channel_count: 3, publish_state: 'publishing' }
  assert.deepEqual(publishStage(publishing), { value: '发布中', tone: 'run' })
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
  assert.deepEqual(uploadStage(channelFailed), { value: '已完成', tone: 'ok' })
  assert.deepEqual(channelStage(channelFailed), { value: '2/3 张', tone: 'danger' })
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
