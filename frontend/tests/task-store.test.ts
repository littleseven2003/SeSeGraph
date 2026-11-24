import { test } from 'node:test'
import assert from 'node:assert/strict'
import { clearTask, loadTask, loadTaskFor, matchSnapshotFiles, saveTask, updateSnapshotStrategy, type StorageLike } from '../src/utils/task-store.ts'

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

const snapshotA = {
  postId: 1,
  imageCount: 2,
  files: [
    { name: 'a1.jpg', size: 10 },
    { name: 'a2.jpg', size: 20 },
  ],
  createdAt: 1,
}

const snapshotB = {
  postId: 2,
  imageCount: 2,
  files: [
    { name: 'b1.jpg', size: 30 },
    { name: 'b2.jpg', size: 40 },
  ],
  createdAt: 2,
}

test('按任务编号读取快照，不被其他任务的单键快照覆盖', () => {
  const storage = memoryStorage()
  saveTask(storage, snapshotA)
  saveTask(storage, snapshotB)
  assert.equal(loadTask(storage)?.postId, 2)
  assert.deepEqual(loadTaskFor(storage, 1), snapshotA)
  assert.deepEqual(loadTaskFor(storage, 2), snapshotB)
  assert.equal(loadTaskFor(storage, 3), null)
})

test('兼容旧的单键快照，但只在编号一致时采用', () => {
  const storage = memoryStorage({
    'sesegraph.workbench.task': JSON.stringify(snapshotB),
  })
  assert.deepEqual(loadTaskFor(storage, 2), snapshotB)
  assert.equal(loadTaskFor(storage, 1), null)
})

test('清除指定任务只移除它自己的槽位，较新的续传入口保留', () => {
  const storage = memoryStorage()
  saveTask(storage, snapshotA)
  saveTask(storage, snapshotB)
  // B(2) 是最近任务；清较旧的 A(1) 不应影响 B 的入口。
  clearTask(storage, 1)
  assert.deepEqual(loadTask(storage), snapshotB)
  assert.deepEqual(loadTaskFor(storage, 2), snapshotB)
  assert.equal(loadTaskFor(storage, 1), null)

  // 清最近任务本身时才一并清掉全局索引。
  clearTask(storage, 2)
  assert.equal(loadTask(storage), null)
  assert.equal(loadTaskFor(storage, 2), null)
})

test('没有编号的清理只动最近任务索引，不碰其它任务的槽位', () => {
  const storage = memoryStorage()
  saveTask(storage, snapshotA)
  saveTask(storage, snapshotB)
  clearTask(storage)
  assert.equal(loadTask(storage), null)
  assert.deepEqual(loadTaskFor(storage, 1), snapshotA)
  assert.deepEqual(loadTaskFor(storage, 2), snapshotB)
})

test('同数量但不同文件被拒绝', () => {
  const result = matchSnapshotFiles(snapshotB.files, snapshotA.files)
  assert.ok('error' in result)
})

test('续传收尾只写本任务的画质与压缩偏好，最近的 B 保持不变', () => {
  const storage = memoryStorage()
  const withModeA = { ...snapshotA, mode: 'document' as const, autoCompress: false }
  const withModeB = { ...snapshotB, mode: 'photo' as const, autoCompress: true }
  saveTask(storage, withModeA)
  saveTask(storage, withModeB)
  // 最近任务是 B(2)，续传 A(1) 结束时更新策略只能落在 A 自己身上。
  const updated = updateSnapshotStrategy(storage, 1, 'photo', true)
  assert.deepEqual(updated, { ...withModeA, mode: 'photo', autoCompress: true })
  assert.deepEqual(loadTask(storage), withModeB)
  assert.deepEqual(loadTaskFor(storage, 1), { ...withModeA, mode: 'photo', autoCompress: true })
  // 编号不存在时不产生任何写入。
  assert.equal(updateSnapshotStrategy(storage, 9, 'photo', true), null)
  assert.deepEqual(loadTask(storage), withModeB)
})
