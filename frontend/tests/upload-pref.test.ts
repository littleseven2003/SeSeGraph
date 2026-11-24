import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  UPLOAD_CONCURRENCY_DEFAULT,
  UPLOAD_CONCURRENCY_MAX,
  UPLOAD_CONCURRENCY_MIN,
  clampUploadConcurrency,
  effectiveUploadConcurrency,
  readUploadConcurrency,
  saveUploadConcurrency,
  serverUploadConcurrency,
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

test('服务端上限缺失或非法时回落默认值', () => {
  assert.equal(serverUploadConcurrency(undefined), UPLOAD_CONCURRENCY_DEFAULT)
  assert.equal(serverUploadConcurrency(0), UPLOAD_CONCURRENCY_DEFAULT)
  assert.equal(serverUploadConcurrency('3'), UPLOAD_CONCURRENCY_DEFAULT)
  assert.equal(serverUploadConcurrency(2.4), 2)
  assert.equal(serverUploadConcurrency(99), UPLOAD_CONCURRENCY_MAX)
})

test('派发宽度取本地偏好与服务端上限的较小值', () => {
  // 服务端只接 3 路时，选了 4 路也只派 3 路，不多占一个必然排队的名额。
  assert.equal(effectiveUploadConcurrency(4, 3), 3)
  assert.equal(effectiveUploadConcurrency(2, 3), 2)
  assert.equal(effectiveUploadConcurrency(1, 3), 1)
  // 服务端更宽时仍尊重本地偏好。
  assert.equal(effectiveUploadConcurrency(3, 8), 3)
  // 服务端未声明时按默认宽度派发，不会突然升到 4 路。
  assert.equal(effectiveUploadConcurrency(4, undefined), UPLOAD_CONCURRENCY_DEFAULT)
})
