// 任务列表的分段筛选与三阶段展示规则，独立于页面便于逐条验证。
import type { TaskFilter } from '../api/posts.ts'

// 筛选顺序即界面顺序：全部在最前，其余按任务生命周期排列。
export const TASK_FILTERS: { value: TaskFilter; label: string }[] = [
  { value: '', label: '全部' },
  { value: 'active', label: '进行中' },
  { value: 'paused', label: '已暂停' },
  { value: 'stopped', label: '已停止' },
  { value: 'finished', label: '已完成' },
]

export type TaskTone = 'run' | 'warn' | 'danger' | 'ok' | 'muted'

// TaskState 是任务行里与展示有关的字段，便于脱离接口单独判定。
export interface TaskState {
  status: string
  publish_state: string
  control_state: string
  image_count: number
  /** 兼容口径，仅上传加已入频道 */
  stored_count: number
  /** 真实已接收，含已接收后存频道失败 */
  received_count: number
  channel_count: number
}

export interface TaskStage {
  value: string
  tone: TaskTone
}

// uploadStage 只看已接收张数：仍在上传与已入频道的图片都算接收成功。
export function uploadStage(task: TaskState): TaskStage {
  if (task.image_count <= 0) return { value: '无图片', tone: 'muted' }
  const received = task.received_count
  if (received >= task.image_count) return { value: '已完成', tone: 'ok' }
  if (task.status === 'failed') return { value: `${received}/${task.image_count} 张`, tone: 'danger' }
  return { value: `${received}/${task.image_count} 张`, tone: received > 0 ? 'run' : 'muted' }
}

// channelStage 只认确实已入频道的张数，不与已接收合并。
export function channelStage(task: TaskState): TaskStage {
  if (task.image_count <= 0) return { value: '无图片', tone: 'muted' }
  const stored = task.channel_count
  if (stored >= task.image_count) return { value: '已完成', tone: 'ok' }
  if (task.status === 'failed') return { value: `${stored}/${task.image_count} 张`, tone: 'danger' }
  return { value: `${stored}/${task.image_count} 张`, tone: stored > 0 ? 'run' : 'muted' }
}

// publishStage 只看 publish_state，不借用前两段的状态。
export function publishStage(task: TaskState): TaskStage {
  switch (task.publish_state) {
    case 'published':
      return { value: '已发布', tone: 'ok' }
    case 'publishing':
      return { value: '发布中', tone: 'run' }
    case 'pending':
      return { value: '待发布', tone: 'run' }
    case 'failed':
      return { value: '发布失败', tone: 'danger' }
    default:
      return { value: '未开始', tone: 'muted' }
  }
}

// badgeOf 给出整体状态胶囊：暂停与停止优先，其次才是完成与运行中的细分。
export function badgeOf(task: TaskState): { label: string; tone: TaskTone } {
  if (task.control_state === 'stopped') return { label: '已停止', tone: 'danger' }
  if (task.control_state === 'paused') return { label: '已暂停', tone: 'warn' }
  if (task.publish_state === 'published') return { label: '已完成', tone: 'ok' }
  if (task.publish_state === 'failed' || task.status === 'failed') return { label: '失败', tone: 'danger' }
  if (task.publish_state === 'publishing') return { label: '发布中', tone: 'run' }
  if (task.status === 'uploading') return { label: '传输中', tone: 'run' }
  if (task.status === 'queued' || task.status === 'publishing') return { label: '存频道中', tone: 'run' }
  if (task.publish_state === 'pending') return { label: '待发布', tone: 'run' }
  return { label: '待处理', tone: 'muted' }
}

// canPause 与后端一致：暂停只对仍在运行且有待处理阶段的任务开放。
export function canPause(task: TaskState): boolean {
  if (task.control_state !== 'running') return false
  return ['uploading', 'queued', 'publishing'].includes(task.status) ||
    ['pending', 'publishing'].includes(task.publish_state)
}

export function canResume(task: TaskState): boolean {
  return task.control_state === 'paused'
}

// 已完成的任务没有停止入口；停止是终态，不再重复提供。
export function canStop(task: TaskState): boolean {
  return task.control_state !== 'stopped' && task.publish_state !== 'published'
}

// 只有仍在往服务器传的任务才叫「继续上传」；频道存图失败走重试入口。
export function canContinueUpload(task: TaskState): boolean {
  if (task.control_state !== 'running' || task.status !== 'uploading') return false
  return task.image_count > 0 && task.received_count < task.image_count
}

// 存频道失败的任务已收齐图片，只能在频道侧重试，不再补传文件。
export function canRetryStore(task: TaskState): boolean {
  if (task.control_state === 'stopped') return false
  if (task.status !== 'failed') return false
  return task.received_count > task.channel_count
}
