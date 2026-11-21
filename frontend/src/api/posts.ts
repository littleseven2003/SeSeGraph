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
