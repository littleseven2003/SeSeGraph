// 从视频里截一帧静态画面：缩略图与封面共用这一份解码逻辑，避免两处各写一套。

export interface FrameOptions {
  /** 目标最长边像素；0 表示保持原始尺寸。 */
  maxPixels?: number
  /** 截取位置占时长的比例，默认避开开头黑场。 */
  position?: number
  /** 取消信号：超时或切帖时中止等待，并清理播放器与对象 URL。 */
  signal?: AbortSignal
}

// isVideoBlob 判断媒体是否为视频，决定是否需要先取帧再缩放。
export function isVideoBlob(blob: Blob): boolean {
  return blob.type === 'video/mp4' || blob.type.startsWith('video/')
}

// captureVideoFrame 读取元信息后定位到一帧并绘成 WebP；失败时抛出明确错误由调用方回落到占位。
export async function captureVideoFrame(blob: Blob, options: FrameOptions = {}): Promise<Blob> {
  if (typeof document === 'undefined') throw new Error('当前环境无法截取视频画面')
  const maxPixels = options.maxPixels ?? 0
  const position = options.position ?? 0.1
  const signal = options.signal
  if (signal?.aborted === true) throw new Error('视频取帧已取消')
  const url = URL.createObjectURL(blob)
  const video = document.createElement('video')
  video.preload = 'auto'
  video.muted = true
  video.playsInline = true
  // wait 让每个可能长期挂起的步骤都能被取消信号打断，打断后由 finally 清理。
  const wait = async <T>(task: Promise<T>): Promise<T> => {
    if (signal === undefined) return await task
    return await new Promise<T>((resolve, reject) => {
      const onAbort = (): void => reject(new Error('视频取帧已取消'))
      const done = (): void => signal.removeEventListener('abort', onAbort)
      signal.addEventListener('abort', onAbort, { once: true })
      void task.then(
        (value) => {
          done()
          resolve(value)
        },
        (error) => {
          done()
          reject(error)
        },
      )
    })
  }
  try {
    await wait(
      new Promise<void>((resolve, reject) => {
        video.onloadedmetadata = () => resolve()
        video.onerror = () => reject(new Error('视频读取失败'))
        video.src = url
      }),
    )
    const duration = Number.isFinite(video.duration) ? video.duration : 0
    const seekTo = duration > 0 ? Math.min(duration * position, 1) : 0
    if (seekTo > 0) {
      await wait(
        new Promise<void>((resolve, reject) => {
          video.onseeked = () => resolve()
          video.onerror = () => reject(new Error('视频定位失败'))
          video.currentTime = seekTo
        }),
      )
    }
    const width = video.videoWidth
    const height = video.videoHeight
    if (width <= 0 || height <= 0) throw new Error('视频尺寸不可用')
    // 直接在画布上按目标尺寸绘制，不额外留一份原始分辨率画面。
    const scale = maxPixels > 0 ? Math.min(1, maxPixels / Math.max(width, height)) : 1
    const canvas = document.createElement('canvas')
    canvas.width = Math.max(1, Math.round(width * scale))
    canvas.height = Math.max(1, Math.round(height * scale))
    const context = canvas.getContext('2d')
    if (context === null) throw new Error('画布不可用')
    context.drawImage(video, 0, 0, canvas.width, canvas.height)
    return await new Promise<Blob>((resolve, reject) => {
      canvas.toBlob(
        (result) => (result === null ? reject(new Error('视频取帧失败')) : resolve(result)),
        'image/webp',
        0.82,
      )
    })
  } finally {
    // 取消或超时同样走这里：断开数据源、停掉加载并回收对象 URL。
    video.onloadedmetadata = null
    video.onseeked = null
    video.onerror = null
    video.removeAttribute('src')
    video.load()
    URL.revokeObjectURL(url)
  }
}
