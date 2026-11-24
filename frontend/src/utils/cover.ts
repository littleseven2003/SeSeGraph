// 浏览器端生成封面缩略图：宽度 400px、WebP 质量 0.82，约 20–40KB。
import { COVER_WIDTH } from './image.ts'
import { captureVideoFrame, isVideoBlob } from './video-frame.ts'

// 封面生成结果，dataUrl 直接放进建任务请求体。
export interface CoverResult {
  dataUrl: string
  bytes: number
  width: number
  height: number
}

// 按原图比例计算缩略图尺寸，只缩小不放大。
export function coverSize(width: number, height: number): { width: number; height: number } {
  if (width <= 0 || height <= 0) {
    return { width: COVER_WIDTH, height: COVER_WIDTH }
  }
  if (width <= COVER_WIDTH) {
    return { width: Math.round(width), height: Math.round(height) }
  }
  return { width: COVER_WIDTH, height: Math.max(1, Math.round((height * COVER_WIDTH) / width)) }
}

// toDataUrl 把二进制转成可直接放进 JSON 的 data URL。
export function toDataUrl(bytes: Uint8Array, mime: string): string {
  let binary = ''
  const chunk = 0x8000
  for (let offset = 0; offset < bytes.length; offset += chunk) {
    binary += String.fromCharCode(...bytes.subarray(offset, offset + chunk))
  }
  return `data:${mime};base64,${btoa(binary)}`
}

async function loadImage(blob: Blob): Promise<{ source: CanvasImageSource; width: number; height: number }> {
  if (typeof createImageBitmap === 'function') {
    const bitmap = await createImageBitmap(blob)
    return { source: bitmap, width: bitmap.width, height: bitmap.height }
  }
  const url = URL.createObjectURL(blob)
  try {
    const image = await new Promise<HTMLImageElement>((resolve, reject) => {
      const element = new Image()
      element.onload = () => resolve(element)
      element.onerror = () => reject(new Error('图片读取失败'))
      element.src = url
    })
    return { source: image, width: image.naturalWidth, height: image.naturalHeight }
  } finally {
    URL.revokeObjectURL(url)
  }
}

function toBlob(canvas: OffscreenCanvas | HTMLCanvasElement): Promise<Blob> {
  if (canvas instanceof HTMLCanvasElement) {
    return new Promise((resolve, reject) => {
      canvas.toBlob(
        (blob) => (blob ? resolve(blob) : reject(new Error('封面生成失败'))),
        'image/webp',
        0.82,
      )
    })
  }
  return canvas.convertToBlob({ type: 'image/webp', quality: 0.82 })
}

// makeCover 缩放到目标尺寸并编码为 WebP；浏览器不支持时回退 JPEG。
export async function makeCover(blob: Blob): Promise<CoverResult> {
  const { source, width, height } = await loadImage(blob)
  const target = coverSize(width, height)
  const canvas =
    typeof OffscreenCanvas === 'function'
      ? new OffscreenCanvas(target.width, target.height)
      : Object.assign(document.createElement('canvas'), target)
  const context = canvas.getContext('2d') as
    | OffscreenCanvasRenderingContext2D
    | CanvasRenderingContext2D
    | null
  if (!context) {
    throw new Error('当前浏览器不支持封面生成')
  }
  context.drawImage(source, 0, 0, target.width, target.height)
  if ('close' in source && typeof source.close === 'function') {
    source.close()
  }

  let encoded = await toBlob(canvas)
  if (encoded.type !== 'image/webp') {
    encoded = new Blob([await encoded.arrayBuffer()], { type: encoded.type || 'image/png' })
  }
  const bytes = new Uint8Array(await encoded.arrayBuffer())
  return {
    dataUrl: toDataUrl(bytes, encoded.type || 'image/webp'),
    bytes: bytes.length,
    width: target.width,
    height: target.height,
  }
}

// makeCoverFromMedia 按媒体类型生成封面：视频取首帧，图片直接缩放。
export async function makeCoverFromMedia(blob: Blob): Promise<CoverResult> {
  if (isVideoBlob(blob)) {
    return makeCover(await captureVideoFrame(blob))
  }
  return makeCover(blob)
}
