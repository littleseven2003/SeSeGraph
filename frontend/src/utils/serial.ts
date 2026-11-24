// 串行任务队列：同一时刻只跑一个任务，失败不会打断后续项。
// 工作台用它排队逐图小图生成，两路并行上传也不会同时展开两张原图。
export interface SerialQueue {
  /** push 把任务排到队尾，返回的 Promise 在本次任务结束时兑现，不会拒绝。 */
  push<T>(task: () => Promise<T>): Promise<void>
  /** size 返回仍在排队的任务数，不含正在执行的那个。 */
  size(): number
  /** idle 在所有已排队任务结束后兑现。 */
  idle(): Promise<void>
}

// createSerialQueue 创建一个串行队列。
export function createSerialQueue(): SerialQueue {
  let tail: Promise<void> = Promise.resolve()
  let pending = 0
  return {
    push<T>(task: () => Promise<T>): Promise<void> {
      pending += 1
      tail = tail
        .then(() => task())
        .then(
          () => undefined,
          () => undefined,
        )
        .then(() => {
          pending -= 1
        })
      return tail
    },
    size(): number {
      return pending
    },
    idle(): Promise<void> {
      return tail
    },
  }
}
