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

// appendSelection 决定新选择是追加还是替换：续传时失配的选择需要整体替换。
export function appendSelection<T>(existing: readonly T[], incoming: readonly T[], replace: boolean): T[] {
  return replace ? [...incoming] : [...existing, ...incoming]
}

// 上传队列条目：序号来自服务端进度，文件来自开始上传时的列表快照。
export interface UploadItem<T> {
  tile: T
  sortOrder: number
}

// planUploadQueue 用固定下来的列表生成上传队列，后续列表变化不影响已排定的请求。
export function planUploadQueue<T>(
  items: readonly T[],
  pending: readonly { index: number; sortOrder: number }[],
): UploadItem<T>[] {
  const queue: UploadItem<T>[] = []
  for (const item of pending) {
    const tile = items[item.index]
    if (tile === undefined) continue
    queue.push({ tile, sortOrder: item.sortOrder })
  }
  return queue
}

// 工作台阶段，与视图里的阶段取值保持一致。
export type WorkbenchPhase = 'idle' | 'creating' | 'uploading' | 'queued' | 'failed'

// discardBlocked 判断当前阶段是否禁止放弃任务：
// 创建或上传进行中放弃会解锁列表，可能让未发出的请求取到新选的文件。
export function discardBlocked(phase: WorkbenchPhase): boolean {
  return phase === 'creating' || phase === 'uploading'
}

// quotaUsed 返回本次选择可用的张数基数：续传重选按新批次计算，不占用旧列表配额。
export function quotaUsed(existingCount: number, replace: boolean): number {
  return replace ? 0 : existingCount
}
