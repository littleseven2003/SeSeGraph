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
