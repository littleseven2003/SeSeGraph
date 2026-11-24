import assert from 'node:assert/strict'
import test from 'node:test'
import { createSerialQueue } from '../src/utils/serial.ts'

test('串行队列任意时刻只跑一个任务且保持入队顺序', async () => {
  const queue = createSerialQueue()
  const events: string[] = []
  let running = 0
  let peak = 0
  const task = (name: string, delay: number) => async () => {
    running += 1
    peak = Math.max(peak, running)
    events.push('start:' + name)
    await new Promise((resolve) => setTimeout(resolve, delay))
    events.push('end:' + name)
    running -= 1
  }
  queue.push(task('a', 30))
  queue.push(task('b', 5))
  queue.push(task('c', 5))
  assert.equal(queue.size(), 3)
  await queue.idle()
  assert.equal(peak, 1, '不应同时执行两个任务')
  assert.deepEqual(events, ['start:a', 'end:a', 'start:b', 'end:b', 'start:c', 'end:c'])
  assert.equal(queue.size(), 0)
})

test('任务失败不会打断后续任务', async () => {
  const queue = createSerialQueue()
  const done: string[] = []
  await queue.push(async () => {
    throw new Error('生成失败')
  })
  await queue.push(async () => {
    done.push('ok')
  })
  assert.deepEqual(done, ['ok'])
})
