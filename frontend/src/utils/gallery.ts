// 画廊的纯逻辑：查询串、发布时间、状态文案、分享文案与模糊偏好。

/** 模糊开关的本地存储键。 */
export const BLUR_STORAGE_KEY = 'sesegraph.gallery.blur'

/** 默认分享文案模板，占位符与任务书一致。 */
export const DEFAULT_SHARE_TEMPLATE = '{{title}} · {{count}} 张图\n{{url}}\n{{tags}}'

/** 分享文案允许的占位符。 */
export const SHARE_PLACEHOLDERS = ['{{title}}', '{{description}}', '{{url}}', '{{count}}', '{{tags}}'] as const

/** 状态文案与语气。 */
export interface StatusLabel {
  label: string
  tone: 'idle' | 'progress' | 'ok' | 'error'
}

/** buildGalleryQuery 生成列表接口的查询串，空关键词与空标签不下发。 */
export function buildGalleryQuery(input: {
  q?: string
  tags?: string[]
  page?: number
  pageSize?: number
}): string {
  const params = new URLSearchParams()
  const keyword = (input.q ?? '').trim()
  if (keyword !== '') params.set('q', keyword)
  for (const tag of input.tags ?? []) {
    if (tag.trim() !== '') params.append('tag', tag.trim())
  }
  params.set('page', String(input.page ?? 1))
  params.set('page_size', String(input.pageSize ?? 12))
  return params.toString()
}

/** formatPublishedAt 把接口时间转成本地「YYYY-MM-DD HH:mm」，空值返回空串。 */
export function formatPublishedAt(value: string): string {
  if (value.trim() === '') return ''
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return ''
  const pad = (part: number): string => String(part).padStart(2, '0')
  return (
    date.getFullYear() +
    '-' +
    pad(date.getMonth() + 1) +
    '-' +
    pad(date.getDate()) +
    ' ' +
    pad(date.getHours()) +
    ':' +
    pad(date.getMinutes())
  )
}

/** statusLabel 汇总存图与发布的现态，撤回优先级最高。 */
export function statusLabel(status: string, publishState: string, disabled = false): StatusLabel {
  if (disabled) return { label: '已撤回', tone: 'error' }
  if (publishState === 'published') return { label: '已发布', tone: 'ok' }
  if (publishState === 'failed') return { label: '发布失败', tone: 'error' }
  if (publishState === 'publishing') return { label: '发布中', tone: 'progress' }
  if (publishState === 'pending') return { label: '待发布', tone: 'progress' }
  switch (status) {
    case 'uploading':
      return { label: '上传中', tone: 'progress' }
    case 'queued':
      return { label: '排队中', tone: 'progress' }
    case 'publishing':
      return { label: '存图中', tone: 'progress' }
    case 'failed':
      return { label: '存图失败', tone: 'error' }
    case 'completed':
      return { label: '待发布', tone: 'idle' }
    default:
      return { label: '未发布', tone: 'idle' }
  }
}

/** renderShareText 按模板替换占位符，未识别的占位符原样保留。 */
export function renderShareText(
  template: string,
  values: { title: string; description: string; url: string; count: number; tags: string[] },
): string {
  const replacement: Record<string, string> = {
    '{{title}}': values.title,
    '{{description}}': values.description,
    '{{url}}': values.url,
    '{{count}}': String(values.count),
    '{{tags}}': values.tags.map((tag) => '#' + tag).join(' '),
  }
  let text = template
  for (const [token, value] of Object.entries(replacement)) {
    text = text.split(token).join(value)
  }
  return text.trim()
}

/** 整帖分享的可用条件：已发布、公开、未撤回且互链页面就绪。 */
export interface SharePostState {
  publish_state: string
  public: boolean
  disabled: boolean
  telegraph_urls: string[]
}

/** 单图外链的可用条件：帖子公开未撤回且该图已存图。 */
export interface ShareImageState {
  short_code: string
  status: string
}

/** postShareUrl 仅在分篇互链完成后返回整帖分享链接，否则为空。 */
export function postShareUrl(post: SharePostState): string {
  if (post.disabled || !post.public || post.publish_state !== 'published') return ''
  return post.telegraph_urls.find((url) => url.trim() !== '') ?? ''
}

/** imageShareUrl 用服务端公开地址拼接单图外链，条件不满足时返回空串。 */
export function imageShareUrl(
  post: { public: boolean; disabled: boolean },
  image: ShareImageState,
  publicBase: string,
): string {
  if (publicBase.trim() === '' || post.disabled || !post.public) return ''
  if (image.status !== 'stored' || image.short_code.trim() === '') return ''
  return publicBase.trim().replace(/\/+$/, '') + '/i/' + image.short_code
}

/** LatestGuard 只让最后一次请求的结果生效，避免旧响应覆盖新选择。 */
export interface LatestGuard {
  begin(): number
  isCurrent(token: number): boolean
}

/** createLatestGuard 生成请求序号守卫。 */
export function createLatestGuard(): LatestGuard {
  let current = 0
  return {
    begin(): number {
      current += 1
      return current
    },
    isCurrent(token: number): boolean {
      return token === current
    },
  }
}

/** formatFileSize 把字节数转成易读单位。 */
export function formatFileSize(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return '0 KB'
  if (bytes < 1024 * 1024) return Math.max(1, Math.round(bytes / 1024)) + ' KB'
  return (bytes / (1024 * 1024)).toFixed(1) + ' MB'
}

interface StorageLike {
  getItem(key: string): string | null
  setItem(key: string, value: string): void
}

/** readBlurPreference 读取模糊开关，默认开启。 */
export function readBlurPreference(storage: StorageLike | undefined): boolean {
  if (storage === undefined) return true
  try {
    return storage.getItem(BLUR_STORAGE_KEY) !== 'off'
  } catch {
    return true
  }
}

/** saveBlurPreference 持久化模糊开关。 */
export function saveBlurPreference(storage: StorageLike | undefined, enabled: boolean): void {
  if (storage === undefined) return
  try {
    storage.setItem(BLUR_STORAGE_KEY, enabled ? 'on' : 'off')
  } catch {
    // 存储不可用时只影响本次会话
  }
}

/** pageWindow 计算分页按钮要显示的页码，最多五页并以省略区间收拢。 */
export function pageWindow(page: number, pageSize: number, total: number): number[] {
  const totalPages = Math.max(1, Math.ceil(total / Math.max(1, pageSize)))
  const current = Math.min(Math.max(1, page), totalPages)
  const start = Math.max(1, Math.min(current - 2, totalPages - 4))
  const end = Math.min(totalPages, start + 4)
  const pages: number[] = []
  for (let index = start; index <= end; index += 1) pages.push(index)
  return pages
}
