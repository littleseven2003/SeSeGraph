// 缩略图放大气泡的定位：放在缩略图左侧，纵向钳在视口安全区内。
export interface BubbleAnchor {
  left: number
  top: number
  height: number
}

export interface BubbleMetrics {
  viewportWidth: number
  viewportHeight: number
  bubbleWidth: number
  bubbleHeight: number
  gap?: number
  margin?: number
  minViewport?: number
}

export interface BubblePosition {
  right: number
  top: number
}

// bubblePosition 返回气泡的 right/top；左侧空间不足或窄屏时返回 null，不硬挤。
export function bubblePosition(anchor: BubbleAnchor, metrics: BubbleMetrics): BubblePosition | null {
  const gap = metrics.gap ?? 12
  const margin = metrics.margin ?? 16
  const minViewport = metrics.minViewport ?? 640
  if (metrics.viewportWidth < minViewport) return null
  // 缩略图左侧放不下气泡就不弹，避免压住抽屉或超出窗口。
  if (anchor.left - gap - metrics.bubbleWidth < margin) return null
  const maxTop = metrics.viewportHeight - metrics.bubbleHeight - margin
  const top = Math.min(Math.max(anchor.top, margin), Math.max(margin, maxTop))
  return { right: metrics.viewportWidth - anchor.left + gap, top }
}
