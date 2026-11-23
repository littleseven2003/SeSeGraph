// 上传计划：根据服务端进度决定需要补传哪些序号。
import type { ServerProgress } from '../api/posts.ts'

// 单张图片在本地的上传状态。
export type UploadStatus = 'pending' | 'uploading' | 'uploaded' | 'stored' | 'failed'

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

// UPLOAD_CONCURRENCY 是每轮同时上传的图片数。
// 跨境单流带宽有限，两路能明显缩短总耗时，又不会把低配实例或临时目录压满。
export const UPLOAD_CONCURRENCY = 2

// UploadPoolFailure 记录首个失败的队列位置与原因，后续项不再派发。
export interface UploadPoolFailure {
  index: number
  error: unknown
}

// runUploadPool 以受控并发执行上传队列：首个失败后停止派发新项，
// 已在途的请求自然收尾，返回失败位置供界面提示与续传定位。
export async function runUploadPool<T>(
  items: readonly T[],
  limit: number,
  task: (item: T, index: number) => Promise<void>,
): Promise<UploadPoolFailure | null> {
  const width = Math.max(1, Math.min(limit, items.length))
  let cursor = 0
  let failure: UploadPoolFailure | null = null
  const worker = async (): Promise<void> => {
    while (failure === null) {
      const index = cursor
      cursor += 1
      if (index >= items.length) return
      try {
        await task(items[index], index)
      } catch (error) {
        if (failure === null) failure = { index, error }
        return
      }
    }
  }
  await Promise.all(Array.from({ length: width }, () => worker()))
  return failure
}

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
    // 已存入频道与已传至服务器是两段状态，界面上要能区分。
    if (image.status === 'stored') states[image.sort_order] = 'stored'
    else if (image.status === 'uploaded') states[image.sort_order] = 'uploaded'
    else states[image.sort_order] = 'pending'
  }
  return states
}

// acceptedCount 统计当前已收录的图片数量，用于进度文案。
export function acceptedCount(states: Record<number, UploadStatus>): number {
  return Object.values(states).filter((status) => status === 'uploaded' || status === 'stored').length
}
