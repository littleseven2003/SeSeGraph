// 工作台图片列表的纯函数操作，便于单独验证排序与序号计算。
import { sortByNaturalName } from './natural.ts'

// 工作台里的一张待上传图片。
export interface WorkbenchImage {
  /** 本地唯一标识，用于预览地址与列表 key */
  id: string
  /** 原始文件名，含 ZIP 内目录 */
  name: string
  size: number
  mime: string
  blob: Blob
  /** 上传序号，从 1 开始，与服务端 sort_order 一致 */
  sortOrder: number
}

let sequence = 0

// createId 生成列表内唯一的本地标识。
export function createId(): string {
  sequence += 1
  return `img-${sequence}`
}

// assignSortOrders 按当前顺序重排上传序号，返回新数组。
export function assignSortOrders<T extends { sortOrder: number }>(items: readonly T[]): T[] {
  return items.map((item, index) => ({ ...item, sortOrder: index + 1 }))
}

// moveItem 把 from 位置的元素移动到 to 位置，越界时原样返回。
export function moveItem<T>(items: readonly T[], from: number, to: number): T[] {
  if (from === to || from < 0 || to < 0 || from >= items.length || to >= items.length) {
    return items.slice()
  }
  const next = items.slice()
  const [moved] = next.splice(from, 1)
  next.splice(to, 0, moved)
  return next
}

// removeItem 移除指定元素，找不到时原样返回。
export function removeItem<T extends { id: string }>(items: readonly T[], id: string): T[] {
  return items.filter((item) => item.id !== id)
}

// sortByName 按文件名自然排序并重排序号。
export function sortByName<T extends { name: string; sortOrder: number }>(items: readonly T[]): T[] {
  return assignSortOrders(sortByNaturalName(items, (item) => item.name))
}

// selectCover 点选封面；封面不存在时默认取第一张。
export function selectCover(items: readonly { id: string }[], coverId: string): string {
  if (items.length === 0) return ''
  if (items.some((item) => item.id === coverId)) return coverId
  return items[0].id
}

// totalBytes 汇总当前待上传体积。
export function totalBytes(items: readonly { size: number }[]): number {
  return items.reduce((sum, item) => sum + item.size, 0)
}

// 界面展示用的图片项，附带浏览器内生成的预览地址。
export interface WorkbenchTile extends WorkbenchImage {
  previewUrl: string
}

// 发布前的必填校验：续传沿用旧任务已保存的元数据，不再要求重新填写。
export interface PublishInput {
  title: string
  coverReady: boolean
  resuming: boolean
}

export function validatePublishInput(input: PublishInput): string | null {
  if (input.resuming) return null
  if (input.title.trim() === '') return '请填写总标题'
  if (!input.coverReady) return '封面还在生成，请稍后重试'
  return null
}
