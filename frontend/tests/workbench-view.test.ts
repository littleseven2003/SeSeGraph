import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import assert from 'node:assert/strict'

const source = readFileSync(new URL('../src/views/WorkbenchView.vue', import.meta.url), 'utf8')

test('进度面板不依赖已选图片，刷新后可继续发布或重试', () => {
  const panel = source.indexOf('<ProgressPanel')
  assert.ok(panel > 0, '缺少进度面板')
  // 面板必须在「有图」分支之外：拿手机底栏与 v-else 结束位置做参照。
  const bar = source.indexOf('fixed inset-x-0 bottom-0')
  const emptyState = source.indexOf('v-if="tiles.length === 0"')
  assert.ok(emptyState >= 0, '缺少空态分支')
  assert.ok(panel > bar, '进度面板应排在「有图」分支之后，不能只在选了图片时渲染')
  assert.ok(source.includes('进度面板对所有状态可见'), '需要说明面板为何放在分支外')
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
