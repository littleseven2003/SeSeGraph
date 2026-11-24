import assert from 'node:assert/strict'
import { test } from 'node:test'
import { captureVideoFrame, isVideoBlob } from '../src/utils/video-frame.ts'

// 用最小的假播放器替代真实 DOM，验证取消时一定走清理分支。
interface FakeDom {
  created: string[]
  revoked: string[]
  removedAttrs: string[]
  loads: number
  restore: () => void
}

function installFakeDom(): FakeDom {
  const created: string[] = []
  const revoked: string[] = []
  const removedAttrs: string[] = []
  const state = { loads: 0 }
  // 播放器永远不触发 loadedmetadata，模拟取帧卡在上游。
  const video = {
    preload: '',
    muted: false,
    playsInline: false,
    src: '',
    duration: 0,
    videoWidth: 0,
    videoHeight: 0,
    onloadedmetadata: null as (() => void) | null,
    onseeked: null as (() => void) | null,
    onerror: null as (() => void) | null,
    removeAttribute(name: string): void {
      removedAttrs.push(name)
      if (name === 'src') this.src = ''
    },
    load(): void {
      state.loads += 1
    },
  }
  const originalCreate = URL.createObjectURL
  const originalRevoke = URL.revokeObjectURL
  const originalDocument = (globalThis as { document?: unknown }).document
  URL.createObjectURL = (() => {
    const url = `blob:fake-${created.length + 1}`
    created.push(url)
    return url
  }) as typeof URL.createObjectURL
  URL.revokeObjectURL = ((url: string) => {
    revoked.push(url)
  }) as typeof URL.revokeObjectURL
  ;(globalThis as { document?: unknown }).document = { createElement: () => video }
  return {
    created,
    revoked,
    removedAttrs,
    get loads() {
      return state.loads
    },
    restore(): void {
      URL.createObjectURL = originalCreate
      URL.revokeObjectURL = originalRevoke
      if (originalDocument === undefined) delete (globalThis as { document?: unknown }).document
      else (globalThis as { document?: unknown }).document = originalDocument
    },
  } as FakeDom
}

test('已取消的信号不会创建播放器与对象 URL', async () => {
  const dom = installFakeDom()
  try {
    const controller = new AbortController()
    controller.abort()
    await assert.rejects(
      captureVideoFrame(new Blob([], { type: 'video/mp4' }), { signal: controller.signal }),
      /视频取帧已取消/,
    )
    assert.equal(dom.created.length, 0, '取消后不应再建对象 URL')
  } finally {
    dom.restore()
  }
})

test('取帧途中取消会断开数据源并回收对象 URL', async () => {
  const dom = installFakeDom()
  try {
    const controller = new AbortController()
    const pending = captureVideoFrame(new Blob([], { type: 'video/mp4' }), { signal: controller.signal })
    // 让播放器先进入等待，再取消。
    await new Promise((resolve) => setTimeout(resolve, 10))
    assert.equal(dom.created.length, 1, '开始时建立了一个对象 URL')
    controller.abort()
    await assert.rejects(pending, /视频取帧已取消/)
    assert.deepEqual(dom.revoked, dom.created, '对象 URL 必须被回收')
    assert.ok(dom.removedAttrs.includes('src'), '必须移除 src 断开数据源')
    assert.equal(dom.loads, 1, '必须调用 load() 停掉加载')
  } finally {
    dom.restore()
  }
})

test('取消后不会误判为非视频', () => {
  assert.equal(isVideoBlob(new Blob([], { type: 'video/mp4' })), true)
})
