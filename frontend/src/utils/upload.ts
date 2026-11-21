// 上传计划：根据服务端进度决定需要补传哪些序号。
import type { ServerProgress } from '../api/posts.ts'

// 单张图片在本地的上传状态。
export type UploadStatus = 'pending' | 'uploading' | 'uploaded' | 'failed'

// 需要上传的序号。
export interface UploadPlan {
  /** 待上传任务的本地下标与序号 */
  pending: { index: number; sortOrder: number }[]
  /** 服务端已收录、无需重复上传的数量 */
  skipped: number
  total: number
}

export type PlanResult = { plan: UploadPlan } | { error: string }

// 服务端已收录的状态：uploaded 表示已落盘，stored 表示已进频道。
const DONE_STATUSES = new Set(['uploaded', 'stored'])

// planUploads 按序号对齐本地文件与服务端记录，只补传缺失的图片。
// 本地数量与服务端声明不一致时直接报错，避免错位覆盖。
export function planUploads(progress: ServerProgress, localCount: number): PlanResult {
  if (progress.image_count !== localCount) {
    return {
      error: `服务器记录的图片数量（${progress.image_count}）与本次选择的文件数量（${localCount}）不一致，请重新选择同一批文件`,
    }
  }
  const byOrder = new Map<number, string>()
  for (const image of progress.images) {
    byOrder.set(image.sort_order, image.status)
  }

  const pending: UploadPlan['pending'] = []
  let skipped = 0
  for (let index = 0; index < localCount; index += 1) {
    const sortOrder = index + 1
    const status = byOrder.get(sortOrder)
    if (status && DONE_STATUSES.has(status)) {
      skipped += 1
      continue
    }
    pending.push({ index, sortOrder })
  }
  return { plan: { pending, skipped, total: localCount } }
}

// initialStatuses 生成初始状态：已收录的标为 uploaded，其余待上传。
export function initialStatuses(progress: ServerProgress): Record<number, UploadStatus> {
  const states: Record<number, UploadStatus> = {}
  for (const image of progress.images) {
    states[image.sort_order] = DONE_STATUSES.has(image.status) ? 'uploaded' : 'pending'
  }
  return states
}

// acceptedCount 统计当前已收录的图片数量，用于进度文案。
export function acceptedCount(states: Record<number, UploadStatus>): number {
  return Object.values(states).filter((status) => status === 'uploaded').length
}
