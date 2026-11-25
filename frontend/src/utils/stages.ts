// 四阶段任务模型：#1 压缩 → #2 上传 → #3 同步 → #4 发布。
// 工作台与任务页共用同一套判定，阶段口径只在这里定义一次。
import type { TaskTone } from './tasks.ts'

/** StageKey 是四阶段的稳定标识，用于测试与渲染顺序。 */
export type StageKey = 'compress' | 'upload' | 'sync' | 'publish'

/** StageView 是单个阶段的展示状态：值、色调与进度比例。 */
export interface StageView {
  key: StageKey
  /** 阶段序号，从 1 开始，与界面上的 #1–#4 一致 */
  index: number
  label: string
  /** 右侧紧凑状态值，如「12/31」「完成」「未记录」 */
  value: string
  tone: TaskTone
  /** 0–1 的进度比例，用于进度轨宽度 */
  ratio: number
}

/** StageInput 是四阶段判定的全部输入，字段与服务端口径一一对应。 */
export interface StageInput {
  imageCount: number
  /** 提交时浏览器已处理的项数；null 表示旧任务没有这项记录 */
  compressedCount: number | null
  /** 真实已接收：上传、入库与存频道失败都算收到 */
  receivedCount: number
  /** 已存入 Telegram 频道 */
  channelCount: number
  publishState: string
  controlState: string
  status: string
}

const STAGE_LABELS: Record<StageKey, string> = {
  compress: '压缩',
  upload: '上传',
  sync: '同步',
  publish: '发布',
}

const STAGE_INDEX: Record<StageKey, number> = { compress: 1, upload: 2, sync: 3, publish: 4 }

function stage(key: StageKey, value: string, tone: TaskTone, ratio: number): StageView {
  return {
    key,
    index: STAGE_INDEX[key],
    label: STAGE_LABELS[key],
    value,
    tone,
    ratio: Math.min(1, Math.max(0, ratio)),
  }
}

// ratioOf 用已完成数除以总数，总数非法时按 0 处理。
function ratioOf(done: number, total: number): number {
  if (total <= 0) return 0
  return Math.min(1, Math.max(0, done / total))
}

// compressStage 只看浏览器上报的已处理项数：没有记录就如实显示未记录。
export function compressStage(input: Pick<StageInput, 'imageCount' | 'compressedCount'>): StageView {
  if (input.imageCount <= 0) return stage('compress', '无媒体', 'muted', 0)
  if (input.compressedCount === null) return stage('compress', '未记录', 'muted', 0)
  const done = input.compressedCount
  if (done >= input.imageCount) return stage('compress', '完成', 'ok', 1)
  if (done > 0) return stage('compress', `${done}/${input.imageCount}`, 'run', ratioOf(done, input.imageCount))
  return stage('compress', '等待', 'muted', 0)
}

// uploadStageView 与任务页口径一致：仍在上传与已入频道的图片都算接收成功。
// 暂停或停止只标在实际受影响的这一段，不把整条任务都说成停止。
export function uploadStageView(
  input: Pick<StageInput, 'imageCount' | 'receivedCount' | 'status' | 'controlState'>,
): StageView {
  if (input.imageCount <= 0) return stage('upload', '无媒体', 'muted', 0)
  const received = Math.max(0, input.receivedCount)
  if (received >= input.imageCount) return stage('upload', '完成', 'ok', 1)
  if (input.controlState === 'paused' && input.status === 'uploading') {
    return stage('upload', '已暂停', 'warn', ratioOf(received, input.imageCount))
  }
  if (input.controlState === 'stopped' && input.status === 'uploading') {
    return stage('upload', '已停止', 'danger', ratioOf(received, input.imageCount))
  }
  if (input.status === 'failed') {
    return stage('upload', `${received}/${input.imageCount}`, 'danger', ratioOf(received, input.imageCount))
  }
  if (received > 0) {
    return stage('upload', `${received}/${input.imageCount}`, 'run', ratioOf(received, input.imageCount))
  }
  return stage('upload', '等待', 'muted', 0)
}

// syncStageView 只认确实已入频道的张数，不与已接收合并。
export function syncStageView(
  input: Pick<StageInput, 'imageCount' | 'channelCount' | 'status' | 'controlState'>,
): StageView {
  if (input.imageCount <= 0) return stage('sync', '无媒体', 'muted', 0)
  const stored = Math.max(0, input.channelCount)
  if (stored >= input.imageCount) return stage('sync', '完成', 'ok', 1)
  if (input.controlState === 'paused' && input.status === 'queued') {
    return stage('sync', '已暂停', 'warn', ratioOf(stored, input.imageCount))
  }
  if (input.controlState === 'stopped' && (input.status === 'queued' || input.status === 'stopped')) {
    return stage('sync', '已停止', 'danger', ratioOf(stored, input.imageCount))
  }
  if (input.status === 'failed') {
    return stage('sync', `${stored}/${input.imageCount}`, 'danger', ratioOf(stored, input.imageCount))
  }
  if (stored > 0) return stage('sync', `${stored}/${input.imageCount}`, 'run', ratioOf(stored, input.imageCount))
  return stage('sync', '等待', 'muted', 0)
}

// publishStageView 只看 publish_state，不借用前两段的状态。
// 排队与进行中都是未知进度，不编一个固定百分比。
export function publishStageView(input: Pick<StageInput, 'publishState' | 'controlState'>): StageView {
  if (input.controlState === 'paused' && (input.publishState === 'pending' || input.publishState === 'publishing')) {
    return stage('publish', '已暂停', 'warn', 0)
  }
  if (input.controlState === 'stopped' && (input.publishState === 'pending' || input.publishState === 'publishing')) {
    return stage('publish', '已停止', 'danger', 0)
  }
  switch (input.publishState) {
    case 'published':
      return stage('publish', '已发布', 'ok', 1)
    case 'publishing':
      return stage('publish', '发布中', 'run', 0)
    case 'pending':
      return stage('publish', '排队中', 'run', 0)
    case 'failed':
      return stage('publish', '发布失败', 'danger', 0)
    default:
      return stage('publish', '未开始', 'muted', 0)
  }
}

// taskStages 按 #1–#4 的顺序返回四段。
export function taskStages(input: StageInput): StageView[] {
  return [compressStage(input), uploadStageView(input), syncStageView(input), publishStageView(input)]
}

// stageSummary 给出一行概况：正在进行的阶段与可读状态。
// 排队、发布中这类没有确定进度的状态直接给文字，不编一个百分比。
export function stageSummary(stages: readonly StageView[]): string {
  for (const item of stages) {
    if (item.tone === 'danger') return `${item.label}失败`
  }
  for (const item of stages) {
    if (item.ratio >= 1) continue
    if (item.tone === 'warn') return `${item.label}${item.value}`
    if (item.value === '等待') return `${item.label}待开始`
    if (/^\d+\/\d+$/.test(item.value)) return `${item.label} ${item.value}`
    return `${item.label}${item.value === '未开始' ? '待开始' : item.value}`
  }
  return '四阶段已完成'
}

/** StageSlot 是折叠行四槽里的一格：是否适用、已确认比例与斜纹占位的原因。 */
export interface StageSlot {
  key: StageKey
  index: number
  label: string
  /** 已确认的填充比例 0–1 */
  ratio: number
  /** true 表示这一格对本任务不适用，用斜纹占位而不是空槽 */
  inactive: boolean
  /** 斜纹格的原因，读屏与悬停都用它 */
  reason: '' | '未记录' | '不适用'
}

// stageSlots 把四阶段摊成固定四格：不适用阶段保留槽位，避免列表纵向错位。
export function stageSlots(stages: readonly StageView[], options: OverallOptions = {}): StageSlot[] {
  const includePublish = options.publishIntent === 'auto' || PUBLISH_STARTED.has(options.publishState ?? '')
  return stages.map((item) => {
    let inactive = false
    let reason: StageSlot['reason'] = ''
    if (item.key === 'compress' && options.hasCompressRecord === false) {
      inactive = true
      reason = '未记录'
    }
    if (item.key === 'publish' && !includePublish) {
      inactive = true
      reason = '不适用'
    }
    return {
      key: item.key,
      index: item.index,
      label: item.label,
      ratio: inactive ? 0 : item.ratio,
      inactive,
      reason,
    }
  })
}

/** OverallOptions 决定总进度的分母：只统计这条任务真正适用的阶段。 */
export interface OverallOptions {
  /** 提交时选择的发布意图：auto 表示同步完成后会自动发布 */
  publishIntent?: string
  /** 当前发布状态：pending 及之后表示已发起发布，发布阶段计入总进度；idle 视为未开始 */
  publishState?: string
  /** false 表示旧任务没有压缩记录，该段从分母排除 */
  hasCompressRecord?: boolean
}

// 已发起发布的取值：接口的 idle 与空串都表示还没开始，发布段不进分母。
const PUBLISH_STARTED: ReadonlySet<string> = new Set(['pending', 'publishing', 'published', 'failed'])

// stageOverall 返回整条进度（0–100 的整数百分点），供任务行的进度轨使用。
// 分母只含适用阶段：旧任务缺压缩记录时不计该段；仅存图任务不计发布段。
export function stageOverall(stages: readonly StageView[], options: OverallOptions = {}): number {
  const includeCompress = options.hasCompressRecord !== false
  const includePublish = options.publishIntent === 'auto' || PUBLISH_STARTED.has(options.publishState ?? '')
  const applicable = stages.filter((item) => {
    if (item.key === 'compress') return includeCompress
    if (item.key === 'publish') return includePublish
    return true
  })
  if (applicable.length === 0) return 0
  const total = applicable.reduce((sum, item) => sum + item.ratio, 0)
  const value = Math.floor((total / applicable.length) * 100)
  // 未达完成条件最高 99：四舍五入不能把「还差一点」画成满轨。
  if (applicable.every((item) => item.ratio >= 1)) return 100
  return Math.min(99, Math.max(0, value))
}

/** StageSource 是任务行或进度响应里四阶段需要的字段。 */
export interface StageSource {
  status: string
  publish_state: string
  control_state: string
  image_count: number
  received_count?: number
  channel_count?: number
  compressed_count?: number | null
}

// stageInputOf 把任务行或进度响应转成四阶段输入；缺字段按未记录/未接收处理。
export function stageInputOf(source: StageSource): StageInput {
  return {
    imageCount: source.image_count,
    compressedCount: source.compressed_count ?? null,
    receivedCount: source.received_count ?? 0,
    channelCount: source.channel_count ?? 0,
    publishState: source.publish_state,
    controlState: source.control_state,
    status: source.status,
  }
}
