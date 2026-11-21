import { test } from 'node:test'
import assert from 'node:assert/strict'
import { clearTask, loadTask, saveTask, type StorageLike } from '../src/utils/task-store.ts'

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

const snapshot = { postId: 12, imageCount: 3, fileNames: ['1.jpg', '2.jpg', '3.jpg'], createdAt: 1700000000000 }

test('保存后可以读回快照', () => {
  const storage = memoryStorage()
  saveTask(storage, snapshot)
  assert.deepEqual(loadTask(storage), snapshot)
})

test('未保存时返回空', () => {
  assert.equal(loadTask(memoryStorage()), null)
})

test('内容损坏时返回空而不是抛错', () => {
  const storage = memoryStorage({ 'sesegraph.workbench.task': '{not json' })
  assert.equal(loadTask(storage), null)
})

test('字段不完整时忽略旧快照', () => {
  const storage = memoryStorage({ 'sesegraph.workbench.task': JSON.stringify({ postId: 3 }) })
  assert.equal(loadTask(storage), null)
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
