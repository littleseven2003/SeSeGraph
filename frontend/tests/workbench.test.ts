import { test } from 'node:test'
import assert from 'node:assert/strict'
import { assignSortOrders, moveItem, removeItem, selectCover, sortByName, totalBytes } from '../src/utils/workbench.ts'

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
