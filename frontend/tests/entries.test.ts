import assert from 'node:assert/strict'
import { test } from 'node:test'
import { collectEntryFiles, readAllBatches, type CollectedFiles, type DirectoryReader } from '../src/utils/entries.ts'

function fakeFile(name: string, fail = false): FileSystemEntry {
  return {
    isFile: true,
    isDirectory: false,
    name,
    file: (success: (file: File) => void, error: () => void) => {
      if (fail) {
        error()
        return
      }
      success(new File([new Uint8Array([1, 2, 3])], name, { type: 'image/jpeg' }))
    },
  } as unknown as FileSystemEntry
}

function fakeDirectory(name: string, batches: FileSystemEntry[][], failFirst = false): FileSystemEntry {
  let index = 0
  let failed = false
  const reader: DirectoryReader = {
    readEntries(success, error) {
      if (failFirst && !failed) {
        failed = true
        error?.(new Error('read failed'))
        return
      }
      success(batches[index] ?? [])
      index += 1
    },
  }
  return {
    isFile: false,
    isDirectory: true,
    name,
    createReader: () => reader,
  } as unknown as FileSystemEntry
}

function empty(): CollectedFiles {
  return { files: [], failed: [] }
}

test('目录读取循环到空批次，101 个文件跨批全部收录', async () => {
  const first = Array.from({ length: 100 }, (_, index) => fakeFile(`IMG_${index + 1}.jpg`))
  const second = [fakeFile('IMG_101.jpg')]
  const result = empty()
  await collectEntryFiles(fakeDirectory('photos', [first, second]), result)
  assert.equal(result.files.length, 101)
  assert.equal(result.failed.length, 0)
})

test('逐条读取失败会记入 failed 而不是静默丢弃', async () => {
  const result = empty()
  await collectEntryFiles(fakeDirectory('photos', [[fakeFile('a.jpg'), fakeFile('broken.jpg', true)]]), result)
  assert.deepEqual(result.files.map((file) => file.name), ['a.jpg'])
  assert.deepEqual(result.failed, ['broken.jpg'])
})

test('readEntries 首次失败会重试一次', async () => {
  const reader: DirectoryReader = (() => {
    const inner = { count: 0 }
    return {
      readEntries(success, error) {
        inner.count += 1
        if (inner.count === 1) {
          error?.(new Error('boom'))
          return
        }
        success(inner.count === 2 ? [fakeFile('a.jpg')] : [])
      },
    }
  })()
  const entries = await readAllBatches(reader)
  assert.equal(entries.length, 1)
})

test('嵌套目录递归收集', async () => {
  const nested = fakeDirectory('inner', [[fakeFile('deep.jpg')]])
  const outer = fakeDirectory('photos', [[fakeDirectory('sub', [[nested]]), fakeFile('top.jpg')]])
  const result = empty()
  await collectEntryFiles(outer, result)
  assert.deepEqual(result.files.map((file) => file.name).sort(), ['deep.jpg', 'top.jpg'])
})
