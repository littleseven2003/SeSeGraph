// 导入即压缩：按稳定 ID 去重、串行执行，清空或换模式后旧结果作废。
// 复用工作台同一条串行队列，同一时刻只解码一张原图，低配机器不会被并行压缩顶爆。
import type { SerialQueue } from './serial.ts'

export interface LocalCompressHooks {
  /** isAlive 报告该项是否仍在当前列表里 */
  isAlive: (id: string) => boolean
  /** run 完成一次压缩并写回该项；异常由调用方在 run 内处理 */
  run: (id: string) => Promise<void>
}

/** LocalCompressScheduler 只负责去重、顺序与作废，压缩策略仍由调用方决定。 */
export class LocalCompressScheduler {
  private readonly queue: SerialQueue
  private readonly hooks: LocalCompressHooks
  private readonly pending = new Set<string>()
  private epoch = 0
  private disposed = false

  constructor(queue: SerialQueue, hooks: LocalCompressHooks) {
    this.queue = queue
    this.hooks = hooks
  }

  /** request 排队压缩一项；已在排队、已失效或已压缩的项由调用方先过滤。 */
  request(id: string): void {
    if (this.disposed || this.pending.has(id) || !this.hooks.isAlive(id)) return
    this.pending.add(id)
    const epoch = this.epoch
    void this.queue.push(async () => {
      try {
        if (!this.alive(epoch, id)) return
        await this.hooks.run(id)
      } catch {
        // 单项失败保留原图占位，下一项照常处理。
      } finally {
        this.pending.delete(id)
      }
    })
  }

  has(id: string): boolean {
    return this.pending.has(id)
  }

  size(): number {
    return this.pending.size
  }

  /** idle 等队列清空：提交前用它避免同图被压缩两次。 */
  idle(): Promise<void> {
    return this.queue.idle()
  }

  /** reset 换模式或清空后调用：在途结果作废，去重集合清空。 */
  reset(): void {
    this.epoch += 1
    this.pending.clear()
  }

  dispose(): void {
    this.reset()
    this.disposed = true
  }

  private alive(epoch: number, id: string): boolean {
    return !this.disposed && epoch === this.epoch && this.hooks.isAlive(id)
  }
}
