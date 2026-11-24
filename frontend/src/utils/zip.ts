// 在浏览器内解压 ZIP：先读中央目录筛选条目并检查限制，只展开命中的图片与说明文件。
import { unzipSync } from 'fflate'
import { baseName, isMediaName, isVideoName } from './image.ts'
import { sortByNaturalName } from './natural.ts'

// 默认识别的元信息文件名，匹配时不区分大小写。
export const DEFAULT_META_FILENAMES = ['info.txt', 'meta.txt', 'readme.txt']

// 单个压缩包的展开限制，避免一次解压把页面拖死。
export interface ArchiveLimits {
  /** 允许的图片张数上限 */
  maxImages: number
  /** 单张图片展开后的上限 */
  maxImageBytes: number
  /** 单段视频的上限，与 Telegram 云端 getFile 一致 */
  maxVideoBytes: number
  /** 全部图片展开后的总上限 */
  maxTotalBytes: number
  /** 说明文件展开后的上限，与图片限制相互独立 */
  maxMetaBytes: number
  /** 压缩文件本身体积上限：整包读入前先拦，避免把超大文件塞进内存 */
  maxArchiveBytes: number
}

export const DEFAULT_ARCHIVE_LIMITS: ArchiveLimits = {
  maxImages: 500,
  maxImageBytes: 20 * 1024 * 1024,
  maxVideoBytes: 20000000,
  maxTotalBytes: 512 * 1024 * 1024,
  maxMetaBytes: 64 * 1024,
  maxArchiveBytes: 512 * 1024 * 1024,
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

// ArchivePlan 是中央目录扫描的结果：只登记条目与限制结论，不展开数据。
export interface ArchivePlan {
  images: { name: string; size: number }[]
  /** 命中的说明文件条目名，未命中时为空串 */
  metaEntry: string
  metaName: string
  /** 说明文件展开后的体积，用于第二遍核对 */
  metaSize: number
  skipped: number
  metaError: string
}

export type ScanResult = { ok: true; plan: ArchivePlan } | { ok: false; error: string }

// scanArchive 只遍历中央目录并执行数量、单项与总量限制，不展开任何数据。
// 第一遍一律让 filter 返回 false，ZIP 的条目信息已经够做全部判断。
export function scanArchive(data: Uint8Array, options: ArchiveOptions = {}): ScanResult {
  const limits: ArchiveLimits = { ...DEFAULT_ARCHIVE_LIMITS, ...options.limits }
  const wantedMeta = new Set((options.metaNames ?? DEFAULT_META_FILENAMES).map((name) => name.toLowerCase()))

  const images: { name: string; size: number }[] = []
  let metaEntry = ''
  let metaName = ''
  let metaSize = 0
  let metaError = ''
  let skipped = 0
  let totalBytes = 0
  let overCount = 0
  let overTotal = false

  unzipSync(data, {
    filter: (file) => {
      if (isDirectoryEntry(file.name) || isJunkPath(file.name)) return false
      if (isMediaName(file.name)) {
        if (images.length >= limits.maxImages || overCount > 0) {
          overCount += 1
          return false
        }
        const size = file.originalSize
        // 视频按 20,000,000 字节限制，静态图上限由调用方按画质模式传入。
        const limit = isVideoName(file.name) ? limits.maxVideoBytes : limits.maxImageBytes
        if (size >= limit) {
          // 单项超限只跳过该项，不因一个文件拒掉整个压缩包。
          skipped += 1
          return false
        }
        if (totalBytes + size > limits.maxTotalBytes) {
          overTotal = true
          return false
        }
        totalBytes += size
        images.push({ name: file.name, size })
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
        metaSize = file.originalSize
        return false
      }
      skipped += 1
      return false
    },
  })

  if (overCount > 0) {
    return { ok: false, error: `压缩包内图片超过 ${limits.maxImages} 张，请先精选后再上传` }
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
  return { ok: true, plan: { images, metaEntry, metaName, metaSize, skipped, metaError } }
}

// expandEntry 只展开白名单里的一项，把峰值压在单张体积上。
// ZIP 允许同名条目重复出现，因此除了名字还要比对扫描时登记的原始大小。
export function expandEntry(data: Uint8Array, name: string, size: number): Uint8Array | null {
  // 同名条目只展开第一份，避免用同名副本绕过说明文件与单图上限。
  let taken = false
  const files = unzipSync(data, {
    filter: (file) => {
      if (taken) return false
      if (file.name !== name || file.originalSize !== size) return false
      taken = true
      return true
    },
  })
  return files[name] ?? null
}

// ProgressiveResult 是逐项展开的说明文件信息；图片通过回调边展开边交给调用方。
export interface ProgressiveMeta {
  metaText: string
  metaName: string
  skipped: number
  metaError: string
}

// unpackZipProgressive 逐项展开压缩包：每次只解开一项，展开后立刻交给 onImage，
// 不在内存里攒整包。Worker 里用它，主线程不会因为解压大包而卡住。
export function unpackZipProgressive(
  data: Uint8Array,
  options: ArchiveOptions,
  onImage: (image: ArchiveImage) => void,
): ({ ok: true } & ProgressiveMeta) | { ok: false; error: string } {
  const limits: ArchiveLimits = { ...DEFAULT_ARCHIVE_LIMITS, ...options.limits }
  const scan = scanArchive(data, options)
  if (!scan.ok) return scan
  const plan = scan.plan

  let actualBytes = 0
  for (const image of sortByNaturalName(plan.images, (entry) => entry.name)) {
    const bytes = expandEntry(data, image.name, image.size)
    if (bytes === null) continue
    actualBytes += bytes.length
    if (actualBytes > limits.maxTotalBytes) {
      return {
        ok: false,
        error: `压缩包展开后超过 ${formatSize(limits.maxTotalBytes)}，请分批上传`,
      }
    }
    onImage({ name: image.name, data: bytes })
  }

  const metaBytes = plan.metaEntry !== '' ? expandEntry(data, plan.metaEntry, plan.metaSize) : null
  return {
    ok: true,
    metaText: metaBytes ? new TextDecoder('utf-8').decode(metaBytes) : '',
    metaName: plan.metaName,
    skipped: plan.skipped,
    metaError: plan.metaError,
  }
}

// unpackZip 同步展开整包，供不支持 Worker 的环境与单元测试使用。
export function unpackZip(data: Uint8Array, options: ArchiveOptions = {}): UnpackResult {
  const limits: ArchiveLimits = { ...DEFAULT_ARCHIVE_LIMITS, ...options.limits }
  const images: ArchiveImage[] = []
  let actualBytes = 0
  const result = unpackZipProgressive(data, options, (image) => {
    actualBytes += image.data.length
    images.push(image)
  })
  if (!result.ok) return result
  if (actualBytes > limits.maxTotalBytes) {
    return {
      ok: false,
      error: `压缩包展开后超过 ${formatSize(limits.maxTotalBytes)}，请分批上传`,
    }
  }
  return {
    ok: true,
    images,
    metaText: result.metaText,
    metaName: result.metaName,
    skipped: result.skipped,
    metaError: result.metaError,
  }
}
