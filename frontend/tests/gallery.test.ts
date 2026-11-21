import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  BLUR_STORAGE_KEY,
  DEFAULT_SHARE_TEMPLATE,
  buildGalleryQuery,
  formatFileSize,
  formatPublishedAt,
  imagePublicUrl,
  pageWindow,
  primaryShareUrl,
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

test('primaryShareUrl 优先 Telegraph 链接并回落到首图短码', () => {
  const origin = 'https://img.example.com/'
  assert.equal(
    primaryShareUrl({ telegraph_urls: ['https://telegra.ph/Page-1'], images: [{ short_code: 'abc12345' }] }, origin),
    'https://telegra.ph/Page-1',
  )
  assert.equal(primaryShareUrl({ telegraph_urls: [], images: [{ short_code: 'abc12345' }] }, origin), 'https://img.example.com/i/abc12345')
  assert.equal(primaryShareUrl({ telegraph_urls: [], images: [] }, origin), '')
  assert.equal(imagePublicUrl('abc12345', 'https://img.example.com/'), 'https://img.example.com/i/abc12345')
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
