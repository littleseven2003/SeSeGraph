// 图片类型判定与单张大小限制，规则与后端保持一致。

export const IMAGE_EXTENSIONS = ['jpg', 'jpeg', 'png', 'webp', 'gif'] as const

// 扩展名到 MIME 的映射，上传时用于 declare 内容类型。
const MIME_BY_EXTENSION: Record<string, string> = {
  jpg: 'image/jpeg',
  jpeg: 'image/jpeg',
  png: 'image/png',
  webp: 'image/webp',
  gif: 'image/gif',
}

// 单张图片上限，与后端 LimitForMode 对齐。
export const MAX_PHOTO_BYTES = 10 * 1024 * 1024
export const MAX_DOCUMENT_BYTES = 20 * 1024 * 1024
// 单帖图片数量上限，与后端一致。
export const MAX_IMAGE_COUNT = 500
// 封面缩略图宽度，浏览器端生成的 WebP 约 20–40KB。
export const COVER_WIDTH = 400

// 压缩前用于判定体积限制的文件信息。
export interface FileSizeInfo {
  name: string
  size: number
}

export type UploadMode = 'photo' | 'document'

// baseName 取路径最后一段，兼容 ZIP 内的目录结构。
export function baseName(path: string): string {
  const normalized = path.replace(/\\/g, '/')
  const index = normalized.lastIndexOf('/')
  return index >= 0 ? normalized.slice(index + 1) : normalized
}

// extensionOf 返回小写扩展名，没有扩展名时返回空串。
export function extensionOf(name: string): string {
  const base = baseName(name)
  const index = base.lastIndexOf('.')
  if (index <= 0 || index === base.length - 1) {
    return ''
  }
  return base.slice(index + 1).toLowerCase()
}

// isImageName 判断文件名是否属于支持的图片类型。
export function isImageName(name: string): boolean {
  return IMAGE_EXTENSIONS.includes(extensionOf(name) as (typeof IMAGE_EXTENSIONS)[number])
}

// mimeForName 返回扩展名对应的 MIME，未知扩展名返回空串。
export function mimeForName(name: string): string {
  return MIME_BY_EXTENSION[extensionOf(name)] ?? ''
}

// limitForMode 返回指定画质模式下的单张上限。
export function limitForMode(mode: UploadMode): number {
  return mode === 'document' ? MAX_DOCUMENT_BYTES : MAX_PHOTO_BYTES
}

// checkFile 返回不放行的原因，放行时返回 null。
export function checkFile(file: FileSizeInfo, mode: UploadMode, count: number): string | null {
  if (!isImageName(file.name)) {
    return `${baseName(file.name)} 不是支持的图片格式`
  }
  const limit = limitForMode(mode)
  if (file.size > limit) {
    const megabyte = (limit / (1024 * 1024)).toFixed(0)
    return `${baseName(file.name)} 超过 ${megabyte}MB 上限`
  }
  if (count > MAX_IMAGE_COUNT) {
    return `单个帖子最多 ${MAX_IMAGE_COUNT} 张图片`
  }
  return null
}

// formatBytes 输出便于阅读的体积文案。
export function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(0)} KB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
}
