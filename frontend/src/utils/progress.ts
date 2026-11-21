// 进度文案与轮询判定，集中管理状态映射，避免界面散落魔法字符串。
import type { ServerProgress } from '../api/posts.ts'

// 帖子状态文案，与后端状态取值一一对应。
const POST_LABELS: Record<string, string> = {
  uploading: '上传中',
  queued: '排队中',
  publishing: '发布中',
  completed: '存图完成',
  failed: '失败',
}

// 单张图片状态文案。
const IMAGE_LABELS: Record<string, string> = {
  pending: '等待上传',
  uploaded: '已落盘',
  stored: '已存图',
  failed: '失败',
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
    return `存图完成，共 ${total} 张已进入频道`
  }
  if (progress.status === 'failed') {
    return `已存 ${stored} / ${total} 张，有 ${progress.failed_count} 张需要重试`
  }
  if (progress.status === 'queued') {
    return `已入队，等待服务端处理（已存 ${stored} / ${total} 张）`
  }
  return `正在存图，已存 ${stored} / ${total} 张`
}
