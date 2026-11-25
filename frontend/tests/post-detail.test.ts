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

test('详情页是 1120 版心的非对称双栏，含进度与危险区', () => {
  const view = source('views/PostDetailView.vue')
  assert.ok(view.includes('max-w-[1120px]'), '版心 1120')
  assert.ok(view.includes('lg:grid-cols-[minmax(0,1fr)_340px]'), '桌面非对称双栏')
  assert.ok(view.includes('lg:sticky lg:top-20'), '右栏滚动时固定')
  assert.ok(view.includes('id="progress"'), '进度区可被深链定位')
  assert.ok(view.includes('<TaskStageBar'), '四阶段复用同一组件')
  assert.ok(view.includes('taskStages(stageInputOf('), '进度口径复用统一判定')
  assert.ok(view.includes('confirmDelete'), '永久删除需二次确认')
  assert.ok(view.includes('setPostDisabled'), '撤回与恢复走目标值接口')
  assert.ok(view.includes('postShareUrl'), '分享链接沿用同一判定')
  assert.ok(view.includes('imageShareUrl'), '单图链接沿用公开条件')
  assert.equal(view.includes('TODO'), false, '不留下待办字样')
})

test('任务页已完成任务可跳到详情进度', () => {
  const view = source('views/TasksView.vue')
  assert.ok(view.includes("name: 'post'"), '任务可跳详情')
  assert.ok(view.includes("hash: '#progress'"), '跳到详情进度区')
  assert.ok(view.includes("task.status === 'completed'"), '只对已完成任务露出入口')
})
