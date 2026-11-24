// 详情抽屉的缩略图按需加载：同一时间只回源一张，缓存固定上限，切帖或关闭即释放。
import type { GalleryImage } from '../api/gallery.ts'

// 缓存上限：只留低分辨率 Blob 的 object URL，几天内几十项的列表也不会累计占内存。
export const THUMB_CACHE_LIMIT = 16
// 超过该体积的原图不随列表自动加载，等悬停、键盘聚焦或触控预览时再取。
export const THUMB_EAGER_MAX_BYTES = 4_000_000
// 缩略图最长边，行内 44px、放大气泡 224px 都用这一份。
export const THUMB_MAX_PIXELS = 224

// canLoadThumb 判断该项是否有可读的管理员媒体：只有已存入频道的图片能按序号取。
export function canLoadThumb(image: Pick<GalleryImage, 'status'>): boolean {
  return image.status === 'stored'
}

// needsExplicitPreview 判断是否必须等用户明确预览：视频与较大的原图不自动回源。
export function needsExplicitPreview(image: Pick<GalleryImage, 'file_size' | 'mime_type'>): boolean {
  return image.mime_type === 'video/mp4' || image.file_size > THUMB_EAGER_MAX_BYTES
}

export interface ThumbEntry {
  url: string
  order: number
}

export type ThumbLoader = (order: number, signal: AbortSignal) => Promise<Blob>

export interface ThumbQueueOptions {
  limit?: number
  createUrl?: (blob: Blob) => string
  revokeUrl?: (url: string) => void
  /** 每张缩略图入缓存后回调一次，界面据此刷新行内小图。 */
  onStore?: (order: number) => void
}

// ThumbQueue 串行回源并做 LRU 淘汰，任一时刻最多一个请求在途。
export class ThumbQueue {
  private readonly loader: ThumbLoader
  private readonly limit: number
  private readonly createUrl: (blob: Blob) => string
  private readonly revokeUrl: (url: string) => void
  private readonly cache = new Map<number, string>()
  private readonly onStore: ((order: number) => void) | undefined
  private readonly waiting: number[] = []
  private current: AbortController | null = null
  private running = false
  private disposed = false
  private loaded = 0

  constructor(loader: ThumbLoader, options: ThumbQueueOptions = {}) {
    this.loader = loader
    this.limit = Math.max(1, options.limit ?? THUMB_CACHE_LIMIT)
    this.createUrl = options.createUrl ?? ((blob) => URL.createObjectURL(blob))
    this.revokeUrl = options.revokeUrl ?? ((url) => URL.revokeObjectURL(url))
    this.onStore = options.onStore
  }

  /** size 返回当前缓存的缩略图数量。 */
  get size(): number {
    return this.cache.size
  }

  /** loadedCount 返回本次会话成功加载的数量，便于在界面上说明加载进度。 */
  get loadedCount(): number {
    return this.loaded
  }

  has(order: number): boolean {
    return this.cache.has(order)
  }

  get(order: number): string {
    const url = this.cache.get(order)
    if (url === undefined) return ''
    // 命中即提到队尾，让最近看过的项留得更久。
    this.cache.delete(order)
    this.cache.set(order, url)
    return url
  }

  // request 排队取一张；已在缓存或队列中的重复请求会被忽略。
  request(order: number): void {
    if (this.disposed || this.cache.has(order) || this.waiting.includes(order)) return
    this.waiting.push(order)
    void this.pump()
  }

  private async pump(): Promise<void> {
    if (this.running || this.disposed) return
    this.running = true
    try {
      while (!this.disposed && this.waiting.length > 0) {
        const order = this.waiting.shift() as number
        if (this.cache.has(order)) continue
        const controller = new AbortController()
        this.current = controller
        try {
          const blob = await this.loader(order, controller.signal)
          if (this.disposed || controller.signal.aborted) continue
          this.store(order, blob)
        } catch {
          // 单张失败不影响其余项：调用方会在需要时重试。
        } finally {
          if (this.current === controller) this.current = null
        }
      }
    } finally {
      this.running = false
    }
  }

  private store(order: number, blob: Blob): void {
    this.cache.set(order, this.createUrl(blob))
    this.loaded += 1
    this.onStore?.(order)
    while (this.cache.size > this.limit) {
      const oldest = this.cache.keys().next().value
      if (oldest === undefined) break
      const url = this.cache.get(oldest) ?? ''
      this.cache.delete(oldest)
      if (url !== '') this.revokeUrl(url)
    }
  }

  // dispose 取消在途请求、清空队列并释放全部 object URL。
  dispose(): void {
    this.disposed = true
    this.waiting.length = 0
    this.current?.abort()
    this.current = null
    for (const url of this.cache.values()) this.revokeUrl(url)
    this.cache.clear()
  }
}

// pickEagerOrders 只挑可视区里可以自动加载的项；视频与大图留给明确预览。
export function pickEagerOrders(visible: number[], images: GalleryImage[]): number[] {
  const byOrder = new Map(images.map((image) => [image.sort_order, image]))
  const picked: number[] = []
  for (const order of visible) {
    const image = byOrder.get(order)
    if (image === undefined) continue
    if (!canLoadThumb(image) || needsExplicitPreview(image)) continue
    picked.push(order)
  }
  return picked
}

// renderThumb 把原图缩到最长边 max 的 WebP：只保留小体积 Blob，原始大图随即释放。
export async function renderThumb(blob: Blob, max = THUMB_MAX_PIXELS): Promise<Blob> {
  if (typeof createImageBitmap !== 'function' || typeof document === 'undefined') {
    throw new Error('当前浏览器不支持缩略图')
  }
  const bitmap = await createImageBitmap(blob)
  try {
    const scale = Math.min(1, max / Math.max(bitmap.width, bitmap.height))
    const width = Math.max(1, Math.round(bitmap.width * scale))
    const height = Math.max(1, Math.round(bitmap.height * scale))
    const canvas = document.createElement('canvas')
    canvas.width = width
    canvas.height = height
    const context = canvas.getContext('2d')
    if (context === null) throw new Error('无法绘制缩略图')
    context.drawImage(bitmap, 0, 0, width, height)
    const output = await new Promise<Blob | null>((resolve) => canvas.toBlob(resolve, 'image/webp', 0.72))
    if (output === null) throw new Error('生成缩略图失败')
    return output
  } finally {
    bitmap.close?.()
  }
}

// withTimeout 给解码与取图加一道上限，超时按失败处理并回到占位。
export async function withTimeout<T>(task: Promise<T>, ms: number, message = '缩略图超时'): Promise<T> {
  let timer: ReturnType<typeof setTimeout> | undefined
  const timeout = new Promise<never>((_, reject) => {
    timer = setTimeout(() => reject(new Error(message)), ms)
  })
  try {
    return await Promise.race([task, timeout])
  } finally {
    if (timer !== undefined) clearTimeout(timer)
  }
}
