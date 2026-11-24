// 压缩包解压的调用侧：优先交给 Worker 逐项展开，环境不支持时回落到主线程的同一套流程。
import {
  unpackZipProgressive,
  type ArchiveImage,
  type ArchiveLimits,
  type ProgressiveMeta,
} from './zip.ts'

/** ZipOptions 是解压时可覆盖的说明文件名与展开限制。 */
export interface ZipOptions {
  metaNames?: readonly string[]
  limits?: Partial<ArchiveLimits>
}

/** imageBlob 把解压出的字节包成 Blob：直接复用同一段内存，不再复制一份大图。 */
export function imageBlob(image: ArchiveImage, type: string): Blob {
  return new Blob([image.data as Uint8Array<ArrayBuffer>], { type })
}

/** ZipOutcome 是解压结果：图片按自然排序给出，说明文件与跳过计数一并返回。 */
export type ZipOutcome =
  | ({ ok: true; images: ArchiveImage[] } & ProgressiveMeta)
  | { ok: false; error: string; cancelled?: boolean }

interface WorkerReply {
  id: number
  type: 'image' | 'done' | 'error'
  name?: string
  data?: ArrayBuffer
  error?: string
  metaText?: string
  metaName?: string
  skipped?: number
  metaError?: string
}

interface PendingTask {
  resolve: (outcome: ZipOutcome) => void
  images: ArchiveImage[]
  fail: (error: string) => void
  /** onImage 给出时逐项交接，客户端不再累积整包展开字节 */
  onImage?: (image: ArchiveImage) => void
}

let worker: Worker | null = null
let nextId = 1
const pending = new Map<number, PendingTask>()

// ensureWorker 懒创建 Worker：测试与老浏览器不实例化任何线程。
function ensureWorker(): Worker | null {
  if (worker !== null) return worker
  if (typeof Worker !== 'function') return null
  try {
    worker = new Worker(new URL('./zip.worker.ts', import.meta.url), { type: 'module' })
  } catch {
    return null
  }
  worker.onmessage = (event: MessageEvent<WorkerReply>) => {
    const reply = event.data
    const task = pending.get(reply.id)
    if (task === undefined) return
    if (reply.type === 'image') {
      if (reply.name !== undefined && reply.data !== undefined) {
        const image = { name: reply.name, data: new Uint8Array(reply.data) }
        // 有逐项回调就立刻交接，主线程只短暂持有单张字节。
        if (task.onImage !== undefined) task.onImage(image)
        else task.images.push(image)
      }
      return
    }
    pending.delete(reply.id)
    if (reply.type === 'error') {
      task.resolve({ ok: false, error: reply.error ?? '压缩包读取失败，请确认文件完整且没有加密' })
      return
    }
    task.resolve({
      ok: true,
      images: task.images,
      metaText: reply.metaText ?? '',
      metaName: reply.metaName ?? '',
      skipped: reply.skipped ?? 0,
      metaError: reply.metaError ?? '',
    })
  }
  // Worker 崩溃时整批任务一起失败，界面给出可重试的说明而不是一直转圈。
  worker.onerror = () => {
    const tasks = Array.from(pending.values())
    pending.clear()
    worker?.terminate()
    worker = null
    for (const task of tasks) task.fail('压缩包读取失败，请重新选择文件')
  }
  return worker
}

/**
 * unpackArchive 解压一个压缩包。
 * 有 Worker 时整包交给线程池逐项展开；否则在同一套逐项流程里同步执行，
 * 两种路径的排序、限制与说明文件解析完全一致。
 */
export async function unpackArchive(
  file: File,
  options: ZipOptions = {},
  onImage?: (image: ArchiveImage) => void,
): Promise<ZipOutcome> {
  const target = ensureWorker()
  if (target === null) {
    // 没有 Worker 时才在主线程整包读入，流程与线程内一致。
    const buffer = await file.arrayBuffer()
    const images: ArchiveImage[] = []
    const result = unpackZipProgressive(new Uint8Array(buffer), options, (image) => {
      if (onImage !== undefined) onImage(image)
      else images.push(image)
    })
    if (!result.ok) return result
    return { ...result, images }
  }
  return await new Promise<ZipOutcome>((resolve) => {
    const id = nextId
    nextId += 1
    pending.set(id, {
      resolve,
      images: [],
      onImage,
      fail: (error) => resolve({ ok: false, error }),
    })
    // 只把文件句柄交给线程：整包字节由 Worker 自己读，主线程不额外占一整份。
    target.postMessage({
      id,
      file,
      metaNames: options.metaNames === undefined ? undefined : [...options.metaNames],
      limits: options.limits,
    })
  })
}

/**
 * cancelArchive 取消进行中的解压并立即释放线程持有的整包字节。
 * 用于生成中清空列表或离开工作台：不必等一批大包解完才回收内存。
 */
export function cancelArchive(reason = '压缩包读取已取消'): void {
  const tasks = Array.from(pending.values())
  pending.clear()
  if (worker !== null) {
    worker.terminate()
    worker = null
  }
  for (const task of tasks) task.resolve({ ok: false, error: reason, cancelled: true })
}

/** disposeArchiveWorker 离开工作台时关闭解压线程，避免空闲线程常驻占用内存。 */
export function disposeArchiveWorker(): void {
  if (worker !== null) {
    worker.terminate()
    worker = null
  }
  const tasks = Array.from(pending.values())
  pending.clear()
  for (const task of tasks) task.fail('压缩包读取已取消')
}
