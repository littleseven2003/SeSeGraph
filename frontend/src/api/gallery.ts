// 画廊接口客户端：列表筛选、详情与标签读取。
import { ApiError } from './client.ts'

export interface GalleryItem {
  id: number
  title: string
  cover_url: string
  cover_thumb_path: string
  image_count: number
  status: string
  publish_state: string
  public: boolean
  disabled: boolean
  tags: string[]
  telegraph_urls: string[]
  published_at: string
  created_at: string
  updated_at: string
}

export interface GalleryPage {
  items: GalleryItem[]
  total: number
  page: number
  page_size: number
}

export interface GalleryImage {
  sort_order: number
  short_code: string
  mime_type: string
  file_size: number
  status: string
}

export interface PostDetail extends GalleryItem {
  description: string
  author_name: string
  upload_mode: string
  error_message?: string
  publish_error?: string
  telegraph_paths: string[]
  images: GalleryImage[]
  public_base_url: string
}

export interface TagCount {
  name: string
  count: number
}

export interface GalleryQuery {
  q: string
  tags: string[]
  page: number
  pageSize: number
}

export interface PostMetadata {
  title: string
  description: string
  author_name: string
}

async function galleryRequest(path: string, init?: RequestInit): Promise<Response> {
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

// fetchGallery 按关键词、多标签与分页读取画廊列表。
export async function fetchGallery(query: GalleryQuery): Promise<GalleryPage> {
  const params = new URLSearchParams()
  if (query.q !== '') params.set('q', query.q)
  for (const tag of query.tags) params.append('tag', tag)
  params.set('page', String(query.page))
  params.set('page_size', String(query.pageSize))
  return (await (await galleryRequest('/api/posts?' + params.toString())).json()) as GalleryPage
}

// fetchPostDetail 读取单帖详情与图片清单。
export async function fetchPostDetail(id: number): Promise<PostDetail> {
  return (await (await galleryRequest('/api/posts/' + String(id))).json()) as PostDetail
}

// fetchTags 读取全部标签及关联数量。
export async function fetchTags(): Promise<TagCount[]> {
  const body = (await (await galleryRequest('/api/tags')).json()) as { items: TagCount[] }
  return body.items
}

// updatePost 更新标题、说明与作者；已发布帖会先同步远端页面再落本地。
export async function updatePost(id: number, meta: PostMetadata): Promise<PostMetadata> {
  const response = await galleryRequest('/api/posts/' + String(id), {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(meta),
  })
  return (await response.json()) as PostMetadata
}

// setPostDisabled 把单帖熔断写成目标状态：禁用后短码立即 404，恢复沿用原有公开授权。
// 发送目标值而非翻转，超时重试同一目标保持原状态。
export async function setPostDisabled(
  id: number,
  disabled: boolean,
): Promise<{ id: number; disabled: boolean; public: boolean }> {
  const response = await galleryRequest('/api/posts/' + String(id) + '/toggle', {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ disabled }),
  })
  return (await response.json()) as { id: number; disabled: boolean; public: boolean }
}
