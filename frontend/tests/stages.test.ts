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
  assert.equal(uploadStageView({ imageCount: 4, receivedCount: 0, status: 'uploading', controlState: 'running' }).value, '等待')
  assert.equal(uploadStageView({ imageCount: 4, receivedCount: 2, status: 'uploading', controlState: 'running' }).value, '2/4')
  assert.equal(uploadStageView({ imageCount: 4, receivedCount: 4, status: 'queued', controlState: 'running' }).tone, 'ok')
  assert.equal(uploadStageView({ imageCount: 4, receivedCount: 2, status: 'failed', controlState: 'running' }).tone, 'danger')
  // 已接收等于总数时即使任务失败也算完成，避免把已收齐误报成失败。
  assert.equal(uploadStageView({ imageCount: 4, receivedCount: 4, status: 'failed', controlState: 'running' }).tone, 'ok')
  // 暂停与停止只标在实际受影响的阶段。
  assert.equal(uploadStageView({ imageCount: 4, receivedCount: 1, status: 'uploading', controlState: 'paused' }).value, '已暂停')
  assert.equal(uploadStageView({ imageCount: 4, receivedCount: 1, status: 'uploading', controlState: 'stopped' }).value, '已停止')
  assert.equal(uploadStageView({ imageCount: 4, receivedCount: 1, status: 'queued', controlState: 'paused' }).value, '1/4')
})

test('同步段只认已入频道的张数', () => {
  assert.equal(syncStageView({ imageCount: 4, channelCount: 0, status: 'queued', controlState: 'running' }).value, '等待')
  assert.equal(syncStageView({ imageCount: 4, channelCount: 3, status: 'queued', controlState: 'running' }).value, '3/4')
  assert.equal(syncStageView({ imageCount: 4, channelCount: 4, status: 'completed', controlState: 'running' }).tone, 'ok')
  assert.equal(syncStageView({ imageCount: 4, channelCount: 1, status: 'failed', controlState: 'running' }).tone, 'danger')
  assert.equal(syncStageView({ imageCount: 4, channelCount: 1, status: 'queued', controlState: 'paused' }).value, '已暂停')
})

test('发布段只看发布状态，暂停单独标注', () => {
  assert.equal(publishStageView({ publishState: 'idle', controlState: 'running' }).value, '未开始')
  assert.equal(publishStageView({ publishState: 'pending', controlState: 'running' }).value, '排队中')
  assert.equal(publishStageView({ publishState: 'publishing', controlState: 'running' }).tone, 'run')
  // 排队与进行中不编固定百分比。
  assert.equal(publishStageView({ publishState: 'pending', controlState: 'running' }).ratio, 0)
  assert.equal(publishStageView({ publishState: 'publishing', controlState: 'running' }).ratio, 0)
  assert.equal(publishStageView({ publishState: 'published', controlState: 'running' }).tone, 'ok')
  assert.equal(publishStageView({ publishState: 'failed', controlState: 'running' }).tone, 'danger')
  // 发布还没开始时暂停不算这一段的暂停，只有排队或进行中才标。
  assert.equal(publishStageView({ publishState: 'idle', controlState: 'paused' }).value, '未开始')
  assert.equal(publishStageView({ publishState: 'pending', controlState: 'paused' }).value, '已暂停')
  assert.equal(publishStageView({ publishState: 'pending', controlState: 'paused' }).tone, 'warn')
  assert.equal(publishStageView({ publishState: 'pending', controlState: 'stopped' }).value, '已停止')
})

test('整体概况优先报失败，其次报进行中的阶段', () => {
  const done = taskStages({ ...base, receivedCount: 4, channelCount: 4, publishState: 'published' })
  assert.equal(stageSummary(done), '四阶段已完成')
  assert.equal(stageOverall(done), 100)

  const running = taskStages({ ...base, receivedCount: 2 })
  assert.equal(stageSummary(running), '上传 50%')
  // 排队与发布中没有确定进度，给文字而不是百分比。
  const queued = taskStages({ ...base, receivedCount: 4, channelCount: 4, publishState: 'pending' })
  assert.equal(stageSummary(queued), '发布排队中')
  const idlePublish = taskStages({ ...base, receivedCount: 4, channelCount: 4 })
  assert.equal(stageSummary(idlePublish), '发布待开始')

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

test('总进度只按适用阶段计分母，未达完成条件最高 99', () => {
  // 旧任务没有压缩记录：分母排除压缩段，上传一半、同步未开始即为两段均值的 25%。
  const legacy = taskStages({ ...base, compressedCount: null, receivedCount: 2 })
  assert.equal(stageOverall(legacy, { hasCompressRecord: false, publishIntent: '', publishState: '' }), 25)

  // 仅存图：同步齐即满轨，不因发布段未开始而停在 75%。
  const storedOnly = taskStages({ ...base, receivedCount: 4, channelCount: 4, publishState: '' })
  assert.equal(stageOverall(storedOnly, { hasCompressRecord: true, publishIntent: '', publishState: '' }), 100)

  // 提交并发布：同步齐但发布排队中，发布段没有确定进度，四段均值 75% 且不能满轨。
  const autoPending = taskStages({ ...base, receivedCount: 4, channelCount: 4, publishState: 'pending' })
  assert.equal(stageOverall(autoPending, { hasCompressRecord: true, publishIntent: 'auto', publishState: 'pending' }), 75)
  const autoDone = taskStages({ ...base, receivedCount: 4, channelCount: 4, publishState: 'published' })
  assert.equal(stageOverall(autoDone, { hasCompressRecord: true, publishIntent: 'auto', publishState: 'published' }), 100)

  // 手动发起发布后同样以真实发布为完成条件。
  const manualRunning = taskStages({ ...base, receivedCount: 4, channelCount: 4, publishState: 'publishing' })
  assert.equal(stageOverall(manualRunning, { hasCompressRecord: true, publishIntent: '', publishState: 'publishing' }), 75)

  // 暂停与失败保留已确认进度，不因状态变化归零。
  const paused = taskStages({ ...base, receivedCount: 2, status: 'uploading', controlState: 'paused' })
  assert.equal(stageOverall(paused, { hasCompressRecord: true, publishIntent: '', publishState: '' }), 50)

  // 整数百分点：三张里完成一张时给 33，不四舍五入成 34 以上。
  const third = taskStages({ ...base, imageCount: 3, compressedCount: 3, receivedCount: 1 })
  assert.equal(stageOverall(third, { hasCompressRecord: true, publishIntent: '', publishState: '' }), 44)
})
