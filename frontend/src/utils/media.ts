// 短视频与超大静态图的前端预处理规则，与后端上限保持一致。
import { baseName, extensionOf, formatBytes, isVideoName, limitForMode, type UploadMode } from './image.ts'

// 单段视频上限，与 Telegram 云端 getFile 的 20MB 硬限制一致。
export const MAX_VIDEO_BYTES = 20000000
// photo 模式下超过该体积的静态图先压缩再上传。
export const PHOTO_COMPRESS_THRESHOLD = 10000000
// 压缩输入的安全上限：避免把整块内存交给超大原图。
export const COMPRESS_MAX_INPUT_BYTES = 64 * 1024 * 1024
export const COMPRESS_MAX_INPUT_PIXELS = 40000000
// 压缩产物的宽高之和与长宽比上限。
export const COMPRESS_MAX_DIMENSION_SUM = 10000
export const COMPRESS_MAX_ASPECT = 20
// 压缩目标体积留出少量余量，避免边界处四舍五入后再超限。
export const COMPRESS_TARGET_BYTES = 9800000

export type MediaKind = 'image' | 'video'

// mediaKind 返回文件属于图片还是视频，未知类型返回空串。
export function mediaKind(name: string, mime: string): MediaKind | '' {
  if (isVideoName(name) || mime === 'video/mp4') return 'video'
  return 'image'
}

// limitForMedia 返回单文件上限：视频固定 20,000,000 字节，图片按画质模式。
export function limitForMedia(mode: UploadMode, name: string): number {
  return isVideoName(name) ? MAX_VIDEO_BYTES : limitForMode(mode)
}

// needsCompression 判断静态图是否需要先压缩：只处理 photo 模式且超过 10MB 的图。
export function needsCompression(mode: UploadMode, name: string, size: number): boolean {
  return mode === 'photo' && !isVideoName(name) && size > PHOTO_COMPRESS_THRESHOLD
}

// checkMediaFile 返回不放行的原因，放行时返回 null。
export function checkMediaFile(file: { name: string; size: number }, mode: UploadMode): string | null {
  const name = baseName(file.name)
  if (isVideoName(file.name)) {
    if (file.size > MAX_VIDEO_BYTES) {
      return `${name} 超过 20MB 上限，短视频请先压缩或裁剪`
    }
    return null
  }
  if (needsCompression(mode, file.name, file.size)) {
    if (file.size > COMPRESS_MAX_INPUT_BYTES) {
      return `${name} 超过 64MB，请先用其他工具缩放后再导入`
    }
    return null
  }
  if (file.size > limitForMode(mode)) {
    return `${name} 超过 ${(limitForMode(mode) / (1024 * 1024)).toFixed(0)}MB 上限`
  }
  return null
}

// formatDuration 把秒数格式化为 m:ss，未知时长返回空串。
export function formatDuration(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds <= 0) return ''
  const total = Math.round(seconds)
  const minutes = Math.floor(total / 60)
  const rest = total % 60
  return `${minutes}:${String(rest).padStart(2, '0')}`
}

// 压缩结果，带原始与产物体积，供界面显示前后对比。
export interface CompressResult {
  blob: Blob
  width: number
  height: number
  fromSize: number
  toSize: number
}

// 动图判定：GIF 按扩展名，WebP/PNG 检查动画标记，避免把动效静默压成静态图。
export async function isAnimatedImage(blob: Blob, name: string): Promise<boolean> {
  if (extensionOf(name) === 'gif') return true
  const bytes = new Uint8Array(await blob.slice(0, 4096).arrayBuffer())
  const text = String.fromCharCode(...bytes)
  return text.includes('ANIM') || text.includes('acTL')
}

function loadBitmap(blob: Blob): Promise<{ bitmap: ImageBitmap | HTMLImageElement; close: () => void }> {
  if (typeof createImageBitmap === 'function') {
    return createImageBitmap(blob).then((bitmap) => ({
      bitmap,
      close: () => bitmap.close(),
    }))
  }
  const url = URL.createObjectURL(blob)
  return new Promise((resolve, reject) => {
    const image = new Image()
    image.onload = () => resolve({ bitmap: image, close: () => URL.revokeObjectURL(url) })
    image.onerror = () => {
      URL.revokeObjectURL(url)
      reject(new Error('图片读取失败'))
    }
    image.src = url
  })
}

function encodeScale(source: CanvasImageSource, width: number, height: number, quality: number): Promise<Blob> {
  const canvas =
    typeof OffscreenCanvas === 'function'
      ? new OffscreenCanvas(width, height)
      : Object.assign(document.createElement('canvas'), { width, height })
  const context = canvas.getContext('2d') as
    | OffscreenCanvasRenderingContext2D
    | CanvasRenderingContext2D
    | null
  if (!context) throw new Error('当前浏览器不支持图片压缩')
  context.drawImage(source, 0, 0, width, height)
  if (canvas instanceof HTMLCanvasElement) {
    return new Promise((resolve, reject) => {
      canvas.toBlob(
        (blob) => (blob ? resolve(blob) : reject(new Error('图片压缩失败'))),
        'image/webp',
        quality,
      )
    })
  }
  return canvas.convertToBlob({ type: 'image/webp', quality })
}

// fitScale 先满足宽高之和与长宽比，再按需要继续缩小。
export function fitScale(width: number, height: number): { width: number; height: number } | null {
  if (width <= 0 || height <= 0) return null
  const aspect = width / height
  if (aspect > COMPRESS_MAX_ASPECT || 1 / aspect > COMPRESS_MAX_ASPECT) return null
  let scale = 1
  if (width + height > COMPRESS_MAX_DIMENSION_SUM) {
    scale = COMPRESS_MAX_DIMENSION_SUM / (width + height)
  }
  return {
    width: Math.max(1, Math.floor(width * scale)),
    height: Math.max(1, Math.floor(height * scale)),
  }
}

// compressImage 把静态图压到上限内：按体积逐档降质并缩放，产物满足宽高与长宽比约束。
export async function compressImage(blob: Blob): Promise<CompressResult> {
  if (blob.size > COMPRESS_MAX_INPUT_BYTES) {
    throw new Error('原图超过 64MB，请先用其他工具缩放')
  }
  const { bitmap, close } = await loadBitmap(blob)
  const sourceWidth = 'width' in bitmap ? bitmap.width : 0
  const sourceHeight = 'height' in bitmap ? bitmap.height : 0
  try {
    if (sourceWidth * sourceHeight > COMPRESS_MAX_INPUT_PIXELS) {
      throw new Error('原图像素超过 4000 万，请先用其他工具缩放')
    }
    const base = fitScale(sourceWidth, sourceHeight)
    if (!base) {
      throw new Error('原图长宽比超出 20:1，无法压缩')
    }
    let width = base.width
    let height = base.height
    const qualities = [0.9, 0.82, 0.74, 0.66, 0.58, 0.5]
    for (let round = 0; round < 6; round += 1) {
      for (const quality of qualities) {
        const encoded = await encodeScale(bitmap, width, height, quality)
        if (encoded.size <= COMPRESS_TARGET_BYTES) {
          return { blob: encoded, width, height, fromSize: blob.size, toSize: encoded.size }
        }
      }
      width = Math.max(1, Math.floor(width * 0.82))
      height = Math.max(1, Math.floor(height * 0.82))
      if (width + height <= 2) break
    }
    throw new Error('压缩后仍超过 10MB，请先裁剪图片')
  } finally {
    close()
  }
}

// 视频元信息：时长与首帧封面。
export interface VideoMeta {
  duration: number
  width: number
  height: number
  poster: Blob | null
}

// extractVideoMeta 读取时长并在首帧处截一张 WebP 作为封面；失败时返回空封面。
export async function extractVideoMeta(file: Blob): Promise<VideoMeta> {
  const url = URL.createObjectURL(file)
  const video = document.createElement('video')
  video.preload = 'metadata'
  video.muted = true
  video.playsInline = true
  try {
    await new Promise<void>((resolve, reject) => {
      video.onloadedmetadata = () => resolve()
      video.onerror = () => reject(new Error('视频读取失败'))
      video.src = url
    })
    const duration = Number.isFinite(video.duration) ? video.duration : 0
    const width = video.videoWidth
    const height = video.videoHeight
    const poster = await captureFrame(video, url, duration)
    return { duration, width, height, poster }
  } finally {
    video.removeAttribute('src')
    video.load()
    URL.revokeObjectURL(url)
  }
}

// captureFrame 在 0.1 秒处截一帧，避开黑场开头。
async function captureFrame(video: HTMLVideoElement, _url: string, duration: number): Promise<Blob | null> {
  const seekTo = duration > 0.2 ? Math.min(0.1, duration / 2) : 0
  await new Promise<void>((resolve) => {
    const done = (): void => resolve()
    video.onseeked = done
    try {
      video.currentTime = seekTo
    } catch {
      done()
    }
  })
  const width = video.videoWidth
  const height = video.videoHeight
  if (width <= 0 || height <= 0) return null
  const canvas = document.createElement('canvas')
  canvas.width = width
  canvas.height = height
  const context = canvas.getContext('2d')
  if (!context) return null
  context.drawImage(video, 0, 0, width, height)
  return new Promise((resolve) => {
    canvas.toBlob((blob) => resolve(blob), 'image/webp', 0.82)
  })
}

// compressionSummary 汇总本轮压缩的体积变化。
export function compressionSummary(results: CompressResult[]): string {
  if (results.length === 0) return ''
  const from = results.reduce((sum, item) => sum + item.fromSize, 0)
  const to = results.reduce((sum, item) => sum + item.toSize, 0)
  return `已自动压缩 ${results.length} 张超 10MB 静态图（合计 ${formatBytes(from)} → ${formatBytes(to)}）`
}

// 供界面显示的体积对比。
export function sizeCompare(fromSize: number, toSize: number): string {
  return `${formatBytes(fromSize)} → ${formatBytes(toSize)}`
}
