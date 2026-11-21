import { test } from 'node:test'
import assert from 'node:assert/strict'
import { naturalCompare, sortByNaturalName } from '../src/utils/natural.ts'

test('数字按数值比较而非字典序', () => {
  const names = ['10.jpg', '2.jpg', '1.jpg', '21.jpg', '3.jpg']
  assert.deepEqual(sortByNaturalName(names, (name) => name), ['1.jpg', '2.jpg', '3.jpg', '10.jpg', '21.jpg'])
})

test('前导零与纯数字等价', () => {
  assert.equal(naturalCompare('007.jpg', '7.jpg'), 0)
  assert.equal(naturalCompare('007.jpg', '8.jpg'), -1)
})

test('数字位数不同时按位数优先', () => {
  assert.equal(naturalCompare('a9.png', 'a10.png'), -1)
  assert.equal(naturalCompare('a100.png', 'a99.png'), 1)
})

test('大小写与目录前缀不影响排序', () => {
  assert.equal(naturalCompare('IMG_2.JPG', 'img_10.jpg'), -1)
  assert.equal(naturalCompare('pack/3.png', 'pack/12.png'), -1)
})

test('名称完全相同时保持原始顺序', () => {
  const items = [
    { id: 'x', name: 'a.jpg' },
    { id: 'y', name: 'a.jpg' },
  ]
  assert.deepEqual(sortByNaturalName(items, (item) => item.name).map((item) => item.id), ['x', 'y'])
})

test('停止位不同时较短名称在前', () => {
  assert.equal(naturalCompare('page', 'page2'), -1)
  assert.equal(naturalCompare('page2', 'page'), 1)
})
