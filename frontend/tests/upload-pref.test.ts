import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  UPLOAD_CONCURRENCY_DEFAULT,
  UPLOAD_CONCURRENCY_MAX,
  UPLOAD_CONCURRENCY_MIN,
  clampUploadConcurrency,
  readUploadConcurrency,
  saveUploadConcurrency,
} from '../src/utils/upload-pref.ts'

// memoryStorage 提供最小 localStorage 替身。
function memoryStorage(): Storage {
  const map = new Map<string, string>()
  return {
    get length() {
      return map.size
    },
    clear: () => map.clear(),
    getItem: (key: string) => map.get(key) ?? null,
    key: (index: number) => Array.from(map.keys())[index] ?? null,
    removeItem: (key: string) => void map.delete(key),
    setItem: (key: string, value: string) => void map.set(key, value),
  } as Storage
}

test('同时上传数收敛到 1–4 的整数', () => {
  assert.equal(clampUploadConcurrency(0), UPLOAD_CONCURRENCY_MIN)
  assert.equal(clampUploadConcurrency(9), UPLOAD_CONCURRENCY_MAX)
  assert.equal(clampUploadConcurrency('3'), 3)
  assert.equal(clampUploadConcurrency('2.6'), 3)
  assert.equal(clampUploadConcurrency('abc'), UPLOAD_CONCURRENCY_DEFAULT)
  assert.equal(clampUploadConcurrency(null), UPLOAD_CONCURRENCY_DEFAULT)
})

test('读写本地偏好，缺省回落默认值', () => {
  const storage = memoryStorage()
  assert.equal(readUploadConcurrency(storage), UPLOAD_CONCURRENCY_DEFAULT)
  assert.equal(saveUploadConcurrency(storage, 4), 4)
  assert.equal(readUploadConcurrency(storage), 4)
  storage.setItem('sesegraph.upload.concurrency', '99')
  assert.equal(readUploadConcurrency(storage), UPLOAD_CONCURRENCY_MAX)
  assert.equal(readUploadConcurrency(undefined), UPLOAD_CONCURRENCY_DEFAULT)
})
