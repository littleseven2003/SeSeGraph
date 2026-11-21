import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  BLUR_STORAGE_KEY,
  DEFAULT_SHARE_TEMPLATE,
  buildGalleryQuery,
  formatFileSize,
  formatPublishedAt,
  createLatestGuard,
  focusStep,
  imageShareUrl,
  normalizeShareTemplate,
  pageWindow,
  postShareUrl,
  readShareTemplate,
  saveShareTemplate,
  readBlurPreference,
  renderShareText,
  saveBlurPreference,
  statusLabel,
} from '../src/utils/gallery.ts'

test('buildGalleryQuery 去掉空关键词并保留多标签', () => {
  const query = buildGalleryQuery({ q: ' 海边 ', tags: ['写真', '', '海边'], page: 2, pageSize: 12 })
  const params = new URLSearchParams(query)
  assert.equal(params.get('q'), '海边')
  assert.deepEqual(params.getAll('tag'), ['写真', '海边'])
  assert.equal(params.get('page'), '2')
  assert.equal(params.get('page_size'), '12')
})

test('buildGalleryQuery 空关键词不下发 q', () => {
  const params = new URLSearchParams(buildGalleryQuery({ q: '   ' }))
  assert.equal(params.has('q'), false)
  assert.equal(params.get('page'), '1')
  assert.equal(params.get('page_size'), '12')
})

test('formatPublishedAt 输出本地时间并对空值返回空串', () => {
  assert.equal(formatPublishedAt(''), '')
  assert.equal(formatPublishedAt('不是时间'), '')
  const formatted = formatPublishedAt('2026-11-21T21:43:42Z')
  assert.match(formatted, /^\d{4}-\d{2}-\d{2} \d{2}:\d{2}$/)
})

test('statusLabel 撤回优先且覆盖发布各态', () => {
  assert.deepEqual(statusLabel('completed', 'published', true), { label: '已撤回', tone: 'error' })
  assert.deepEqual(statusLabel('completed', 'published'), { label: '已发布', tone: 'ok' })
  assert.deepEqual(statusLabel('completed', 'failed'), { label: '发布失败', tone: 'error' })
  assert.deepEqual(statusLabel('completed', 'pending'), { label: '待发布', tone: 'progress' })
  assert.deepEqual(statusLabel('publishing', 'idle'), { label: '存图中', tone: 'progress' })
  assert.deepEqual(statusLabel('completed', 'idle'), { label: '待发布', tone: 'idle' })
  assert.deepEqual(statusLabel('failed', 'idle'), { label: '存图失败', tone: 'error' })
  assert.deepEqual(statusLabel('uploading', 'idle'), { label: '上传中', tone: 'progress' })
})

test('renderShareText 替换全部占位符', () => {
  const text = renderShareText(DEFAULT_SHARE_TEMPLATE, {
    title: '海边写真',
    description: '夏日海滩',
    url: 'https://telegra.ph/Page-1',
    count: 12,
    tags: ['写真', '海边'],
  })
  assert.equal(text, '海边写真 · 12 张图\nhttps://telegra.ph/Page-1\n#写真 #海边')
})

test('renderShareText 保留未知占位符', () => {
  const text = renderShareText('{{title}} / {{unknown}}', {
    title: '标题',
    description: '',
    url: '',
    count: 0,
    tags: [],
  })
  assert.equal(text, '标题 / {{unknown}}')
})

test('postShareUrl 只在发布完成且互链就绪时给出链接', () => {
  const published = {
    publish_state: 'published',
    public: true,
    disabled: false,
    telegraph_urls: ['https://telegra.ph/Page-1'],
  }
  assert.equal(postShareUrl(published), 'https://telegra.ph/Page-1')
  assert.equal(postShareUrl({ ...published, publish_state: 'pending' }), '')
  assert.equal(postShareUrl({ ...published, public: false }), '')
  assert.equal(postShareUrl({ ...published, disabled: true }), '')
  assert.equal(postShareUrl({ ...published, telegraph_urls: [] }), '')
  assert.equal(postShareUrl({ ...published, telegraph_urls: ['  '] }), '')
})

test('imageShareUrl 按公开状态、存图状态与公开地址拼接', () => {
  const post = { public: true, disabled: false }
  const stored = { short_code: 'abc12345', status: 'stored' }
  assert.equal(imageShareUrl(post, stored, 'https://img.example.com/'), 'https://img.example.com/i/abc12345')
  assert.equal(imageShareUrl(post, stored, 'https://img.example.com/base'), 'https://img.example.com/base/i/abc12345')
  assert.equal(imageShareUrl({ ...post, disabled: true }, stored, 'https://img.example.com'), '')
  assert.equal(imageShareUrl({ ...post, public: false }, stored, 'https://img.example.com'), '')
  assert.equal(imageShareUrl(post, { short_code: 'abc12345', status: 'uploaded' }, 'https://img.example.com'), '')
  assert.equal(imageShareUrl(post, { short_code: '', status: 'stored' }, 'https://img.example.com'), '')
  assert.equal(imageShareUrl(post, stored, ''), '')
})

test('createLatestGuard 丢弃迟到的旧响应', async () => {
  const guard = createLatestGuard()
  const first = guard.begin()
  const second = guard.begin()
  assert.equal(guard.isCurrent(first), false)
  assert.equal(guard.isCurrent(second), true)

  // 模拟 A 慢、B 快：B 的结果生效后 A 才返回，A 必须被丢弃。
  const order: string[] = []
  const respond = (token: number, name: string, delay: number): Promise<void> =>
    new Promise((resolve) =>
      setTimeout(() => {
        if (guard.isCurrent(token)) order.push(name)
        resolve()
      }, delay),
    )
  const slow = respond(guard.begin(), 'A', 20)
  const fast = respond(guard.begin(), 'B', 1)
  await Promise.all([slow, fast])
  assert.deepEqual(order, ['B'])
})

test('formatFileSize 覆盖字节档位', () => {
  assert.equal(formatFileSize(0), '0 KB')
  assert.equal(formatFileSize(2048), '2 KB')
  assert.equal(formatFileSize(3 * 1024 * 1024), '3.0 MB')
})

test('模糊偏好默认开启并能持久化关闭', () => {
  const store = new Map<string, string>()
  const storage = {
    getItem: (key: string) => store.get(key) ?? null,
    setItem: (key: string, value: string) => void store.set(key, value),
  }
  assert.equal(readBlurPreference(storage), true)
  saveBlurPreference(storage, false)
  assert.equal(store.get(BLUR_STORAGE_KEY), 'off')
  assert.equal(readBlurPreference(storage), false)
  assert.equal(readBlurPreference(undefined), true)
})

test('pageWindow 最多五页并夹在当前页附近', () => {
  assert.deepEqual(pageWindow(1, 12, 0), [1])
  assert.deepEqual(pageWindow(1, 12, 60), [1, 2, 3, 4, 5])
  assert.deepEqual(pageWindow(5, 12, 240), [3, 4, 5, 6, 7])
  assert.deepEqual(pageWindow(20, 12, 240), [16, 17, 18, 19, 20])
})

test('分享模板默认值、持久化与恢复默认', () => {
  const store = new Map<string, string>()
  const storage = {
    getItem: (key: string) => store.get(key) ?? null,
    setItem: (key: string, value: string) => void store.set(key, value),
  }
  assert.equal(readShareTemplate(storage), DEFAULT_SHARE_TEMPLATE)

  const custom = '{{title}}（{{count}} 张）\n{{url}}'
  assert.equal(saveShareTemplate(storage, custom), custom)
  assert.equal(readShareTemplate(storage), custom)

  assert.equal(normalizeShareTemplate('   '), DEFAULT_SHARE_TEMPLATE)
  assert.equal(saveShareTemplate(storage, '  '), DEFAULT_SHARE_TEMPLATE)
  assert.equal(readShareTemplate(storage), DEFAULT_SHARE_TEMPLATE)

  const broken = {
    getItem: () => {
      throw new Error('blocked')
    },
    setItem: () => {
      throw new Error('blocked')
    },
  }
  assert.equal(readShareTemplate(broken), DEFAULT_SHARE_TEMPLATE)
  assert.equal(saveShareTemplate(broken, custom), custom)
})

test('focusStep 在首尾循环并处理无焦点元素', () => {
  assert.equal(focusStep(0, 3, true), 2)
  assert.equal(focusStep(2, 3, false), 0)
  assert.equal(focusStep(1, 3, false), 2)
  assert.equal(focusStep(1, 3, true), 0)
  assert.equal(focusStep(-1, 3, false), 0)
  assert.equal(focusStep(-1, 3, true), 2)
  assert.equal(focusStep(0, 0, false), -1)
})
