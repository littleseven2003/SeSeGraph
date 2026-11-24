// 压缩包解压 Worker：逐项展开并立刻把结果交给主线程，主线程不会因为大包卡住。
import { unpackZipProgressive, type ArchiveImage, type ArchiveLimits } from './zip.ts'

interface ZipRequest {
  id: number
  file: File
  metaNames?: string[]
  limits?: Partial<ArchiveLimits>
}

interface ZipScope {
  postMessage: (message: unknown, transfer?: Transferable[]) => void
  onmessage: ((event: MessageEvent<ZipRequest>) => void) | null
}

const scope = self as unknown as ZipScope

scope.onmessage = (event: MessageEvent<ZipRequest>) => {
  const { id, file, metaNames, limits } = event.data
  try {
    void unpackInWorker(id, file, metaNames, limits)
  } catch {
    scope.postMessage({ id, type: 'error', error: '压缩包读取失败，请确认文件完整且没有加密' })
  }
}

// unpackInWorker 在线程内读取整包并逐项展开：主线程只传文件句柄，不占整包字节。
// 展开一项就转移一项，线程内不攒整包，峰值只有单张体积。
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
    const result = unpackZipProgressive(new Uint8Array(buffer), { metaNames, limits }, (image: ArchiveImage) => {
      const data = image.data
      scope.postMessage({ id, type: 'image', name: image.name, data }, [data.buffer])
    })
    if (!result.ok) {
      scope.postMessage({ id, type: 'error', error: result.error })
      return
    }
    scope.postMessage({
      id,
      type: 'done',
      metaText: result.metaText,
      metaName: result.metaName,
      skipped: result.skipped,
      metaError: result.metaError,
    })
  } catch {
    scope.postMessage({ id, type: 'error', error: '压缩包读取失败，请确认文件完整且没有加密' })
  }
}
