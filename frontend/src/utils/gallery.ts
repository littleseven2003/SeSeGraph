import { splitTags } from './meta.ts'

// 画廊的纯逻辑：查询串、发布时间、状态文案、分享文案与模糊偏好。

/** 触屏点按临时去模糊的时长，单位毫秒。 */
export const TOUCH_REVEAL_MS = 2500

/** 模糊开关的本地存储键。 */
export const BLUR_STORAGE_KEY = 'sesegraph.gallery.blur'

/** 主页布局偏好的本地存储键。 */
export const VIEW_STORAGE_KEY = 'sesegraph.gallery.view'

/** 分享文案模板的本地存储键。 */
export const SHARE_TEMPLATE_STORAGE_KEY = 'sesegraph.gallery.shareTemplate'

/** 默认分享文案模板：标题方括号、描述、标签、发布时间与链接各段之间保留一个空行。 */
export const DEFAULT_SHARE_TEMPLATE =
  '【{{title}}】\n\n{{description}}\n\n标签：{{tags}}\n\n发布时间：{{published_at}}\n\n链接：{{url}}'

/** 分享文案允许的占位符，顺序即界面插入按钮顺序。 */
export const SHARE_PLACEHOLDERS = [
  '{{title}}',
  '{{description}}',
  '{{tags}}',
  '{{published_at}}',
  '{{url}}',
  '{{count}}',
] as const

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

/** formatPublishedAt 把接口时间转成本地「YYYY-MM-DD HH:mm」，空值与缺字段都返回空串。 */
export function formatPublishedAt(value: string | null | undefined): string {
  if (typeof value !== 'string' || value.trim() === '') return ''
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
  if (publishState === 'publishing') return { label: '正在发布', tone: 'progress' }
  if (publishState === 'pending') return { label: '待发布', tone: 'progress' }
  switch (status) {
    case 'uploading':
      return { label: '正在传至服务器', tone: 'progress' }
    case 'queued':
      return { label: '排队存入频道', tone: 'progress' }
    case 'publishing':
      return { label: '正在存入频道', tone: 'progress' }
    case 'failed':
      return { label: '部分存图失败', tone: 'error' }
    case 'stopped':
      return { label: '已停止', tone: 'error' }
    case 'completed':
      return { label: '待发布', tone: 'idle' }
    default:
      return { label: '未发布', tone: 'idle' }
  }
}

/** ShareValues 是分享文案可用的字段。 */
export interface ShareValues {
  title: string
  description: string
  url: string
  count: number
  tags: string[]
  published_at?: string
}

/** 单个占位符的匹配规则；未识别的占位符保持原样。 */
const SHARE_TOKEN_PATTERN = /\{\{(title|description|url|count|tags|published_at)\}\}/g

/**
 * renderShareText 单次替换占位符并收拢空段：
 * 一行里的占位符全部为空时整行省略，连续空行压成一个，首尾空行去掉。
 */
export function renderShareText(template: string, values: ShareValues): string {
  const map: Record<string, string> = {
    title: values.title,
    description: values.description,
    url: values.url,
    count: String(values.count),
    tags: values.tags.map((tag) => '#' + tag).join('、'),
    published_at: values.published_at ?? '',
  }

  const kept: string[] = []
  for (const line of template.split('\n')) {
    const tokens = Array.from(line.matchAll(SHARE_TOKEN_PATTERN), (match) => match[1])
    if (tokens.length > 0 && tokens.every((token) => (map[token] ?? '') === '')) continue
    kept.push(line.replace(SHARE_TOKEN_PATTERN, (_, token: string) => map[token] ?? ''))
  }

  const collapsed: string[] = []
  for (const line of kept) {
    const blank = line.trim() === ''
    if (blank && (collapsed.length === 0 || collapsed[collapsed.length - 1].trim() === '')) continue
    collapsed.push(line)
  }
  while (collapsed.length > 0 && collapsed[collapsed.length - 1].trim() === '') collapsed.pop()
  return collapsed.join('\n').trim()
}

/** formatPublishedDate 取本地日期部分，分享文案与画廊展示保持一致。 */
export function formatPublishedDate(value: string | null | undefined): string {
  return formatPublishedAt(value).split(' ')[0] ?? ''
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

/** GalleryView 是主页的两种布局。 */
export type GalleryView = 'gallery' | 'list'

/** readViewPreference 读取主页布局偏好，默认画廊视图。 */
export function readViewPreference(storage: StorageLike | undefined): GalleryView {
  if (storage === undefined) return 'gallery'
  try {
    return storage.getItem(VIEW_STORAGE_KEY) === 'list' ? 'list' : 'gallery'
  } catch {
    return 'gallery'
  }
}

/** saveViewPreference 持久化主页布局偏好。 */
export function saveViewPreference(storage: StorageLike | undefined, view: GalleryView): void {
  if (storage === undefined) return
  try {
    storage.setItem(VIEW_STORAGE_KEY, view)
  } catch {
    // 隐私模式写入失败时按当前会话使用
  }
}

/** stepIndex 在给定总数里循环移动一位，用于焦点轮播的上一组/下一组。 */
export function stepIndex(current: number, total: number, delta: number): number {
  if (total <= 0) return 0
  return ((current + delta) % total + total) % total
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

/** pageWindow 计算分页按钮要显示的页码，默认五页，窄屏可传 span 收窄到三页。 */
export function pageWindow(page: number, pageSize: number, total: number, span = 5): number[] {
  const totalPages = Math.max(1, Math.ceil(total / Math.max(1, pageSize)))
  const current = Math.min(Math.max(1, page), totalPages)
  const size = Math.max(1, Math.min(span, totalPages))
  const lead = Math.floor(size / 2)
  const start = Math.max(1, Math.min(current - lead, totalPages - size + 1))
  const pages: number[] = []
  for (let index = start; index < start + size; index += 1) pages.push(index)
  return pages
}

/** shouldFeatureFirst 判断首帖能否做大幅主视觉：未筛选首页、至少两组，且首帖未撤回。 */
export function shouldFeatureFirst(options: {
  filtered: boolean
  page: number
  count: number
  firstDisabled: boolean
}): boolean {
  return !options.filtered && options.page === 1 && options.count >= 2 && !options.firstDisabled
}

/** normalizeShareTemplate 去掉首尾空白，空模板回落到默认值。 */
export function normalizeShareTemplate(template: string): string {
  const trimmed = template.trim()
  return trimmed === '' ? DEFAULT_SHARE_TEMPLATE : trimmed
}

/** readShareTemplate 读取自定义模板，缺省或读取失败时用默认模板。 */
export function readShareTemplate(storage: StorageLike | undefined): string {
  if (storage === undefined) return DEFAULT_SHARE_TEMPLATE
  try {
    return normalizeShareTemplate(storage.getItem(SHARE_TEMPLATE_STORAGE_KEY) ?? '')
  } catch {
    return DEFAULT_SHARE_TEMPLATE
  }
}

/** saveShareTemplate 持久化模板，传空串等于恢复默认。 */
export function saveShareTemplate(storage: StorageLike | undefined, template: string): string {
  const normalized = normalizeShareTemplate(template)
  if (storage !== undefined) {
    try {
      if (normalized === DEFAULT_SHARE_TEMPLATE) {
        storage.setItem(SHARE_TEMPLATE_STORAGE_KEY, '')
      } else {
        storage.setItem(SHARE_TEMPLATE_STORAGE_KEY, normalized)
      }
    } catch {
      // 存储不可用时只影响本次会话
    }
  }
  return normalized
}

/** focusStep 计算 Tab 循环后的焦点下标，用于把手动焦点限制在抽屉内。 */
export function focusStep(index: number, length: number, backwards: boolean): number {
  if (length <= 0) return -1
  if (index < 0) return backwards ? length - 1 : 0
  if (backwards) return index === 0 ? length - 1 : index - 1
  return index === length - 1 ? 0 : index + 1
}

/** 浏览量按千进位压成紧凑写法：428、1.2k、10k、1.2M。 */
export function formatViews(count: number): string {
  const value = Math.max(0, Math.floor(count))
  if (value < 1000) return String(value)
  if (value < 1_000_000) return trimTrailingZero(value / 1000) + 'k'
  return trimTrailingZero(value / 1_000_000) + 'M'
}

/** trimTrailingZero 保留一位小数并去掉多余的 .0，超出部分向下取整不夸大。 */
function trimTrailingZero(value: number): string {
  const floored = Math.floor(value * 10) / 10
  return Number.isInteger(floored) ? String(floored) : floored.toFixed(1)
}

/** formatViewsExact 保留千分位，用于详情与悬停提示里的精确值。 */
export function formatViewsExact(count: number): string {
  const digits = String(Math.max(0, Math.floor(count)))
  return digits.replace(/\B(?=(\d{3})+(?!\d))/g, ',')
}

/** showsViews 决定是否展示浏览量：已发布、未撤回且同步过，避免冗余的 0。 */
export function showsViews(item: {
  publish_state: string
  disabled: boolean
  views_updated_at: string | null
}): boolean {
  return (
    item.publish_state === 'published' &&
    !item.disabled &&
    item.views_updated_at !== null &&
    item.views_updated_at !== ''
  )
}

/** shouldShowBlur 决定当前是否盖住模糊：开关开启且未处于临时去模糊状态。 */
export function shouldShowBlur(blurred: boolean, revealed: boolean): boolean {
  return blurred && !revealed
}

/** RevealTrigger 记录这次揭开来自哪种输入，决定用哪条回遮路径。 */
export type RevealTrigger = 'pointer' | 'touch' | 'key'

/**
 * resolveRevealTrigger 按实际激活事件判定输入来源：鼠标走「离开封面满 2 秒」，
 * 触屏与键盘走固定短时回遮。键盘触发的点击 detail 为 0，据此与鼠标点击区分。
 */
export function resolveRevealTrigger(event?: { pointerType?: string; detail?: number }): RevealTrigger {
  if (event === undefined) return 'pointer'
  if (event.pointerType === 'touch') return 'touch'
  if (event.pointerType === 'mouse' || event.pointerType === 'pen') return 'pointer'
  if ((event.detail ?? 1) === 0) return 'key'
  return 'pointer'
}

/** shouldRevealOnTap 判断这次点按是否只做临时去模糊：桌面与触屏一致，先点一次揭开，再点才打开。 */
export function shouldRevealOnTap(input: { blurred: boolean; revealed: boolean }): boolean {
  return input.blurred && !input.revealed
}

/** MetadataDraft 是编辑表单的字段。 */
export interface MetadataDraft {
  title: string
  description: string
  authorName: string
  /** 逗号分隔的标签文本，提交时拆成数组；旧调用方可以不带。 */
  tagsText?: string
}

/** METADATA_LIMITS 与后端校验保持一致。 */
export const METADATA_LIMITS = { title: 200, description: 5000, author: 100 } as const

/** countRunes 按字符数统计，避免中文被当成两个长度。 */
function countRunes(value: string): number {
  return Array.from(value).length
}

/** metadataFromDetail 用详情填充编辑草稿。 */
export function metadataFromDetail(detail: {
  title: string
  description: string
  author_name: string
  tags?: string[]
}): MetadataDraft {
  return {
    title: detail.title,
    description: detail.description,
    authorName: detail.author_name,
    tagsText: (detail.tags ?? []).join('，'),
  }
}

/** validateMetadata 返回空串表示可以提交。 */
export function validateMetadata(draft: MetadataDraft): string {
  const tags = splitTags(draft.tagsText ?? '')
  if (tags.length > 20) return '标签最多 20 个'
  if (tags.some((tag) => Array.from(tag).length > 30)) return '单个标签不超过 30 字'
  const title = draft.title.trim()
  if (title === '') return '标题不能为空'
  if (countRunes(title) > METADATA_LIMITS.title) return '标题不超过 200 字'
  if (countRunes(draft.description) > METADATA_LIMITS.description) return '说明不超过 5000 字'
  if (countRunes(draft.authorName.trim()) > METADATA_LIMITS.author) return '作者名不超过 100 字'
  return ''
}

/** deleteFailureText 保留服务端给出的删除失败原因，远端撤回与本地文件清理都按原文展示。 */
export function deleteFailureText(status: number, message: string): string {
  const detail = message.trim()
  if (detail !== '' && detail !== '请求失败，请稍后重试') return detail
  return status === 409 ? '任务正在进行中，稍后再试' : '删除失败，请稍后重试'
}

/** updateFailureText 把接口错误转成编辑表单里可读的提示。 */
export function updateFailureText(status: number, message: string): string {
  if (status === 409) {
    // 服务端文案已说明原因，分页变化时再补一句处理建议。
    if (message.includes('分页')) return message + '；请增减说明长度后重试'
    return message
  }
  if (status === 401) return '登录已失效，请重新登录后再编辑'
  if (status === 503) return 'Telegraph 未配置，已发布帖暂不能编辑'
  if (status === 502) return '更新 Telegraph 页面失败，本地记录未改动'
  return message !== '' ? message : '保存失败，请稍后重试'
}

/** toggleActionLabel 返回按钮文案。 */
export function toggleActionLabel(disabled: boolean): string {
  return disabled ? '恢复访问' : '撤回'
}

/** toggleHint 返回撤回/恢复的状态说明，含已缓存副本的客观提示。 */
export function toggleHint(disabled: boolean): string {
  return disabled
    ? '源站短码立即返回 404，已缓存的副本可能仍显示一段时间。'
    : '撤回后该帖短码立即返回 404，重新开启即恢复访问。'
}

/** toggleConfirmText 返回二次确认文案，撤回时明确指出源站 404 与缓存副本的边界。 */
export function toggleConfirmText(disabled: boolean): string {
  return disabled
    ? '恢复后分享链接重新可用，确认恢复？'
    : '撤回后源站短码立即 404，已缓存的副本可能仍显示。确认撤回？'
}
