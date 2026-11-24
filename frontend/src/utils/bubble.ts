// 缩略图放大气泡的定位：桌面贴在缩略图左侧，窄屏落到视口内居中，纵向一律钳在安全区。
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
  /** 视口窄于该宽度时不再硬挤左侧，改用视口内居中预览。 */
  compactWidth?: number
}

export type BubblePosition =
  | { side: 'left'; right: number; top: number }
  | { side: 'center'; left: number; top: number; width: number }

// bubblePosition 返回气泡的贴边方式与坐标；视口尺寸不可用时返回 null。
export function bubblePosition(anchor: BubbleAnchor, metrics: BubbleMetrics): BubblePosition | null {
  const gap = metrics.gap ?? 12
  const margin = metrics.margin ?? 16
  const compactWidth = metrics.compactWidth ?? 640
  if (metrics.viewportWidth <= 0 || metrics.viewportHeight <= 0) return null
  // 气泡最多占到安全区宽度，窄屏时按实际可用宽度收缩。
  const width = Math.min(metrics.bubbleWidth, Math.max(1, metrics.viewportWidth - margin * 2))
  const height = Math.min(metrics.bubbleHeight, Math.max(1, metrics.viewportHeight - margin * 2))
  const top = Math.min(Math.max(anchor.top, margin), Math.max(margin, metrics.viewportHeight - height - margin))
  // 左侧放得下就贴左侧，和抽屉列表保持一条视线。
  if (metrics.viewportWidth >= compactWidth && anchor.left - gap - width >= margin) {
    return { side: 'left', right: metrics.viewportWidth - anchor.left + gap, top }
  }
  // 窄屏或左侧空间不足时落到视口内居中：触控点按同样能看到预览，也不会被抽屉裁切。
  return { side: 'center', left: Math.max(margin, Math.round((metrics.viewportWidth - width) / 2)), top, width }
}
