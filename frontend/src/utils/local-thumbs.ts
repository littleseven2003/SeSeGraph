// 工作台本地小图调度：按稳定项 ID 去重，生成前与回写前各确认一次该项仍然有效，
// 迟到的结果不会挂回已删除、已清空或已换一批的网格上。
import type { SerialQueue } from './serial.ts'

/** THUMB_MAX_BYTES 与落盘小图的硬上限一致，超过就不挂到网格上。 */
export const THUMB_MAX_BYTES = 150_000

// isWebpThumb 校验回写前的小图：必须是 WebP，且不超过落盘硬上限。
// 生成失败或格式不对时保留静默占位，避免把非 WebP 的 Blob 挂进网格。
export async function isWebpThumb(blob: Blob): Promise<boolean> {
  if (blob.size === 0 || blob.size > THUMB_MAX_BYTES) return false
  if (blob.type !== '' && blob.type !== 'image/webp') return false
  const head = new Uint8Array(await blob.slice(0, 12).arrayBuffer())
  if (head.length < 12) return false
  const tag = (offset: number, length: number): string => {
    let text = ''
    for (let index = 0; index < length; index += 1) text += String.fromCharCode(head[offset + index])
    return text
  }
  return tag(0, 4) === 'RIFF' && tag(8, 4) === 'WEBP'
}

/** LocalThumbHooks 是调度器与界面之间的四个接口。 */
export interface LocalThumbHooks {
  /** isAlive 报告该项是否仍在当前列表里（按稳定 ID 判断） */
  isAlive: (id: string) => boolean
  /** render 生成该项的小图 */
  render: (id: string) => Promise<Blob>
  /** attach 把校验通过的小图挂回网格 */
  attach: (id: string, blob: Blob) => void
  /** valid 校验回写前的小图，默认使用 WebP 与体积上限校验 */
  valid?: (blob: Blob) => Promise<boolean>
}

/**
 * LocalThumbScheduler 把逐图生成排进串行队列：
 * 同一 ID 只排一次（重排会复制卡片对象，不能因此重复排队）；
 * 生成前不再需要就跳过，回写前再确认一次，清空或离页后迟到的结果直接丢弃。
 */
export class LocalThumbScheduler {
  private readonly queue: SerialQueue
  private readonly hooks: LocalThumbHooks
  private readonly pending = new Set<string>()
  private epoch = 0
  private disposed = false

  constructor(queue: SerialQueue, hooks: LocalThumbHooks) {
    this.queue = queue
    this.hooks = hooks
  }

  /** request 排队生成一项；已在排队、已生成或已失效的请求会被忽略。 */
  request(id: string): void {
    if (this.disposed || this.pending.has(id) || !this.hooks.isAlive(id)) return
    this.pending.add(id)
    const epoch = this.epoch
    void this.queue.push(async () => {
      try {
        if (!this.alive(epoch, id)) return
        const blob = await this.hooks.render(id)
        // 生成期间可能被删除、清空或换了一批文件，回写前再确认一次。
        if (!this.alive(epoch, id)) return
        const valid = this.hooks.valid ?? isWebpThumb
        if (!(await valid(blob))) return
        this.hooks.attach(id, blob)
      } catch {
        // 生成失败保留占位，下一项照常处理。
      } finally {
        this.pending.delete(id)
      }
    })
  }

  /** has 报告该项是否仍在排队或生成中。 */
  has(id: string): boolean {
    return this.pending.has(id)
  }

  /** size 返回仍在排队或生成中的项数。 */
  size(): number {
    return this.pending.size
  }

  /** reset 换一批文件或清空后调用：在途结果作废，去重集合清空。 */
  reset(): void {
    this.epoch += 1
    this.pending.clear()
  }

  /** dispose 离页时调用：此后不再接受任何生成结果。 */
  dispose(): void {
    this.reset()
    this.disposed = true
  }

  private alive(epoch: number, id: string): boolean {
    return !this.disposed && epoch === this.epoch && this.hooks.isAlive(id)
  }
}
