// 进度文案与轮询判定，集中管理状态映射，避免界面散落魔法字符串。
import type { ServerProgress } from '../api/posts.ts'

// 帖子状态文案，与后端状态取值一一对应。
const POST_LABELS: Record<string, string> = {
  uploading: '正在传至服务器',
  queued: '排队存入频道',
  publishing: '正在存入频道',
  completed: '已存入频道',
  failed: '部分存图失败',
}

// 单张图片状态文案。
const IMAGE_LABELS: Record<string, string> = {
  pending: '等待上传',
  uploaded: '已传至服务器',
  stored: '已存入频道',
  failed: '转存失败',
}

// 需要继续轮询的状态；完成与失败后停止。
const ACTIVE_STATUSES = new Set(['uploading', 'queued', 'publishing'])

export function postStatusLabel(status: string): string {
  return POST_LABELS[status] ?? status
}

export function imageStatusLabel(status: string): string {
  return IMAGE_LABELS[status] ?? status
}

export function isActiveStatus(status: string): boolean {
  return ACTIVE_STATUSES.has(status)
}

// progressPercent 返回已存图占比，0–100 的整数。
export function progressPercent(progress: ServerProgress): number {
  if (progress.image_count <= 0) return 0
  const done = Math.min(progress.stored_count, progress.image_count)
  return Math.round((done / progress.image_count) * 100)
}

// needsRetry 判断是否可以手动重试：有失败图片且任务未在活跃状态。
export function needsRetry(progress: ServerProgress): boolean {
  return progress.failed_count > 0 && !isActiveStatus(progress.status)
}

// summaryText 汇总当前进度，用于界面上的一句话说明。
export function summaryText(progress: ServerProgress): string {
  const total = progress.image_count
  const stored = progress.stored_count
  if (progress.status === 'completed') {
    return `共 ${total} 项已全部存入频道`
  }
  if (progress.status === 'failed') {
    return `已存入频道 ${stored} / ${total} 项，${progress.failed_count} 项失败可重试`
  }
  if (progress.status === 'queued') {
    return `已排队等待转存频道（已完成 ${stored} / ${total} 项）`
  }
  return `正在存入频道，已完成 ${stored} / ${total} 项`
}

// 发布状态文案。
const PUBLISH_LABELS: Record<string, string> = {
  idle: '未发布',
  pending: '等待发布',
  publishing: '正在发布',
  published: '已发布',
  failed: '发布失败',
}

export function publishStateLabel(state: string): string {
  return PUBLISH_LABELS[state] ?? state
}

// publishActive 表示发布任务仍在进行，需要继续轮询。
export function publishActive(progress: ServerProgress): boolean {
  return progress.publish_state === 'pending' || progress.publish_state === 'publishing'
}

// canRequestPublish 判断是否可以发起或重试发布。
export function canRequestPublish(progress: ServerProgress): boolean {
  if (progress.status !== 'completed') return false
  if (progress.control_state === 'paused' || progress.control_state === 'stopped') return false
  if (progress.public) return false
  return progress.publish_state === 'idle' || progress.publish_state === 'failed'
}

// publishSummary 汇总发布状态文案。
export function publishSummary(progress: ServerProgress): string {
  if (progress.publish_state === 'published') {
    return `已发布 ${progress.telegraph_urls.length} 个页面`
  }
  if (progress.publish_state === 'failed') {
    return '发布失败，可重新发布（已创建的页面会复用）'
  }
  if (publishActive(progress)) {
    return '正在发布到 Telegraph…'
  }
  return '发布后会生成 Telegraph 分享链接'
}
