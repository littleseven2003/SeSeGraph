import { test } from 'node:test'
import assert from 'node:assert/strict'
import {
  canRequestPublish,
  imageStatusLabel,
  isActiveStatus,
  needsRetry,
  postStatusLabel,
  progressPercent,
  publishActive,
  publishStateLabel,
  publishSummary,
  summaryText,
} from '../src/utils/progress.ts'
import type { ServerProgress } from '../src/api/posts.ts'

function make(status: string, stored: number, failed: number, total = 4): ServerProgress {
  return {
    id: 1,
    status,
    tags: [],
    image_count: total,
    stored_count: stored,
    failed_count: failed,
    images: [],
    publish_state: 'idle',
    public: false,
    telegraph_urls: [],
  }
}

function withPublish(progress: ServerProgress, state: string, publicFlag: boolean, urls: string[] = []): ServerProgress {
  return { ...progress, publish_state: state, public: publicFlag, telegraph_urls: urls }
}

test('帖子与图片状态文案', () => {
  assert.equal(postStatusLabel('completed'), '存图完成')
  assert.equal(postStatusLabel('queued'), '排队中')
  assert.equal(postStatusLabel('unknown'), 'unknown')
  assert.equal(imageStatusLabel('stored'), '已存图')
  assert.equal(imageStatusLabel('failed'), '失败')
})

test('活跃状态需要继续轮询', () => {
  assert.equal(isActiveStatus('queued'), true)
  assert.equal(isActiveStatus('publishing'), true)
  assert.equal(isActiveStatus('completed'), false)
  assert.equal(isActiveStatus('failed'), false)
})

test('进度百分比按已存图计算', () => {
  assert.equal(progressPercent(make('queued', 1, 0)), 25)
  assert.equal(progressPercent(make('completed', 4, 0)), 100)
  assert.equal(progressPercent(make('queued', 0, 0, 0)), 0)
})

test('只有失败且静止时才提示重试', () => {
  assert.equal(needsRetry(make('failed', 2, 2)), true)
  assert.equal(needsRetry(make('completed', 4, 0)), false)
  assert.equal(needsRetry(make('queued', 2, 2)), false)
})

test('汇总文案覆盖完成、失败与进行中', () => {
  assert.match(summaryText(make('completed', 4, 0)), /存图完成/)
  assert.match(summaryText(make('failed', 2, 2)), /2 张需要重试/)
  assert.match(summaryText(make('queued', 2, 0)), /已入队/)
  assert.match(summaryText(make('uploading', 1, 0)), /已存 1 \/ 4/)
})

test('发布状态文案与进行中判断', () => {
  assert.equal(publishStateLabel('published'), '已发布')
  assert.equal(publishStateLabel('publishing'), '发布中')
  const base = make('completed', 4, 0)
  assert.equal(publishActive(withPublish(base, 'pending', false)), true)
  assert.equal(publishActive(withPublish(base, 'published', true)), false)
})

test('只有存图完成且未公开时才能发起或重试发布', () => {
  const completed = make('completed', 4, 0)
  assert.equal(canRequestPublish(withPublish(completed, 'idle', false)), true)
  assert.equal(canRequestPublish(withPublish(completed, 'failed', false)), true)
  assert.equal(canRequestPublish(withPublish(completed, 'pending', false)), false)
  assert.equal(canRequestPublish(withPublish(completed, 'published', true)), false)
  assert.equal(canRequestPublish(withPublish(make('uploading', 1, 0), 'idle', false)), false)
})

test('发布汇总文案覆盖完成、失败与进行中', () => {
  const base = make('completed', 4, 0)
  assert.match(publishSummary(withPublish(base, 'published', true, ['https://telegra.ph/a', 'https://telegra.ph/b'])), /已发布 2 个页面/)
  assert.match(publishSummary(withPublish(base, 'failed', false)), /重新发布/)
  assert.match(publishSummary(withPublish(base, 'publishing', false)), /正在发布/)
  assert.match(publishSummary(withPublish(base, 'idle', false)), /分享链接/)
})
