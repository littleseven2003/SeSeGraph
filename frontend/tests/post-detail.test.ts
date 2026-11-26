import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'

function source(relative: string): string {
  return readFileSync(new URL('../src/' + relative, import.meta.url), 'utf8')
}

test('图集详情有独立路由，与工作台分设', () => {
  const router = source('router/index.ts')
  assert.ok(router.includes("path: '/posts/:id(\\\\d+)', name: 'post'"), '详情页独立路由')
  assert.ok(router.includes('PostDetailView'), '详情页独立组件')
  assert.ok(router.includes("name: 'workbench'"), '工作台仍单独成页')
})

test('画廊点击直达详情页，不再走抽屉', () => {
  const home = source('views/HomeView.vue')
  const start = home.indexOf('async function openDetail')
  const body = home.slice(start, start + 220)
  assert.ok(body.includes("router.push({ name: 'post'"), '点击进入独立详情页')
  assert.equal(body.includes('drawerOpen.value = true'), false, '不再打开侧边抽屉')
})

test('详情页是 1240 版心的非对称双栏，含进度与危险区', () => {
  const view = source('views/PostDetailView.vue')
  assert.ok(view.includes('max-w-[1240px]'), '版心 1240，媒体区更舒展')
  assert.ok(view.includes('lg:grid-cols-[minmax(0,1fr)_312px]'), '桌面非对称双栏，右栏 312px')
  assert.ok(view.includes('lg:sticky lg:top-20'), '右栏滚动时固定')
  assert.ok(view.includes('id="progress"'), '进度区可被深链定位')
  assert.ok(view.includes('v-if="showProgress"'), '完成后隐藏整张进度卡')
  // 进度卡在媒体区上方，隐藏时 #progress 锚点落到媒体区。
  const progressAt = view.indexOf('v-if="showProgress"')
  const mediaAt = view.indexOf(":id=\"showProgress ? 'media' : 'progress'\"")
  assert.ok(progressAt > 0 && mediaAt > progressAt, '进度卡位于媒体区之前且锚点可回落')
  assert.ok(view.includes('<TaskStageBar'), '四阶段复用同一组件')
  assert.ok(view.includes('taskStages(stageInputOf('), '进度口径复用统一判定')
  assert.ok(view.includes('confirmDelete'), '永久删除需二次确认')
  assert.ok(view.includes('setPostDisabled'), '撤回与恢复走目标值接口')
  assert.ok(view.includes('postShareUrl'), '分享链接沿用同一判定')
  assert.ok(view.includes('renderShareText(readShareTemplate(window.localStorage)'), '分享文案复用本机模板与同一渲染')
  assert.ok(view.includes('复制分享文案'), '分享卡提供复制分享文案')
  assert.ok(view.includes('aria-label="刷新浏览量"') && view.includes('title="刷新浏览量"'), '刷新按钮为带语义的图标')
  assert.ok(view.includes('aspect-[3/2] w-full sm:aspect-[4/3]'), '缩略图改为横宽画幅')
  assert.ok(view.includes('flex min-h-11 items-center justify-between gap-1 border-t border-line/70 px-1.5'), '操作栏常驻且窄屏留足触控高度')
  assert.ok(view.includes('h-11 w-9 shrink-0'), '窄屏操作按钮 36×44 热区不再互相重叠')
  const barAt = view.indexOf('揭开第 ${image.sort_order} 张')
  const bar = view.slice(barAt - 400, barAt + 1600)
  assert.equal(bar.includes('-m-2'), false, '媒体操作按钮不用负外边距，相邻热区不再重叠')
  assert.ok(view.includes('sm:h-7 sm:w-7'), '桌面操作按钮收紧')
  assert.ok(view.includes('imageShareUrl'), '单图链接沿用公开条件')
  assert.equal(view.includes('TODO'), false, '不留下待办字样')
})

test('任务页已完成任务可跳到详情进度', () => {
  const view = source('views/TasksView.vue')
  assert.ok(view.includes("name: 'post'"), '任务可跳详情')
  assert.ok(view.includes("hash: '#progress'"), '跳到详情进度区')
  assert.ok(view.includes("task.status === 'completed'"), '只对已完成任务露出入口')
})

test('同步中继续轮询，进度锚点等内容就绪再定位', () => {
  const view = source('views/PostDetailView.vue')
  assert.ok(view.includes("['uploading', 'queued', 'publishing'].includes(state.status)"), '同步中仍算进行中的任务')
  assert.ok(view.includes('function followAnchor'), '锚点在内容渲染后定位')
  assert.ok(view.includes('followAnchor()'), '加载完成后触发一次定位')
})
