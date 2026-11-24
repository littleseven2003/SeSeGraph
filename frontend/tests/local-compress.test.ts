import assert from 'node:assert/strict'
import { test } from 'node:test'
import { LocalCompressScheduler } from '../src/utils/local-compress.ts'
import { createSerialQueue } from '../src/utils/serial.ts'

const tick = () => new Promise((resolve) => setTimeout(resolve, 5))

interface Harness {
  alive: Set<string>
  ran: string[]
  gate: (() => void) | null
  scheduler: LocalCompressScheduler
  queue: ReturnType<typeof createSerialQueue>
}

function harness(options: { gated?: boolean } = {}): Harness {
  const queue = createSerialQueue()
  const alive = new Set<string>()
  const ran: string[] = []
  let gate: (() => void) | null = null
  const scheduler = new LocalCompressScheduler(queue, {
    isAlive: (id) => alive.has(id),
    run: async (id) => {
      ran.push(id)
      if (options.gated === true) {
        await new Promise<void>((resolve) => {
          gate = resolve
        })
      }
    },
  })
  return { alive, ran, get gate() { return gate }, scheduler, queue } as Harness
}

test('同一项重复申请只压一次，重排不会重复排队', async () => {
  const h = harness()
  h.alive.add('a')
  h.scheduler.request('a')
  h.scheduler.request('a')
  await tick()
  assert.deepEqual(h.ran, ['a'])
  assert.equal(h.scheduler.size(), 0)
})

test('生成中被删除的项回写前作废', async () => {
  const h = harness({ gated: true })
  h.alive.add('a')
  h.scheduler.request('a')
  await tick()
  h.alive.delete('a')
  h.gate?.()
  await tick()
  assert.equal(h.scheduler.size(), 0)
})

test('换模式或清空后丢掉在途结果，新 ID 照常处理', async () => {
  const h = harness({ gated: true })
  h.alive.add('a')
  h.scheduler.request('a')
  await tick()
  h.alive.clear()
  h.scheduler.reset()
  h.gate?.()
  await tick()
  assert.equal(h.scheduler.has('a'), false)

  h.alive.add('b')
  h.scheduler.request('b')
  await tick()
  assert.deepEqual(h.ran, ['a', 'b'])
})

test('离页后不再接受新申请', async () => {
  const h = harness()
  h.scheduler.dispose()
  h.alive.add('a')
  h.scheduler.request('a')
  await tick()
  assert.deepEqual(h.ran, [])
})

test('提交前可以等队列清空，避免同一张图压两次', async () => {
  const h = harness()
  h.alive.add('a')
  h.scheduler.request('a')
  await h.scheduler.idle()
  assert.deepEqual(h.ran, ['a'])
})
