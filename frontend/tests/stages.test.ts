import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  compressStage,
  publishStageView,
  stageInputOf,
  stageOverall,
  stageSummary,
  syncStageView,
  taskStages,
  uploadStageView,
} from '../src/utils/stages.ts'

const base = {
  imageCount: 4,
  compressedCount: 4,
  receivedCount: 0,
  channelCount: 0,
  publishState: 'idle',
  controlState: 'running',
  status: 'uploading',
}

test('四阶段固定顺序与编号', () => {
  const stages = taskStages(base)
  assert.deepEqual(stages.map((item) => item.key), ['compress', 'upload', 'sync', 'publish'])
  assert.deepEqual(stages.map((item) => item.index), [1, 2, 3, 4])
  assert.deepEqual(stages.map((item) => item.label), ['压缩', '上传', '同步', '发布'])
})

test('压缩段如实区分完成、进行中与未记录', () => {
  assert.equal(compressStage({ imageCount: 4, compressedCount: 4 }).value, '完成')
  assert.equal(compressStage({ imageCount: 4, compressedCount: 4 }).tone, 'ok')
  assert.equal(compressStage({ imageCount: 4, compressedCount: 2 }).value, '2/4')
  assert.equal(compressStage({ imageCount: 4, compressedCount: 2 }).ratio, 0.5)
  // 旧任务没有记录时显示未记录，不补一个猜测值。
  assert.equal(compressStage({ imageCount: 4, compressedCount: null }).value, '未记录')
  assert.equal(compressStage({ imageCount: 4, compressedCount: null }).ratio, 0)
  assert.equal(compressStage({ imageCount: 0, compressedCount: null }).value, '无媒体')
})

test('上传段按已接收张数推进，失败转危险色', () => {
  assert.equal(uploadStageView({ imageCount: 4, receivedCount: 0, status: 'uploading' }).value, '等待')
  assert.equal(uploadStageView({ imageCount: 4, receivedCount: 2, status: 'uploading' }).value, '2/4')
  assert.equal(uploadStageView({ imageCount: 4, receivedCount: 4, status: 'queued' }).tone, 'ok')
  assert.equal(uploadStageView({ imageCount: 4, receivedCount: 2, status: 'failed' }).tone, 'danger')
  // 已接收等于总数时即使任务失败也算完成，避免把已收齐误报成失败。
  assert.equal(uploadStageView({ imageCount: 4, receivedCount: 4, status: 'failed' }).tone, 'ok')
})

test('同步段只认已入频道的张数', () => {
  assert.equal(syncStageView({ imageCount: 4, channelCount: 0, status: 'queued' }).value, '等待')
  assert.equal(syncStageView({ imageCount: 4, channelCount: 3, status: 'queued' }).value, '3/4')
  assert.equal(syncStageView({ imageCount: 4, channelCount: 4, status: 'completed' }).tone, 'ok')
  assert.equal(syncStageView({ imageCount: 4, channelCount: 1, status: 'failed' }).tone, 'danger')
})

test('发布段只看发布状态，暂停单独标注', () => {
  assert.equal(publishStageView({ publishState: 'idle', controlState: 'running' }).value, '未开始')
  assert.equal(publishStageView({ publishState: 'pending', controlState: 'running' }).value, '排队中')
  assert.equal(publishStageView({ publishState: 'publishing', controlState: 'running' }).tone, 'run')
  assert.equal(publishStageView({ publishState: 'published', controlState: 'running' }).tone, 'ok')
  assert.equal(publishStageView({ publishState: 'failed', controlState: 'running' }).tone, 'danger')
  assert.equal(publishStageView({ publishState: 'idle', controlState: 'paused' }).value, '已暂停')
  assert.equal(publishStageView({ publishState: 'idle', controlState: 'paused' }).tone, 'warn')
})

test('整体概况优先报失败，其次报进行中的阶段', () => {
  const done = taskStages({ ...base, receivedCount: 4, channelCount: 4, publishState: 'published' })
  assert.equal(stageSummary(done), '四阶段已完成')
  assert.equal(stageOverall(done), 100)

  const running = taskStages({ ...base, receivedCount: 2 })
  assert.equal(stageSummary(running), '上传 50%')

  const failed = taskStages({ ...base, receivedCount: 2, status: 'failed' })
  assert.equal(stageSummary(failed), '上传失败')
})

test('任务行来源字段缺失时按未记录与未接收处理', () => {
  const input = stageInputOf({
    status: 'uploading',
    publish_state: 'idle',
    control_state: 'running',
    image_count: 3,
  })
  assert.equal(input.compressedCount, null)
  assert.equal(input.receivedCount, 0)
  assert.equal(input.channelCount, 0)

  const full = stageInputOf({
    status: 'uploading',
    publish_state: 'idle',
    control_state: 'running',
    image_count: 3,
    compressed_count: 3,
    received_count: 1,
    channel_count: 0,
  })
  assert.equal(compressStage(full).value, '完成')
  assert.equal(uploadStageView(full).value, '1/3')
})
