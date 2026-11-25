import { test } from 'node:test'
import assert from 'node:assert/strict'
import { clearDraft, draftState, draftSummary, hasDraft, startDraft } from '../src/stores/draft.ts'

// fakeImage 构造一条最小草稿项，只关心 key、名称与字节数。
function fakeImage(key: string, size: number) {
  return { key, file: new File([new Uint8Array(0)], key), thumbBlob: new Blob(), kind: 'image' as const, name: key, size }
}

test('草稿写入后能统计张数与字节', () => {
  clearDraft()
  startDraft([fakeImage('a', 1024), fakeImage('b', 2048)])

  assert.equal(hasDraft(), true)
  assert.deepEqual(draftSummary(), { count: 2, bytes: 3072 })
})

test('清空草稿后没有可编辑内容', () => {
  startDraft([fakeImage('a', 10)])
  clearDraft()

  assert.equal(hasDraft(), false)
  assert.deepEqual(draftSummary(), { count: 0, bytes: 0 })
  assert.equal(draftState.meta.title, '')
})

test('重新导入会丢掉已不存在的封面选择', () => {
  clearDraft()
  startDraft([fakeImage('a', 10), fakeImage('b', 20)])
  draftState.meta.coverKey = 'b'

  startDraft([fakeImage('c', 30)])

  assert.equal(draftState.meta.coverKey, '')
})
