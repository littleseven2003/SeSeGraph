// 压缩包解压 Worker：逐项展开并立刻把结果交给主线程，主线程不会因为大包卡住。
// 每交一项都等主线程确认后再展开下一张，任何时刻只有一张解压结果在两边之间流转。
import { DEFAULT_ARCHIVE_LIMITS, expandEntry, scanArchive, type ArchiveLimits, type ScanResult } from './zip.ts'
import { sortByNaturalName } from './natural.ts'

interface ZipRequest {
  type?: 'unpack'
  id: number
  file: File
  metaNames?: string[]
  limits?: Partial<ArchiveLimits>
}

interface ZipAck {
  type: 'ack'
  id: number
}

interface ZipScope {
  postMessage: (message: unknown, transfer?: Transferable[]) => void
  onmessage: ((event: MessageEvent<ZipRequest | ZipAck>) => void) | null
}

const scope = self as unknown as ZipScope

// 等待主线程确认的待决项：按 id 保存一次 resolve，确认后继续展开。
const acks = new Map<number, () => void>()
let active = 0

scope.onmessage = (event: MessageEvent<ZipRequest | ZipAck>) => {
  const message = event.data
  if ('type' in message && message.type === 'ack') {
    const release = acks.get(message.id)
    if (release !== undefined) {
      acks.delete(message.id)
      release()
    }
    return
  }
  const request = message as ZipRequest
  active = request.id
  void unpackInWorker(request.id, request.file, request.metaNames, request.limits)
}

// waitAck 让下一项等到主线程处理完上一项再开始，避免整批字节同时压进主线程。
function waitAck(id: number, deadlineMs = 10000): Promise<void> {
  return new Promise((resolve) => {
    if (active !== id) {
      resolve()
      return
    }
    let settled = false
    const finish = (): void => {
      if (settled) return
      settled = true
      clearTimeout(timer)
      acks.delete(id)
      resolve()
    }
    acks.set(id, finish)
    // 主线程长时间不确认时继续展开，宁可短时占用高一些也不能卡住整批导入。
    const timer = setTimeout(finish, deadlineMs)
  })
}

// unpackInWorker 在线程内读取整包并逐项展开：主线程只传文件句柄，不占整包字节。
async function unpackInWorker(
  id: number,
  file: File,
  metaNames: string[] | undefined,
  limits: Partial<ArchiveLimits> | undefined,
): Promise<void> {
  let buffer: ArrayBuffer
  try {
    buffer = await file.arrayBuffer()
  } catch {
    scope.postMessage({ id, type: 'error', error: '压缩包读取失败，请重新选择文件' })
    return
  }
  try {
    const bytes = new Uint8Array(buffer)
    const scan: ScanResult = scanArchive(bytes, { metaNames, limits })
    if (!scan.ok) {
      scope.postMessage({ id, type: 'error', error: scan.error })
      return
    }
    const plan = scan.plan
    const ordered = sortByNaturalName(plan.images, (entry) => entry.name)
    let actualBytes = 0
    const maxTotal = limits?.maxTotalBytes ?? DEFAULT_ARCHIVE_LIMITS.maxTotalBytes
    for (const image of ordered) {
      if (active !== id) return
      const data = expandEntry(bytes, image.name, image.size)
      if (data === null) continue
      actualBytes += data.length
      if (actualBytes > maxTotal) {
        scope.postMessage({ id, type: 'error', error: '压缩包展开后超过上限，请分批上传' })
        return
      }
      scope.postMessage({ id, type: 'image', name: image.name, data }, [data.buffer])
      await waitAck(id)
    }
    const metaBytes = plan.metaEntry !== '' ? expandEntry(bytes, plan.metaEntry, plan.metaSize) : null
    scope.postMessage({
      id,
      type: 'done',
      metaText: metaBytes === null ? '' : new TextDecoder('utf-8').decode(metaBytes),
      metaName: plan.metaName,
      skipped: plan.skipped,
      metaError: plan.metaError,
    })
  } catch {
    scope.postMessage({ id, type: 'error', error: '压缩包读取失败，请确认文件完整且没有加密' })
  }
}
