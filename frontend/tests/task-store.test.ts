import { test } from 'node:test'
import assert from 'node:assert/strict'
import { clearTask, loadTask, matchSnapshotFiles, saveTask, type StorageLike } from '../src/utils/task-store.ts'

function memoryStorage(initial: Record<string, string> = {}): StorageLike & { data: Record<string, string> } {
  const data = { ...initial }
  return {
    data,
    getItem: (key) => (key in data ? data[key] : null),
    setItem: (key, value) => {
      data[key] = value
    },
    removeItem: (key) => {
      delete data[key]
    },
  }
}

const files = [
  { name: '1.jpg', size: 100 },
  { name: '2.jpg', size: 200 },
  { name: '3.jpg', size: 300 },
]
const snapshot = { postId: 12, imageCount: 3, files, createdAt: 1700000000000 }

test('保存后可以读回快照', () => {
  const storage = memoryStorage()
  saveTask(storage, snapshot)
  assert.deepEqual(loadTask(storage), snapshot)
})

test('未保存时返回空', () => {
  assert.equal(loadTask(memoryStorage()), null)
})

test('内容损坏或字段缺失时返回空', () => {
  assert.equal(loadTask(memoryStorage({ 'sesegraph.workbench.task': '{not json' })), null)
  assert.equal(loadTask(memoryStorage({ 'sesegraph.workbench.task': JSON.stringify({ postId: 3 }) })), null)
  assert.equal(
    loadTask(
      memoryStorage({
        'sesegraph.workbench.task': JSON.stringify({ postId: 3, imageCount: 2, files: [files[0]], createdAt: 1 }),
      }),
    ),
    null,
  )
})

test('清除后不再返回快照', () => {
  const storage = memoryStorage()
  saveTask(storage, snapshot)
  clearTask(storage)
  assert.equal(loadTask(storage), null)
})

test('存储不可用时不抛错', () => {
  const broken: StorageLike = {
    getItem: () => {
      throw new Error('denied')
    },
    setItem: () => {
      throw new Error('denied')
    },
    removeItem: () => {
      throw new Error('denied')
    },
  }
  assert.equal(loadTask(broken), null)
  saveTask(broken, snapshot)
  clearTask(broken)
  assert.equal(loadTask(null), null)
})

test('同一批文件按原顺序匹配', () => {
  const matched = matchSnapshotFiles(files, files)
  assert.deepEqual(matched, { order: [0, 1, 2] })
})

test('重新选择后顺序不同也能恢复原序号', () => {
  const reshuffled = [files[2], files[0], files[1]]
  const matched = matchSnapshotFiles(files, reshuffled)
  assert.deepEqual(matched, { order: [1, 2, 0] })
})

test('同数量但另一批文件被拒绝', () => {
  const other = [
    { name: '1.jpg', size: 100 },
    { name: '2.jpg', size: 200 },
    { name: '9.jpg', size: 300 },
  ]
  const matched = matchSnapshotFiles(files, other)
  assert.ok('error' in matched)
  assert.match(matched.error, /9\.jpg|不是同一批/)
})

test('同名不同体积视为不同文件', () => {
  const resized = [{ name: '1.jpg', size: 999 }, files[1], files[2]]
  const matched = matchSnapshotFiles(files, resized)
  assert.ok('error' in matched)
})

test('数量不一致时拒绝续传', () => {
  const matched = matchSnapshotFiles(files, [files[0], files[1]])
  assert.ok('error' in matched)
  assert.match(matched.error, /数量|3 张/)
})

test('本次选择含重复文件时拒绝', () => {
  const matched = matchSnapshotFiles(files, [files[0], files[1], files[1]])
  assert.ok('error' in matched)
  assert.match(matched.error, /重复/)
})
