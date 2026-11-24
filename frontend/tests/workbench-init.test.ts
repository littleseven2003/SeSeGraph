import test from 'node:test'
import assert from 'node:assert/strict'
import {
  canLeaveResume,
  defaultsFromSettings,
  fallbackDefaults,
  loadWorkbenchInitialization,
} from '../src/utils/workbench-init.ts'
import type { SettingsView } from '../src/api/settings.ts'
import type { ServerProgress } from '../src/api/posts.ts'

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

const tick = () => new Promise((resolve) => setTimeout(resolve, 5))

function settings(overrides: Partial<SettingsView> = {}): SettingsView {
  return {
    public_url: 'https://sese.example.com',
    telegram: { token_set: true, token_source: 'stored', channel_id: '@c', channel_source: 'stored', api_base: '', api_base_source: 'default' },
    telegraph: { token_set: true, token_source: 'stored', author_name: 'SeSe', author_source: 'stored', api_base: '', api_base_source: 'default' },
    proxy: { mode: 'direct', mode_source: 'default', url_set: false, url_source: 'none', telegram_enabled: false, telegraph_enabled: false },
    upload: { default_mode: 'document', mode_source: 'stored' },
    meta: { filenames: ['Info.TXT', 'notes.md'], parse_mode: 'key-value', source: 'stored' },
    env_managed: [],
    ...overrides,
  }
}

function progress(mode: string): ServerProgress {
  return {
    id: 7, status: 'uploading', tags: [], image_count: 2, upload_mode: mode,
    stored_count: 1, failed_count: 0, images: [], publish_state: 'idle', public: false, telegraph_urls: [],
  }
}

const snapshot = { postId: 7, imageCount: 2, files: [{ name: 'a.jpg', size: 10 }], createdAt: 1 }

test('默认规则带上服务端上传上限', () => {
  const view = settings()
  view.upload.concurrency = 3
  const defaults = defaultsFromSettings(view)
  assert.equal(defaults.uploadConcurrency, 3)
})

test('服务端上限缺失时回落默认宽度', () => {
  const defaults = defaultsFromSettings(settings())
  assert.equal(defaults.uploadConcurrency, 2)
  assert.equal(fallbackDefaults().uploadConcurrency, 2)
})

test('设置读取延迟时初始化不会提前返回', async () => {
  const settingsCall = deferred<SettingsView>()
  const initPromise = loadWorkbenchInitialization({
    loadSettings: () => settingsCall.promise,
    loadProgress: async () => progress('photo'),
    snapshot: null,
  })

  let settled = false
  void initPromise.then(() => {
    settled = true
  })
  await tick()
  assert.equal(settled, false)
  settingsCall.resolve(settings())
  const init = await initPromise
  assert.deepEqual(init.defaults.metaNames, ['Info.TXT', 'notes.md'])
  assert.equal(init.defaults.parseMode, 'key-value')
  assert.equal(init.defaults.defaultMode, 'document')
  assert.equal(init.progress, null)
})

test('续传画质在进度返回后才确定，覆盖全局默认', async () => {
  const progressCall = deferred<ServerProgress>()
  const initPromise = loadWorkbenchInitialization({
    loadSettings: async () => settings(),
    loadProgress: () => progressCall.promise,
    snapshot,
  })

  let settled = false
  void initPromise.then(() => {
    settled = true
  })
  await tick()
  assert.equal(settled, false)
  progressCall.resolve(progress('document'))
  const init = await initPromise
  assert.equal(init.resumeMode, 'document')
  assert.equal(init.defaults.defaultMode, 'document')
  assert.ok(init.progress)
})

test('设置或进度失败时回落内置默认且不阻塞发帖', async () => {
  const init = await loadWorkbenchInitialization({
    loadSettings: async () => {
      throw new Error('offline')
    },
    loadProgress: async () => {
      throw new Error('gone')
    },
    snapshot,
  })
  const fallback = fallbackDefaults()
  assert.deepEqual(init.defaults, fallback)
  assert.equal(init.progress, null)
  assert.equal(init.resumeMode, null)
})

test('空文件名与非法解析方式回落默认', () => {
  const defaults = defaultsFromSettings(settings({
    meta: { filenames: [], parse_mode: 'regex', source: 'stored' },
    upload: { default_mode: 'ultra', mode_source: 'stored' },
  }))
  assert.deepEqual(defaults.metaNames, ['info.txt', 'meta.txt', 'readme.txt'])
  assert.equal(defaults.parseMode, 'first-line')
  assert.equal(defaults.defaultMode, 'photo')
})

test('初始化完成前不能放弃续传，旧任务不会复活', async () => {
  const progressCall = deferred<ServerProgress>()
  const initPromise = loadWorkbenchInitialization({
    loadSettings: async () => settings(),
    loadProgress: () => progressCall.promise,
    snapshot,
  })

  // 进度还没回来时，放弃入口必须保持禁用，避免清掉快照后旧任务被回填。
  assert.equal(canLeaveResume(false, 'queued'), false)
  progressCall.resolve(progress('document'))
  await initPromise
  assert.equal(canLeaveResume(true, 'queued'), true)
  // 上传进行中仍然禁止放弃。
  assert.equal(canLeaveResume(true, 'uploading'), false)
})

test('进度读取失败与任务不存在分开上报', async () => {
  const offline = await loadWorkbenchInitialization({
    loadSettings: async () => settings(),
    loadProgress: async () => {
      throw new Error('network down')
    },
    snapshot,
  })
  assert.equal(offline.progress, null)
  assert.ok(offline.progressError instanceof Error, '网络失败要能区分')

  const online = await loadWorkbenchInitialization({
    loadSettings: async () => settings(),
    loadProgress: async () => progress('document'),
    snapshot,
  })
  assert.equal(online.progressError, null)
})
