// 在浏览器内解压 ZIP，只保留图片与可选的元信息文本文件。
import { unzipSync } from 'fflate'
import { baseName, isImageName } from './image.ts'
import { sortByNaturalName } from './natural.ts'

// 默认识别的元信息文件名，匹配时不区分大小写。
export const DEFAULT_META_FILENAMES = ['info.txt', 'meta.txt', 'readme.txt']

// 解压后的单张图片。
export interface ArchiveImage {
  name: string
  data: Uint8Array
}

// 解压结果：图片按自然排序，元信息文本可为空。
export interface UnpackedArchive {
  images: ArchiveImage[]
  metaText: string
  /** 被跳过的条目数量，用于界面提示 */
  skipped: number
}

function isJunkPath(name: string): boolean {
  const normalized = name.replace(/\\/g, '/')
  return normalized.startsWith('__MACOSX/') || baseName(normalized).startsWith('.')
}

function isDirectoryEntry(name: string): boolean {
  return name.endsWith('/')
}

// unpackZip 同步解压，传入完整压缩包字节。浏览器端执行，不占用服务端资源。
export function unpackZip(data: Uint8Array, metaNames: readonly string[] = DEFAULT_META_FILENAMES): UnpackedArchive {
  const wanted = new Set(metaNames.map((name) => name.toLowerCase()))
  const files = unzipSync(data, {
    filter: (file) => !isDirectoryEntry(file.name) && !isJunkPath(file.name),
  })

  const images: ArchiveImage[] = []
  let metaBytes: Uint8Array | null = null
  let skipped = 0

  for (const [name, bytes] of Object.entries(files)) {
    if (isImageName(name)) {
      images.push({ name, data: bytes })
      continue
    }
    if (metaBytes === null && wanted.has(baseName(name).toLowerCase())) {
      metaBytes = bytes
      continue
    }
    skipped += 1
  }

  return {
    images: sortByNaturalName(images, (image) => image.name),
    metaText: metaBytes ? new TextDecoder('utf-8').decode(metaBytes) : '',
    skipped,
  }
}
