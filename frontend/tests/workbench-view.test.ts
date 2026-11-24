import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import assert from 'node:assert/strict'

const source = readFileSync(new URL('../src/views/WorkbenchView.vue', import.meta.url), 'utf8')

test('四阶段进度卡不依赖已选图片，刷新后可继续发布或重试', () => {
  assert.ok(source.indexOf('<TaskProgressCard') > 0, '缺少四阶段进度卡')
  // 有图时是左栏第一块，没图时在空态上方单独渲染：两种状态下都能继续发布或重试。
  assert.ok(source.includes('v-if="tiles.length === 0 && serverProgress"'), '无图任务也要能看进度与控制')
  assert.ok(source.includes('已选媒体'), '图片区标题与左栏顺序一致')
  assert.ok(source.includes('v-if="showStages"'), '未开始时进度卡不占高度')
  assert.ok(
    source.includes("serverProgress.value !== null || phase.value !== 'idle' || submitting.value || compressDone.value !== null"),
    '压缩或提交开始后才展示进度卡',
  )
  const leftColumn = source.indexOf('<div class="min-w-0 space-y-4">')
  const card = source.indexOf('<TaskProgressCard', leftColumn)
  const meta = source.indexOf('<MetaPanel', leftColumn)
  const images = source.indexOf('已选媒体', leftColumn)
  assert.ok(card > leftColumn && card < meta, '进度卡排在元信息之前')
  assert.ok(meta < images, '元信息排在图片之前')
})

test('桌面右栏用栏位自身吸顶，长列表滚动时仍可操作', () => {
  assert.ok(source.includes('lg:sticky lg:top-[76px]'), '吸顶要挂在栅格项（aside）上')
  assert.equal(source.includes('<div class="sticky top-[76px] space-y-3">'), false, '内容高度撑不满时，内层吸顶不会生效')
})

test('提交前压缩按模式上限判定，无收益或失败时保留原图', () => {
  // 压缩是否采用产物、失败能否回落都走同一套纯函数，避免界面与提交各判一次。
  assert.ok(source.includes('keepOriginalAfterCompress('), '需要按原图合规性与产物体积决定是否保留原图')
  assert.ok(source.includes('compressFallback('), '压缩失败时按原图是否超限决定保留还是阻断')
  assert.ok(source.includes('restoreSource('), '保留原图要换回 sourceBlob 与原始体积')
  const fallback = source.indexOf("compressFallback(tile.sourceSize ?? tile.size, limit)")
  assert.ok(fallback > 0, '失败回落应使用冻结模式的上限而不是全局设置')
  // 提交时先冻结模式与开关，再决定阈值，切到别的模式不会改变本轮判定。
  const frozen = source.indexOf('const compression = await compressForSubmit(frozen.mode, frozen.autoCompress)')
  assert.ok(frozen > 0, '压缩必须使用冻结后的模式与开关')
  assert.ok(source.includes('压缩无收益'), '汇总文案需要说明保留原图的数量')
})


test('小图调度按稳定 ID 去重并在生成前后确认有效性', () => {
  assert.ok(source.includes('new LocalThumbScheduler('), '小图走统一调度器')
  assert.ok(source.includes('isAlive: (id) => tiles.value.some((tile) => tile.id === id)'), '按稳定 ID 判断有效性')
  assert.ok(source.includes('localThumbs.reset()'), '清空后作废在途结果')
  assert.ok(source.includes('localThumbs.dispose()'), '离页后丢弃迟到结果')
})

test('新建图集默认干净新建态，只在显式续传时加载任务', () => {
  assert.ok(source.includes("targetID > 0 ? loadTaskFor(storage, targetID) : null"), '默认不自动续传')
  assert.ok(source.includes('resumableHint.value = targetID > 0 ? null : loadTask(storage)'), '非续传时只给轻提示')
  assert.ok(source.includes('前往续传'), '提示里提供续传入口')
  assert.ok(source.includes('不影响直接拖入新素材'), '提示不阻断新建')
  assert.ok(source.includes('readUploadConcurrency(window.localStorage)'), '每轮读取同时上传数')
  assert.ok(
    source.includes('effectiveUploadConcurrency(') && source.includes('rules.value.uploadConcurrency'),
    '派发宽度按服务端上限收敛，不多占排队名额',
  )
})
