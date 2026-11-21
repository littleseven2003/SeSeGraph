import { test } from 'node:test'
import assert from 'node:assert/strict'
import {
  appendSelection,
  assignSortOrders,
  discardBlocked,
  moveItem,
  planUploadQueue,
  removeItem,
  selectCover,
  sortByName,
  totalBytes,
  validatePublishInput,
} from '../src/utils/workbench.ts'
import { matchSnapshotFiles } from '../src/utils/task-store.ts'

const items = [
  { id: 'a', name: '10.jpg', size: 10, sortOrder: 1 },
  { id: 'b', name: '2.jpg', size: 20, sortOrder: 2 },
  { id: 'c', name: '1.jpg', size: 30, sortOrder: 3 },
]

test('序号按当前顺序从 1 递增', () => {
  assert.deepEqual(assignSortOrders(items).map((item) => item.sortOrder), [1, 2, 3])
})

test('按文件名自然排序后重排序号', () => {
  const sorted = sortByName(items)
  assert.deepEqual(sorted.map((item) => item.name), ['1.jpg', '2.jpg', '10.jpg'])
  assert.deepEqual(sorted.map((item) => item.sortOrder), [1, 2, 3])
})

test('拖拽移动后序号保持连续', () => {
  const moved = assignSortOrders(moveItem(items, 2, 0))
  assert.deepEqual(moved.map((item) => item.id), ['c', 'a', 'b'])
  assert.deepEqual(moved.map((item) => item.sortOrder), [1, 2, 3])
})

test('越界移动不改动列表', () => {
  assert.deepEqual(moveItem(items, 0, 9).map((item) => item.id), ['a', 'b', 'c'])
  assert.deepEqual(moveItem(items, -1, 0).map((item) => item.id), ['a', 'b', 'c'])
})

test('剔除单张后重新编号', () => {
  const rest = assignSortOrders(removeItem(items, 'b'))
  assert.deepEqual(rest.map((item) => item.id), ['a', 'c'])
  assert.deepEqual(rest.map((item) => item.sortOrder), [1, 2])
})

test('封面失效时回落到第一张', () => {
  assert.equal(selectCover(items, 'b'), 'b')
  assert.equal(selectCover(items, 'gone'), 'a')
  assert.equal(selectCover([], 'gone'), '')
})

test('汇总体积', () => {
  assert.equal(totalBytes(items), 60)
})

test('新建任务要求标题与封面', () => {
  assert.equal(validatePublishInput({ title: '  ', coverReady: true, resuming: false }), '请填写总标题')
  assert.equal(validatePublishInput({ title: '标题', coverReady: false, resuming: false }), '封面还在生成，请稍后重试')
  assert.equal(validatePublishInput({ title: '标题', coverReady: true, resuming: false }), null)
})

test('续传不要求重新填写标题与封面', () => {
  assert.equal(validatePublishInput({ title: '', coverReady: false, resuming: true }), null)
})

test('刷新后重选同批文件（顺序打乱）可直接续传', () => {
  const snapshotFiles = [
    { name: '01.jpg', size: 100 },
    { name: '02.jpg', size: 200 },
    { name: '03.jpg', size: 300 },
  ]
  const reselected = [snapshotFiles[2], snapshotFiles[0], snapshotFiles[1]]
  const matched = matchSnapshotFiles(snapshotFiles, reselected)
  assert.ok('order' in matched)
  const resuming = true
  assert.equal(validatePublishInput({ title: '', coverReady: false, resuming }), null)
  const restored = matched.order.map((index) => reselected[index].name)
  assert.deepEqual(restored, ['01.jpg', '02.jpg', '03.jpg'])
})

test('选错文件后可恢复为不匹配状态并重新选择', () => {
  const snapshotFiles = [
    { name: '01.jpg', size: 100 },
    { name: '02.jpg', size: 200 },
  ]
  const wrong = [
    { name: 'a.jpg', size: 100 },
    { name: 'b.jpg', size: 200 },
  ]
  assert.ok('error' in matchSnapshotFiles(snapshotFiles, wrong))
  // 重新选回同一批文件后又能匹配，说明入口可以反复使用。
  assert.deepEqual(matchSnapshotFiles(snapshotFiles, snapshotFiles), { order: [0, 1] })
})

test('续传失配后的重选整体替换旧选择，新建任务仍然追加', () => {
  const wrong = [{ id: 'a' }, { id: 'b' }]
  const right = [{ id: 'x' }, { id: 'y' }]
  assert.deepEqual(appendSelection(wrong, right, true), right)
  assert.deepEqual(appendSelection(wrong, right, false).map((item) => item.id), ['a', 'b', 'x', 'y'])
})

test('先选错再一次重选成功后可以匹配回任务', () => {
  const snapshotFiles = [
    { name: '01.jpg', size: 100 },
    { name: '02.jpg', size: 200 },
  ]
  const wrong = [
    { name: 'a.jpg', size: 100 },
    { name: 'b.jpg', size: 200 },
  ]
  assert.ok('error' in matchSnapshotFiles(snapshotFiles, wrong))
  const replaced = appendSelection(wrong, snapshotFiles, true)
  assert.deepEqual(matchSnapshotFiles(snapshotFiles, replaced), { order: [0, 1] })
})

test('上传队列使用开始时的列表快照，中途变化不影响已排定请求', () => {
  const tiles = [{ name: '1.jpg' }, { name: '2.jpg' }, { name: '3.jpg' }]
  const queue = planUploadQueue(tiles.slice(), [
    { index: 0, sortOrder: 1 },
    { index: 2, sortOrder: 3 },
  ])
  tiles.splice(0, 2)
  assert.deepEqual(queue.map((item) => item.tile.name), ['1.jpg', '3.jpg'])
  assert.deepEqual(queue.map((item) => item.sortOrder), [1, 3])
})

test('越界下标的待上传项被跳过，不会取到别的图片', () => {
  assert.deepEqual(planUploadQueue([{ name: 'only.jpg' }], [{ index: 5, sortOrder: 6 }]), [])
})

test('创建与上传期间禁止放弃任务', () => {
  assert.equal(discardBlocked('creating'), true)
  assert.equal(discardBlocked('uploading'), true)
  for (const phase of ['idle', 'queued', 'failed'] as const) {
    assert.equal(discardBlocked(phase), false)
  }
})
