// 在浏览器内解压 ZIP：先读中央目录筛选条目并检查限制，只展开命中的图片与说明文件。
import { unzipSync } from 'fflate'
import { baseName, isImageName } from './image.ts'
import { sortByNaturalName } from './natural.ts'

// 默认识别的元信息文件名，匹配时不区分大小写。
export const DEFAULT_META_FILENAMES = ['info.txt', 'meta.txt', 'readme.txt']

// 单个压缩包的展开限制，避免一次解压把页面拖死。
export interface ArchiveLimits {
  /** 允许的图片张数上限 */
  maxImages: number
  /** 单张图片展开后的上限 */
  maxImageBytes: number
  /** 全部图片展开后的总上限 */
  maxTotalBytes: number
  /** 说明文件展开后的上限，与图片限制相互独立 */
  maxMetaBytes: number
}

export const DEFAULT_ARCHIVE_LIMITS: ArchiveLimits = {
  maxImages: 500,
  maxImageBytes: 20 * 1024 * 1024,
  maxTotalBytes: 512 * 1024 * 1024,
  maxMetaBytes: 64 * 1024,
}

// 解压后的单张图片。
export interface ArchiveImage {
  name: string
  data: Uint8Array
}

// 解压结果：成功时给出图片与说明文本，超限时只返回原因。
export interface UnpackedArchive {
  images: ArchiveImage[]
  metaText: string
  /** 命中的元信息文件名，未命中时为空串 */
  metaName: string
  /** 被跳过的非图片条目数量，用于界面提示 */
  skipped: number
  /** 说明文件被忽略的原因，为空表示没有异常 */
  metaError: string
}

export type UnpackResult = ({ ok: true } & UnpackedArchive) | { ok: false; error: string }

interface ArchiveOptions {
  metaNames?: readonly string[]
  limits?: Partial<ArchiveLimits>
}

function isJunkPath(name: string): boolean {
  const normalized = name.replace(/\\/g, '/')
  return normalized.startsWith('__MACOSX/') || baseName(normalized).startsWith('.')
}

function isDirectoryEntry(name: string): boolean {
  return name.endsWith('/')
}

// formatSize 按实际量级显示上限，避免小阈值被显示成 0MB。
function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes}B`
  if (bytes < 1024 * 1024) return `${Math.round(bytes / 1024)}KB`
  return `${Math.floor(bytes / (1024 * 1024))}MB`
}

// unpackZip 分两遍处理压缩包：
// 第一遍只遍历中央目录（filter 一律返回 false，不展开数据），据此执行数量、单张与总量限制；
// 第二遍只展开白名单内的条目。
export function unpackZip(data: Uint8Array, options: ArchiveOptions = {}): UnpackResult {
  const limits: ArchiveLimits = { ...DEFAULT_ARCHIVE_LIMITS, ...options.limits }
  const wantedMeta = new Set((options.metaNames ?? DEFAULT_META_FILENAMES).map((name) => name.toLowerCase()))

  const images: { name: string; size: number }[] = []
  const wanted = new Set<string>()
  let metaEntry = ''
  let metaName = ''
  let metaError = ''
  let skipped = 0
  let totalBytes = 0
  let overCount = 0
  let oversizeName = ''
  let overTotal = false

  // 第一遍：只登记条目，不展开任何数据。
  unzipSync(data, {
    filter: (file) => {
      if (isDirectoryEntry(file.name) || isJunkPath(file.name)) return false
      if (isImageName(file.name)) {
        if (images.length >= limits.maxImages || overCount > 0) {
          overCount += 1
          return false
        }
        const size = file.originalSize
        if (size > limits.maxImageBytes) {
          if (oversizeName === '') oversizeName = baseName(file.name)
          return false
        }
        if (totalBytes + size > limits.maxTotalBytes) {
          overTotal = true
          return false
        }
        totalBytes += size
        images.push({ name: file.name, size })
        wanted.add(file.name)
        return false
      }
      if (metaEntry === '' && wantedMeta.has(baseName(file.name).toLowerCase())) {
        // 说明文件单独限制大小，避免异常压缩包用它撑爆内存。
        if (file.originalSize > limits.maxMetaBytes) {
          metaError = `${baseName(file.name)} 超过 ${formatSize(limits.maxMetaBytes)} 上限，已忽略`
          skipped += 1
          return false
        }
        metaEntry = file.name
        metaName = baseName(file.name)
        wanted.add(file.name)
        return false
      }
      skipped += 1
      return false
    },
  })

  if (overCount > 0) {
    return { ok: false, error: `压缩包内图片超过 ${limits.maxImages} 张，请先精选后再上传` }
  }
  if (oversizeName !== '') {
    return {
      ok: false,
      error: `${oversizeName} 超过 ${formatSize(limits.maxImageBytes)} 上限，请压缩后重试`,
    }
  }
  if (overTotal) {
    return {
      ok: false,
      error: `压缩包展开后超过 ${formatSize(limits.maxTotalBytes)}，请分批上传`,
    }
  }
  if (images.length === 0) {
    return { ok: false, error: '压缩包里没有找到受支持的图片' }
  }

  // 第二遍：只展开白名单条目。
  const files = unzipSync(data, {
    filter: (file) => wanted.has(file.name),
  })

  const unpacked: ArchiveImage[] = []
  let actualBytes = 0
  for (const image of images) {
    const bytes = files[image.name]
    if (!bytes) continue
    actualBytes += bytes.length
    if (actualBytes > limits.maxTotalBytes) {
      return {
        ok: false,
        error: `压缩包展开后超过 ${formatSize(limits.maxTotalBytes)}，请分批上传`,
      }
    }
    unpacked.push({ name: image.name, data: bytes })
  }

  const metaBytes = metaEntry !== '' ? files[metaEntry] : undefined
  return {
    ok: true,
    images: sortByNaturalName(unpacked, (image) => image.name),
    metaText: metaBytes ? new TextDecoder('utf-8').decode(metaBytes) : '',
    metaName,
    skipped,
    metaError,
  }
}
