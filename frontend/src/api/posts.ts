// 管理接口客户端：建任务、上传图片、提交入队与进度查询。
import { ApiError } from './client.ts'

export interface CreatePostPayload {
  title: string
  description: string
  author_name: string
  upload_mode: 'photo' | 'document'
  image_count: number
  cover_thumbnail: string
  tags: string[]
}

export interface CreatedPost {
  id: number
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
  stored_count: number
  failed_count: number
  images: ServerProgressImage[]
}

// uploadImage 逐张上传，请求体直接使用浏览器里的 Blob，由后端流式落盘。
export async function uploadImage(postId: number, sortOrder: number, blob: Blob, mime: string): Promise<UploadedImage> {
  const response = await adminRequest(`/api/posts/${postId}/images?sort=${sortOrder}`, {
    method: 'POST',
    headers: mime === '' ? undefined : { 'Content-Type': mime },
    body: blob,
  })
  return (await response.json()) as UploadedImage
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
