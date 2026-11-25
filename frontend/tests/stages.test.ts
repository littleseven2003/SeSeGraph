import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  compressStage,
  publishStageView,
  stageInputOf,
  stageOverall,
  stagePercent,
  stageSlots,
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
  assert.equal(stageSummary(running), '上传 2/4')
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

test('总进度只按适用阶段计分母，idle 不进发布段', () => {
  // 旧任务没有压缩记录：分母排除压缩段，上传一半、同步未开始即为两段均值的 25%。
  const legacy = taskStages({ ...base, compressedCount: null, receivedCount: 2 })
  assert.equal(stageOverall(legacy, { hasCompressRecord: false, publishIntent: '', publishState: 'idle' }), 25)

  // 仅存图：接口返回 idle，同步齐即满轨，不因发布段未开始而停在 75%。
  const storedOnly = taskStages({ ...base, receivedCount: 4, channelCount: 4, publishState: 'idle' })
  assert.equal(stageOverall(storedOnly, { hasCompressRecord: true, publishIntent: '', publishState: 'idle' }), 100)

  // 未记录压缩的旧任务同样是仅存图口径：剩余两段都完成即满轨。
  const legacyDone = taskStages({ ...base, compressedCount: null, receivedCount: 4, channelCount: 4, publishState: 'idle' })
  assert.equal(stageOverall(legacyDone, { hasCompressRecord: false, publishIntent: '', publishState: 'idle' }), 100)

  // 提交并发布但还没入队：发布段在分母里，四段均值 75% 且不能满轨。
  const autoIdle = taskStages({ ...base, receivedCount: 4, channelCount: 4, publishState: 'idle' })
  assert.equal(stageOverall(autoIdle, { hasCompressRecord: true, publishIntent: 'auto', publishState: 'idle' }), 75)
  const autoPending = taskStages({ ...base, receivedCount: 4, channelCount: 4, publishState: 'pending' })
  assert.equal(stageOverall(autoPending, { hasCompressRecord: true, publishIntent: 'auto', publishState: 'pending' }), 75)

  // 手动发起发布后同样以真实发布为完成条件。
  const manualRunning = taskStages({ ...base, receivedCount: 4, channelCount: 4, publishState: 'publishing' })
  assert.equal(stageOverall(manualRunning, { hasCompressRecord: true, publishIntent: '', publishState: 'publishing' }), 75)
  const autoDone = taskStages({ ...base, receivedCount: 4, channelCount: 4, publishState: 'published' })
  assert.equal(stageOverall(autoDone, { hasCompressRecord: true, publishIntent: 'auto', publishState: 'published' }), 100)

  // 暂停与失败保留已确认进度，不因状态变化归零。
  const paused = taskStages({ ...base, receivedCount: 2, status: 'uploading', controlState: 'paused' })
  assert.equal(stageOverall(paused, { hasCompressRecord: true, publishIntent: '', publishState: 'idle' }), 50)

  // 整数百分点：三张里完成一张时给 33 以上的下取整，不四舍五入成满轨。
  const third = taskStages({ ...base, imageCount: 3, compressedCount: 3, receivedCount: 1 })
  assert.equal(stageOverall(third, { hasCompressRecord: true, publishIntent: '', publishState: 'idle' }), 44)
})

test('四槽固定：不适用的阶段保留槽位并标斜纹原因', () => {
  // 旧任务缺压缩记录：首槽「未记录」，仅存图末槽「不适用」。
  const legacy = stageSlots(taskStages({ ...base, compressedCount: null }), {
    hasCompressRecord: false,
    publishIntent: '',
    publishState: 'idle',
  })
  assert.deepEqual(legacy.map((slot) => slot.key), ['compress', 'upload', 'sync', 'publish'])
  assert.equal(legacy[0].inactive, true)
  assert.equal(legacy[0].reason, '未记录')
  assert.equal(legacy[3].inactive, true)
  assert.equal(legacy[3].reason, '不适用')

  // 提交并发布：末槽参与，未开始也不算不适用。
  const auto = stageSlots(taskStages({ ...base, publishState: 'idle' }), {
    hasCompressRecord: true,
    publishIntent: 'auto',
    publishState: 'idle',
  })
  assert.equal(auto[0].inactive, false)
  assert.equal(auto[3].inactive, false)
  assert.equal(auto[3].ratio, 0)

  // 已发布：末槽满格。
  const done = stageSlots(taskStages({ ...base, receivedCount: 4, channelCount: 4, publishState: 'published' }), {
    hasCompressRecord: true,
    publishIntent: 'auto',
    publishState: 'published',
  })
  assert.equal(done[3].ratio, 1)

  // 不适用槽位不贡献比例，总进度口径不变。
  assert.equal(stageOverall(taskStages({ ...base, compressedCount: null, receivedCount: 4, channelCount: 4, publishState: 'idle' }), {
    hasCompressRecord: false,
    publishIntent: '',
    publishState: 'idle',
  }), 100)
})

test('阶段整数换算：未真正完成不会显示满格', () => {
  assert.equal(stagePercent(0), 0)
  assert.equal(stagePercent(1), 100)
  assert.equal(stagePercent(199 / 200), 99)
  assert.equal(stagePercent(0.995), 99)
  assert.equal(stagePercent(0.9999), 99)
  assert.equal(stagePercent(-1), 0)
  assert.equal(stagePercent(2), 100)
  // 非数值不放大成满格，按未开始处理。
  assert.equal(stagePercent(Number.NaN), 0)
  // 浮点边界：29/100 的乘积是 28.999…，仍要显示 29 而不是少一个百分点。
  for (const value of [1, 7, 29, 57, 58, 99]) {
    assert.equal(stagePercent(value / 100), value, `${value}/100 应显示 ${value}%`)
  }
  assert.equal(stagePercent(199 / 200), 99, '99.5% 夹回 99')
})

test('同步差一张时总值与同步槽同为 99', () => {
  const stages = taskStages({ ...base, imageCount: 200, compressedCount: 200, receivedCount: 200, channelCount: 199 })
  const slots = stageSlots(stages, { hasCompressRecord: true, publishIntent: '', publishState: 'idle' })
  const sync = slots.find((slot) => slot.key === 'sync')
  assert.ok(sync !== undefined)
  assert.equal(stagePercent(sync.ratio), 99)
  assert.equal(stageOverall(stages, { hasCompressRecord: true, publishIntent: '', publishState: 'idle' }), 99)

  // 补齐最后一张才到满格。
  const full = taskStages({ ...base, imageCount: 200, compressedCount: 200, receivedCount: 200, channelCount: 200 })
  assert.equal(stageOverall(full, { hasCompressRecord: true, publishIntent: '', publishState: 'idle' }), 100)
})
