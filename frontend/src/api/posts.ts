// 管理接口客户端：建任务、上传图片、提交入队与进度查询。
import { ApiError } from './client.ts'

export interface CreatePostPayload {
  title: string
  description: string
  author_name: string
  upload_mode: 'photo' | 'document'
  image_count: number
  cover_thumbnail: string
  /** 封面来源序号，发布时会把这张图放到 Telegraph 第一位。 */
  cover_sort_order?: number
  /** 浏览器提交前已处理的项数，用于四阶段的第一段 */
  compressed_count?: number
  /** 发布意图：auto 表示同步完成后自动入发布队列，缺省或空表示仅存图 */
  publish_intent?: 'auto' | ''
  tags: string[]
}

export interface CreatedPost {
  id: number
  /** 本次保存的发布意图，空表示仅存图 */
  publish_intent?: string
  title: string
  tags: string[]
  status: string
  image_count: number
  upload_mode: string
}

async function adminRequest(path: string, init?: RequestInit): Promise<Response> {
  const response = await fetch(path, { credentials: 'same-origin', ...init })
  if (!response.ok) {
    let message = '请求失败，请稍后重试'
    try {
      const body = (await response.json()) as { error?: unknown }
      if (typeof body.error === 'string' && body.error.trim() !== '') {
        message = body.error
      }
    } catch {
      // 响应不是 JSON 时保留统一说明
    }
    throw new ApiError(response.status, message)
  }
  return response
}

// createPost 先落元数据与封面，随后由逐图上传补齐图片。
export async function createPost(payload: CreatePostPayload): Promise<CreatedPost> {
  const response = await adminRequest('/api/posts', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  })
  return (await response.json()) as CreatedPost
}

export interface UploadedImage {
  post_id: number
  sort_order: number
  file_size: number
  mime_type: string
  status: string
  /** 这次上传的内容版本，生成缩略图后带上它回存。 */
  media_version: string
}

export interface CommitResult {
  id: number
  status: string
  image_count: number
  /** 缺图时返回的序号列表 */
  missing?: number[]
}

export interface ServerProgressImage {
  sort_order: number
  status: string
  error?: string
}

export interface ServerProgress {
  id: number
  status: string
  tags: string[]
  image_count: number
  /** 任务创建时确定的画质，续传按它做大小校验 */
  upload_mode: string
  stored_count: number
  failed_count: number
  images: ServerProgressImage[]
  /** 发布状态：idle / pending / publishing / published / failed */
  publish_state: string
  /** 是否已对外公开 */
  public: boolean
  /** 发布完成后的分享链接 */
  telegraph_urls: string[]
  /** 任务控制状态：running / paused / stopped */
  control_state: string
  /** 上传轮次，暂停后继续会递增；上传请求带上它可拒绝迟到写入 */
  upload_round: number
  /** 当前封面来源序号，0 表示未选择封面 */
  cover_sort_order: number
  /** 提交时浏览器已处理的项数；null 表示旧任务没有这项记录 */
  compressed_count?: number | null
  /** 任务失败的可读原因，未失败时为空 */
  error?: string
  /** 发布失败的可读原因，未发布失败时为空 */
  publish_error?: string
}

// uploadImage 逐张上传，请求体直接使用浏览器里的 Blob，由后端流式落盘。
export async function uploadImage(
  postId: number,
  sortOrder: number,
  blob: Blob,
  mime: string,
  round: number,
  signal?: AbortSignal,
): Promise<UploadedImage> {
  // 单张上传必须有时限：低配链路卡住时给出明确失败，而不是让队列无限等待。
  const controller = new AbortController()
  const timer = setTimeout(() => controller.abort(), 5 * 60 * 1000)
  let response: Response
  try {
    response = await adminRequest(`/api/posts/${postId}/images?sort=${sortOrder}&round=${round}`, {
      method: 'POST',
      headers: mime === '' ? undefined : { 'Content-Type': mime },
      body: blob,
      signal: abortable(controller, signal),
    })
  } catch (error) {
    if (signal?.aborted === true) throw new Error('上传已取消')
    if (controller.signal.aborted) throw new Error('单张上传超过 5 分钟未完成，已中断，可继续上传重试')
    throw error
  } finally {
    clearTimeout(timer)
  }
  return (await response.json()) as UploadedImage
}

// changeCover 更换封面：序号必填，缩略图可选（已发布帖先改远端再回写本地）。
export async function changeCover(postId: number, sortOrder: number, thumbnail?: string): Promise<void> {
  const body: { sort_order: number; cover_thumbnail?: string } = { sort_order: sortOrder }
  if (thumbnail !== undefined && thumbnail !== '') body.cover_thumbnail = thumbnail
  await adminRequest(`/api/posts/${postId}/cover`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
}

// pauseTask 暂停任务；继续上传只补服务端缺项。
export async function pauseTask(postId: number): Promise<void> {
  await adminRequest(`/api/posts/${postId}/pause`, { method: 'POST' })
}

// resumeTask 恢复暂停中的任务，服务端会递增上传轮次。
export async function resumeTask(postId: number): Promise<void> {
  await adminRequest(`/api/posts/${postId}/resume`, { method: 'POST' })
}

// stopTask 停止任务；停止后短码关闭且不再被队列领取。
export async function stopTask(postId: number): Promise<void> {
  await adminRequest(`/api/posts/${postId}/stop`, { method: 'POST' })
}

// abortable 把外部取消信号与单张超时合并成一个 AbortSignal。
function abortable(controller: AbortController, external?: AbortSignal): AbortSignal {
  if (external === undefined) return controller.signal
  if (external.aborted) controller.abort()
  else external.addEventListener('abort', () => controller.abort(), { once: true })
  return controller.signal
}

/** TaskRow 是任务列表的一行，聚合上传、存图与发布状态。 */
export interface TaskRow {
  id: number
  title: string
  status: string
  publish_state: string
  control_state: string
  upload_round: number
  image_count: number
  /** 兼容口径：已上传加已存入频道，不含存频道失败项 */
  stored_count: number
  /** 真实已接收张数：上传、入库与存频道失败都算收到 */
  received_count: number
  /** 只在服务器、尚未存入频道的张数 */
  uploaded_count: number
  /** 已存入频道张数 */
  channel_count: number
  /** 提交时浏览器已处理的项数；null 表示旧任务没有这项记录 */
  compressed_count: number | null
  /** 提交时选择的发布意图：auto 表示同步完成后自动入队，空串表示旧任务或仅存图 */
  publish_intent: string
  updated_at: string
  /** 任务失败的可读原因，未失败时为空串 */
  error?: string
  /** 发布失败的可读原因，未发布失败时为空串 */
  publish_error?: string
}

/** 任务列表筛选值：空串表示全部，与后端 status 参数一致。 */
export type TaskFilter = '' | 'active' | 'paused' | 'stopped' | 'finished'

/** fetchTasks 按筛选条件分页读取任务列表。 */
export async function fetchTasks(
  page: number,
  pageSize = 20,
  status: TaskFilter = '',
): Promise<{ items: TaskRow[]; total: number; page: number; page_size: number }> {
  const query = `page=${page}&page_size=${pageSize}${status === '' ? '' : `&status=${status}`}`
  const response = await adminRequest(`/api/tasks?${query}`)
  return (await response.json()) as { items: TaskRow[]; total: number; page: number; page_size: number }
}

// thumbPath 返回某一项缩略图的地址；version 参与查询串，便于区分版本。
export function thumbPath(postId: number, sortOrder: number, version: string): string {
  return `/api/posts/${postId}/media/${sortOrder}/thumb?v=${encodeURIComponent(version)}`
}

// fetchThumbBlob 读取已落盘的缩略图；还没有时返回 null，由调用方回源生成。
export async function fetchThumbBlob(
  postId: number,
  sortOrder: number,
  version: string,
  signal?: AbortSignal,
): Promise<Blob | null> {
  const response = await fetch(thumbPath(postId, sortOrder, version), { credentials: 'same-origin', signal })
  if (response.status === 404) return null
  if (!response.ok) throw new ApiError(response.status, '读取缩略图失败')
  return await response.blob()
}

// uploadThumb 把生成好的缩略图回存服务端，成功后再次打开只取小图。
export async function uploadThumb(
  postId: number,
  sortOrder: number,
  version: string,
  blob: Blob,
  signal?: AbortSignal,
): Promise<void> {
  const response = await fetch(thumbPath(postId, sortOrder, version), {
    method: 'PUT',
    credentials: 'same-origin',
    headers: { 'Content-Type': 'image/webp' },
    body: blob,
    signal,
  })
  if (response.ok) return
  let message = '保存缩略图失败'
  try {
    const body = (await response.json()) as { error?: unknown }
    if (typeof body.error === 'string' && body.error.trim() !== '') message = body.error
  } catch {
    // 非 JSON 响应保留统一说明
  }
  throw new ApiError(response.status, message)
}

// mediaPath 返回管理员会话下读取原图或视频的地址，供换封面生成缩略图。
export function mediaPath(postId: number, sortOrder: number): string {
  return `/api/posts/${postId}/media/${sortOrder}`
}

// fetchMediaBlob 读取帖子内的一张原图或视频，失败时抛出明确错误。
export async function fetchMediaBlob(postId: number, sortOrder: number, signal?: AbortSignal): Promise<Blob> {
  const response = await fetch(mediaPath(postId, sortOrder), { credentials: 'same-origin', signal })
  if (!response.ok) throw new ApiError(response.status, '读取原图失败，无法生成封面')
  return await response.blob()
}

// commitPost 校验图片齐备并进入上传队列。
export async function commitPost(postId: number): Promise<CommitResult> {
  const response = await adminRequest(`/api/posts/${postId}/commit`, { method: 'POST' })
  return (await response.json()) as CommitResult
}

// fetchProgress 读取服务端进度，用于展示与断点续传。
export async function fetchProgress(postId: number): Promise<ServerProgress> {
  const response = await adminRequest(`/api/posts/${postId}/progress`)
  return (await response.json()) as ServerProgress
}

export interface RetryResult {
  id: number
  status: string
  retried: number
}

// retryPost 把失败的图片重新排入队列。
export async function retryPost(postId: number): Promise<RetryResult> {
  const response = await adminRequest(`/api/posts/${postId}/retry`, { method: 'POST' })
  return (await response.json()) as RetryResult
}

export interface PublishResult {
  id: number
  publish_state: string
  public: boolean
}

// publishPost 由管理员确认发布，开启图片访问并把任务放入发布队列。
export async function publishPost(postId: number): Promise<PublishResult> {
  const response = await adminRequest(`/api/posts/${postId}/publish`, { method: 'POST' })
  return (await response.json()) as PublishResult
}
