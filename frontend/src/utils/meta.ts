// 解析压缩包内元信息文本，支持两种模式：首行标题、键值匹配。

export type ParseMode = 'first-line' | 'key-value'

// 帖子元信息，与建任务接口字段一致。
export interface PostMeta {
  title: string
  description: string
  tags: string[]
  author: string
}

// 标签分隔符：逗号、中文逗号、井号与空白。
export function splitTags(raw: string): string[] {
  const seen = new Set<string>()
  const tags: string[] = []
  for (const piece of raw.split(/[,，、#\s]+/)) {
    const tag = piece.trim()
    if (tag === '') continue
    const key = tag.toLowerCase()
    if (seen.has(key)) continue
    seen.add(key)
    tags.push(tag)
  }
  return tags
}

const KEY_ALIASES: Record<string, keyof PostMeta> = {
  title: 'title',
  标题: 'title',
  description: 'description',
  desc: 'description',
  说明: 'description',
  描述: 'description',
  正文: 'description',
  tags: 'tags',
  标签: 'tags',
  author: 'author',
  作者: 'author',
}

function parseFirstLine(lines: string[]): PostMeta {
  const firstIndex = lines.findIndex((line) => line.trim() !== '')
  if (firstIndex < 0) {
    return { title: '', description: '', tags: [], author: '' }
  }
  return {
    title: lines[firstIndex].trim(),
    description: lines.slice(firstIndex + 1).join('\n').trim(),
    tags: [],
    author: '',
  }
}

function parseKeyValue(lines: string[]): PostMeta | null {
  const meta: PostMeta = { title: '', description: '', tags: [], author: '' }
  let matched = false
  const descriptionLines: string[] = []
  for (const line of lines) {
    const separator = line.search(/[:：]/)
    if (separator <= 0) {
      if (matched && line.trim() !== '') descriptionLines.push(line.trim())
      continue
    }
    const rawKey = line.slice(0, separator).trim().toLowerCase()
    const field = KEY_ALIASES[rawKey]
    if (!field) {
      if (matched && line.trim() !== '') descriptionLines.push(line.trim())
      continue
    }
    matched = true
    const value = line.slice(separator + 1).trim()
    if (field === 'tags') {
      meta.tags = splitTags(value)
    } else {
      meta[field] = value
    }
  }
  if (!matched) return null
  if (descriptionLines.length > 0) {
    meta.description = [meta.description, ...descriptionLines].filter((part) => part !== '').join('\n')
  }
  return meta
}

// parseMetaText 按指定模式解析文本；键值模式无命中时回退到首行标题。
export function parseMetaText(text: string, mode: ParseMode): PostMeta {
  const lines = text.replace(/^\uFEFF/, '').split(/\r?\n/)
  if (mode === 'key-value') {
    return parseKeyValue(lines) ?? parseFirstLine(lines)
  }
  return parseFirstLine(lines)
}
