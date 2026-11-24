import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'

const grid = readFileSync(new URL('../src/components/workbench/ImageGrid.vue', import.meta.url), 'utf8')
const view = readFileSync(new URL('../src/views/WorkbenchView.vue', import.meta.url), 'utf8')

test('网格只渲染 512px 小图，不挂原图地址', () => {
  assert.ok(grid.includes(':src="item.thumbUrl"'), '网格图源必须是缩略图')
  assert.equal(grid.includes(':src="item.previewUrl"'), false, '网格不得使用原图地址')
  assert.ok(grid.includes('aspect-[4/3]'), '缩略图容器比例固定，避免滚动跳动')
  assert.ok(grid.includes('loading="lazy"'), '缩略图懒加载')
  assert.ok(grid.includes('decoding="async"'), '异步解码，滚动不被解码阻塞')
  assert.ok(grid.includes('缩略图生成中'), '生成中显示占位而非裂图')
})

test('导入即串行生成小图，取消或删除时回收', () => {
  assert.ok(view.includes('queueLocalThumb'), '导入后排队生成')
  assert.ok(view.includes('THUMB_MAX_PIXELS'), '与 v1.5.0 同规格 512px')
  assert.ok(view.includes("if (item.thumbUrl !== '') URL.revokeObjectURL(item.thumbUrl)"), '释放时回收小图地址')
  assert.ok(view.includes('item.thumbBlob = undefined'), '同时释放小图 Blob')
})

test('导入后生成的小图复用于上传回存', () => {
  assert.ok(view.includes('let thumb = tile.thumbBlob'), '优先复用已生成的小图')
  assert.ok(view.includes("thumb = await renderThumb(source, THUMB_MAX_PIXELS)"), '缺失时才补一次解码')
})

test('防社死开启时小图默认模糊并可点按揭开', () => {
  assert.ok(grid.includes('blurredOf(item)'), '按开关与揭开状态判定')
  assert.ok(grid.includes('scale-125 blur-[6px]'), '模糊与放大复用图集样式')
  assert.ok(grid.includes('toggleReveal(item)'), '点按单项临时揭开')
})

test('重新开启防社死时清空临时揭开，旧项不会一直露着', () => {
  assert.ok(grid.includes('if (on) revealed.value.clear()'), '开关重新开启时复位揭开记录')
})

test('工作台封面与视频预览同样接入全局模糊', () => {
  assert.ok(view.includes('coverBlurred'), '封面预览按开关模糊')
  assert.ok(view.includes('previewBlurred'), '视频预览按开关模糊')
  assert.ok(view.includes('点按封面可临时揭开'), '封面给出揭开提示')
  assert.ok(view.includes('点按画面揭开'), '视频给出揭开提示')
  assert.ok(view.includes('coverRevealed.value = false'), '重新开启开关时封面复位')
  assert.ok(view.includes('previewRevealed.value = false'), '重新开启开关时视频复位')
})
