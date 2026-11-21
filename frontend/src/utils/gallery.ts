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

/** primaryShareUrl 优先取首个 Telegraph 页面链接，未发布时回落到首图短码。 */
export function primaryShareUrl(
  detail: { telegraph_urls: string[]; images: Array<{ short_code: string }> },
  origin: string,
): string {
  const page = detail.telegraph_urls.find((url) => url.trim() !== '')
  if (page !== undefined) return page
  const image = detail.images.find((item) => item.short_code.trim() !== '')
  if (image === undefined) return ''
  return imagePublicUrl(image.short_code, origin)
}

/** imagePublicUrl 生成单图外链。 */
export function imagePublicUrl(shortCode: string, origin: string): string {
  const base = origin.replace(/\/+$/, '')
  return base + '/i/' + shortCode
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
