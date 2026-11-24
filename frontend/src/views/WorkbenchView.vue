<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import AppHeader from '../components/AppHeader.vue'
import DropZone from '../components/workbench/DropZone.vue'
import ImageGrid from '../components/workbench/ImageGrid.vue'
import ModeSwitch from '../components/workbench/ModeSwitch.vue'
import MetaPanel from '../components/workbench/MetaPanel.vue'
import TaskProgressCard from '../components/tasks/TaskProgressCard.vue'
import {
  commitPost,
  createPost,
  fetchProgress,
  pauseTask,
  publishPost,
  resumeTask,
  retryPost,
  stopTask,
  uploadImage,
  uploadThumb,
  type ServerProgress,
} from '../api/posts.ts'
import { ApiError } from '../api/client.ts'
import { makeCover } from '../utils/cover.ts'
import { changeCover } from '../api/posts.ts'
import { THUMB_MAX_PIXELS, renderThumb } from '../utils/thumbs.ts'
import { LocalThumbScheduler } from '../utils/local-thumbs.ts'
import { createSerialQueue } from '../utils/serial.ts'
import { blurEnabled } from '../utils/blur.ts'
import {
  MAX_IMAGE_COUNT,
  extensionOf,
  baseName,
  formatBytes,
  isVideoName,
  limitForMode,
  mimeForName,
  type UploadMode,
} from '../utils/image.ts'
import {
  AUTO_COMPRESS_STORAGE_KEY,
  COMPRESS_TARGET_BYTES,
  COMPRESS_TARGET_DOCUMENT,
  DOCUMENT_LIMIT_BYTES,
  MAX_VIDEO_BYTES,
  PHOTO_LIMIT_BYTES,
  checkImportFile,
  compressFallback,
  compressImage,
  compressionSummary,
  evaluateMedia,
  extractVideoMeta,
  isAnimatedImage,
  keepOriginalAfterCompress,
  readAutoCompress,
  saveAutoCompress,
  selectionSummary,
  type CompressResult,
  type SelectionTally,
} from '../utils/media.ts'
import { prefillFromMeta, type PostMeta } from '../utils/meta.ts'
import { sortByNaturalName } from '../utils/natural.ts'
import { isActiveStatus, publishActive } from '../utils/progress.ts'
import { stageSummary, taskStages } from '../utils/stages.ts'
import {
  browserStorage,
  clearTask,
  loadTask,
  loadTaskFor,
  matchSnapshotFiles,
  saveTask,
  type SnapshotFile,
  type StorageLike,
  type TaskSnapshot,
} from '../utils/task-store.ts'
import { acceptedCount, initialStatuses, planUploads, type UploadStatus, runUploadPool } from '../utils/upload.ts'
import { effectiveUploadConcurrency, readUploadConcurrency } from '../utils/upload-pref.ts'
import { DEFAULT_META_FILENAMES } from '../utils/zip.ts'
import { imageBlob, unpackArchive } from '../utils/zip-client.ts'
import { fetchSettings } from '../api/settings.ts'
import {
  canLeaveResume,
  fallbackDefaults,
  loadWorkbenchInitialization,
  type WorkbenchDefaults,
} from '../utils/workbench-init.ts'
import {
  appendSelection,
  assignSortOrders,
  createId,
  discardBlocked,
  quotaUsed,
  moveItem,
  planUploadQueue,
  removeItem,
  freezeSubmitStrategy,
  selectCover,
  sortByName,
  submitBlocked,
  totalBytes,
  validatePublishInput,
  type WorkbenchTile,
} from '../utils/workbench.ts'

// 上传阶段：只有收尾后按钮才会重新可用。
import type { WorkbenchPhase } from '../utils/workbench.ts'

type Phase = WorkbenchPhase

const storage: StorageLike | null = browserStorage()

const mode = ref<UploadMode>('photo')
// 自动压缩默认开启，选择会记住；只影响草稿，续传沿用建任务时的策略。
const autoCompress = ref(readAutoCompress(storage))
// 设置页保存的说明文件名与解析方式；读取失败时沿用内置默认值。
const metaNames = ref<string[]>([...DEFAULT_META_FILENAMES])
const metaParseMode = ref<'first-line' | 'key-value'>('first-line')
// rules 是本次工作台使用的默认规则；ready 之前文件入口保持关闭。
const rules = ref<WorkbenchDefaults>(fallbackDefaults())
const ready = ref(false)
let modeTouched = false
const tiles = ref<WorkbenchTile[]>([])
const coverId = ref('')
const working = ref(false)
// 提交锁覆盖「压缩 + 建任务 + 上传」整段，阻止双击与中途换策略。
const submitting = ref(false)
const message = ref('')
const metaText = ref('')
const metaSource = ref('')
const coverPreview = ref('')
const coverDataUrl = ref('')
const coverBytes = ref(0)
// resumeUnsafe 表示当前是按任务编号打开、但本机没有该任务的可信文件清单。
// 此时只允许查看进度与控制，禁止选图、换序与补传。
const resumeUnsafe = ref(false)
// uploadAbort 结束本轮在途上传：暂停或停止任务时立即取消。
let uploadAbort: AbortController | null = null
const draft = ref<PostMeta>({ title: '', description: '', tags: [], author: '' })
const postId = ref(0)
const phase = ref<Phase>('idle')
const statuses = ref<Record<number, UploadStatus>>({})
const snapshot = ref<TaskSnapshot | null>(null)
// resumableHint 是本机存在可续传任务时的轻提示，只在空态展示，不阻断拖入。
const resumableHint = ref<TaskSnapshot | null>(null)
// compressDone 是本轮压缩已处理的项数，建任务时随请求上报，四阶段的第一段据此展示。
const compressDone = ref<number | null>(null)
const serverProgress = ref<ServerProgress | null>(null)
const retrying = ref(false)
const publishing = ref(false)
const pollIntervalMs = 2000
let pollTimer: number | undefined
// 视频预览：只在点击时创建对象 URL，关闭后立刻释放。
const previewTile = ref<WorkbenchTile | null>(null)
const previewUrl = ref('')
// 防社死模糊：封面预览与视频预览默认模糊，点按临时揭开，重新开启开关时复位。
const coverRevealed = ref(false)
const previewRevealed = ref(false)
const coverBlurred = computed(() => blurEnabled.value && !coverRevealed.value)
const previewBlurred = computed(() => blurEnabled.value && !previewRevealed.value)

// 开关重新开启的瞬间把两处临时揭开记录一起复位，避免切页后“漏光”。
watch(blurEnabled, (on) => {
  if (!on) return
  coverRevealed.value = false
  previewRevealed.value = false
})

function openPreview(id: string): void {
  const tile = tiles.value.find((item) => item.id === id)
  if (!tile || tile.kind !== 'video') return
  closePreview()
  previewTile.value = tile
  previewRevealed.value = false
  previewUrl.value = URL.createObjectURL(tile.blob)
}

function closePreview(): void {
  if (previewUrl.value !== '') URL.revokeObjectURL(previewUrl.value)
  previewUrl.value = ''
  previewTile.value = null
}

const imageCount = computed(() => tiles.value.length)
const totalSize = computed(() => totalBytes(tiles.value))
const coverTile = computed(() => tiles.value.find((tile) => tile.id === coverId.value))
const uploadedCount = computed(() => acceptedCount(statuses.value))
const busy = computed(() => discardBlocked(phase.value) || submitting.value)
const finished = computed(() => serverProgress.value?.status === 'completed')
// resuming 表示当前选中的文件与上次任务完全一致，可以直接补传。
const resuming = computed(() => {
  if (resumeUnsafe.value) return false
  if (postId.value > 0) {
    const current = snapshot.value
    if (!current) return false
    const local: SnapshotFile[] = snapshotFiles()
    return !('error' in matchSnapshotFiles(current.files, local))
  }
  const saved = snapshot.value
  if (!saved || tiles.value.length === 0) return false
  const local: SnapshotFile[] = snapshotFiles()
  return !('error' in matchSnapshotFiles(saved.files, local))
})
// 续传时沿用旧任务元数据，文件与任务不匹配时解锁，允许重新选择或放弃。
const locked = computed(
  () => resumeUnsafe.value || busy.value || (postId.value > 0 && tiles.value.length > 0 && resuming.value),
)
// 封面只跟压缩/建任务阶段互斥；上传开始后仍可更换封面。
const coverLocked = computed(() => submitting.value && phase.value !== 'uploading')
const viewingTask = computed(() => snapshot.value !== null && tiles.value.length === 0)
// receivedCount 与服务端口径一致：上传、入库与存频道失败都算服务端收到过。
const receivedCount = computed(() => {
  const progress = serverProgress.value
  if (progress === null) return uploadedCount.value
  let received = 0
  for (const image of progress.images) {
    if (image.status === 'uploaded' || image.status === 'stored' || image.status === 'failed') received += 1
  }
  return received
})

const channelCount = computed(() => {
  const progress = serverProgress.value
  if (progress !== null) return progress.stored_count
  let stored = 0
  for (const status of Object.values(statuses.value)) {
    if (status === 'stored') stored += 1
  }
  return stored
})

// stages 是四阶段唯一的数据来源：有服务端进度用它，否则用本轮的压缩与上传状态。
const stages = computed(() =>
  taskStages({
    imageCount: imageCount.value,
    // 草稿还没压缩时给 0（显示「等待」）；只有旧任务缺记录才给 null（显示「未记录」）。
    compressedCount: serverProgress.value
      ? serverProgress.value.compressed_count ?? null
      : compressDone.value ?? 0,
    receivedCount: receivedCount.value,
    channelCount: channelCount.value,
    publishState: serverProgress.value?.publish_state ?? 'idle',
    controlState: serverProgress.value?.control_state ?? 'running',
    status: serverProgress.value?.status ?? (phase.value === 'uploading' ? 'uploading' : phase.value === 'failed' ? 'failed' : 'idle'),
  }),
)

// progressText 是右栏与移动底栏的一行概况，明细只在阶段条里呈现。
const progressText = computed(() => {
  if (phase.value === 'idle' && serverProgress.value === null) return ''
  return stageSummary(stages.value)
})

function onPreviewKeydown(event: KeyboardEvent): void {
  if (event.key === 'Escape') closePreview()
}

// thumbQueue 让逐图小图串行生成：上传虽两路并行，解码仍只排一队，
// 避免同时把两张原图展开进内存，低配机器不会被解码峰值顶爆。
const thumbQueue = createSerialQueue()

// queueThumbQuietly 把一次生成排进串行队列，失败不会打断后续项。
function queueThumbQuietly(postId: number, sortOrder: number, tile: WorkbenchTile, version: string): void {
  void thumbQueue.push(() => storeThumbQuietly(postId, sortOrder, tile, version))
}

// storeThumbQuietly 在上传成功后顺手生成 512px 小图并回存；
// 任何失败都只当作缓存缺失，不影响存图与发布。
async function storeThumbQuietly(
  postId: number,
  sortOrder: number,
  tile: WorkbenchTile,
  version: string,
): Promise<void> {
  if (version === '') return
  try {
    // 复用导入时生成的小图；没有时才补一次解码。
    let thumb = tile.thumbBlob
    if (thumb === undefined) {
      const source = tile.kind === 'video' ? tile.posterBlob : tile.blob
      if (source === undefined) return
      thumb = await renderThumb(source, THUMB_MAX_PIXELS)
    }
    await uploadThumb(postId, sortOrder, version, thumb)
  } catch {
    // 留待打开抽屉时按需补齐
  }
}

onMounted(() => {
  window.addEventListener('keydown', onPreviewKeydown)
  void bootstrap()
})

// bootstrap 先把设置与续传进度读完再开放文件入口，
// 避免用户先拖入 ZIP 或先重选原图时用到还没确定的规则。
async function bootstrap(): Promise<void> {
  const requested = Number(new URLSearchParams(window.location.search).get('resume') ?? '')
  const targetID = Number.isInteger(requested) && requested > 0 ? requested : 0
  // 只有显式带 ?resume=<id> 才进入续传；默认保持干净新建态，不被上次任务阻断。
  const saved = targetID > 0 ? loadTaskFor(storage, targetID) : null
  if (saved) snapshot.value = saved
  // 本机有可续传任务时只给一条非阻断提示，不改变新建入口。
  resumableHint.value = targetID > 0 ? null : loadTask(storage)
  const init = await loadWorkbenchInitialization({
    loadSettings: fetchSettings,
    loadProgress: fetchProgress,
    snapshot: saved,
  })
  rules.value = init.defaults
  metaNames.value = init.defaults.metaNames
  metaParseMode.value = init.defaults.parseMode
  if (init.resumeMode !== null) {
    mode.value = init.resumeMode
    modeTouched = true
  } else if (!modeTouched) {
    mode.value = init.defaults.defaultMode
  }
  if (saved && init.progress !== null) {
    hydrate(saved, init.progress)
  } else if (saved) {
    // 续传任务已不存在（例如数据目录被清空）时丢弃快照。
    clearTask(storage, saved.postId)
    snapshot.value = null
    resetToDefaultMode()
  }

  // 从任务列表直达但本机没有该任务清单：只查看进度与控制，禁止补传。
  if (saved === null && targetID > 0) {
    try {
      const progress = await fetchProgress(targetID)
      postId.value = progress.id
      serverProgress.value = progress
      applyServerProgress(progress)
      phase.value = progress.status === 'uploading' ? 'idle' : progress.status === 'failed' ? 'failed' : 'queued'
      resumeUnsafe.value = true
      message.value = `任务 #${progress.id} 已载入。此设备没有该任务的文件清单，只能查看进度和控制；请在原设备续传，或放弃该任务后重新建任务。`
    } catch {
      message.value = '未能读取该任务，请回任务列表重试'
    }
  }
  ready.value = true
}

// resetToDefaultMode 回到本次设置的默认画质，用于退出续传或重新开始。
function resetToDefaultMode(): void {
  mode.value = rules.value.defaultMode
  modeTouched = false
}

// hydrate 用已读取的服务端进度恢复界面状态，避免重复请求。
function hydrate(saved: TaskSnapshot, progress: ServerProgress): void {
  postId.value = saved.postId
  // 开放导入前恢复该任务的压缩策略；画质模式以服务端记录为准。
  if (typeof saved.autoCompress === 'boolean') autoCompress.value = saved.autoCompress
  if (progress.upload_mode === 'photo' || progress.upload_mode === 'document') mode.value = progress.upload_mode
  applyServerProgress(progress)
  phase.value = progress.status === 'uploading' ? 'idle' : progress.status === 'failed' ? 'failed' : 'queued'
  if (isActiveStatus(progress.status)) {
    startPolling(saved.postId)
  }
  if (progress.status === 'completed') {
    message.value = `任务 #${saved.postId} 已存入频道`
  }
}

function pushError(text: string): void {
  message.value = text
}

// 附加字段用于视频与压缩结果，缺省时按普通图片处理。
interface TileExtra {
  kind?: 'image' | 'video'
  duration?: number
  sourceSize?: number
  compressedFrom?: number
  mime?: string
  posterBlob?: Blob
  sourceBlob?: Blob
  sourceMime?: string
  animated?: boolean
  animatedKind?: 'gif' | 'other'
}

function addTile(name: string, blob: Blob, size: number, extra: TileExtra = {}): WorkbenchTile {
  return {
    id: createId(),
    name,
    size,
    mime: extra.mime ?? mimeForName(name),
    blob,
    sortOrder: 0,
    previewUrl: '',
    thumbUrl: '',
    kind: extra.kind,
    duration: extra.duration,
    sourceSize: extra.sourceSize,
    compressedFrom: extra.compressedFrom,
    posterBlob: extra.posterBlob,
    sourceBlob: extra.sourceBlob,
    sourceMime: extra.sourceMime,
    animated: extra.animated,
    animatedKind: extra.animatedKind,
  }
}

// prepareTiles 只做轻量识别：读视频首帧、标记动图，压缩留到提交前逐图执行。
async function prepareTiles(
  entries: { name: string; blob: Blob }[],
  problems: string[] = [],
): Promise<WorkbenchTile[]> {
  const prepared: WorkbenchTile[] = []
  for (const entry of entries) {
    const label = baseName(entry.name)
    if (isVideoName(entry.name)) {
      const meta = await extractVideoMeta(entry.blob).catch(() => null)
      if (meta === null) problems.push(`${label} 首帧读取失败，已按占位加入`)
      prepared.push(
        addTile(label, entry.blob, entry.blob.size, {
          kind: 'video',
          duration: meta?.duration ?? 0,
          sourceSize: entry.blob.size,
          sourceBlob: entry.blob,
          sourceMime: 'video/mp4',
          posterBlob: meta?.poster ?? undefined,
        }),
      )
      continue
    }
    const animated = await isAnimatedImage(entry.blob, entry.name)
    prepared.push(
      addTile(label, entry.blob, entry.blob.size, {
        sourceSize: entry.blob.size,
        sourceBlob: entry.blob,
        sourceMime: mimeForName(entry.name) || entry.blob.type,
        animated,
        animatedKind: extensionOf(entry.name) === 'gif' ? 'gif' : 'other',
      }),
    )
  }
  return prepared
}

// resetToSource 回到原始文件：切换模式或开关后重新判定，不留不可逆降质。
function resetToSource(): void {
  tiles.value = tiles.value.map((tile) => ({
    ...tile,
    blob: tile.sourceBlob ?? tile.blob,
    size: tile.sourceSize ?? tile.size,
    mime: tile.sourceMime ?? tile.mime,
    compressedFrom: undefined,
    compressFailed: undefined,
    keptOriginal: undefined,
  }))
}

// restoreSource 用原图覆盖单张卡片：压缩无收益或失败但原图合规时保留原图。
function restoreSource(tile: WorkbenchTile): WorkbenchTile {
  return {
    ...tile,
    blob: tile.sourceBlob ?? tile.blob,
    size: tile.sourceSize ?? tile.size,
    mime: tile.sourceMime ?? tile.mime,
    compressedFrom: undefined,
    compressFailed: undefined,
    keptOriginal: true,
  }
}

// compressForSubmit 提交前逐图压缩：返回阻塞原因与本轮压缩摘要。
async function compressForSubmit(
  frozenMode: UploadMode,
  frozenCompress: boolean,
): Promise<{ problem: string; summary: string }> {
  const limit = frozenMode === 'document' ? DOCUMENT_LIMIT_BYTES : PHOTO_LIMIT_BYTES
  const pending: WorkbenchTile[] = []
  const blocked: string[] = []
  for (const tile of tiles.value) {
    const verdict = evaluateMedia(
      {
        kind: tile.kind ?? 'image',
        size: tile.sourceSize ?? tile.size,
        compressed: false,
        animated: tile.animated,
        animatedKind: tile.animatedKind,
        failed: tile.compressFailed,
        keptOriginal: tile.keptOriginal,
      },
      frozenMode,
      frozenCompress,
    )
    if (verdict.state === 'blocked') blocked.push(`${tile.name} ${verdict.label}`)
    else if (verdict.state === 'pending') pending.push(tile)
  }
  if (blocked.length > 0) {
    // 定位第一张被拦的卡片，光有汇总数字不够。
    const first = tiles.value.find((tile) => {
      const verdict = evaluateMedia(
        { kind: tile.kind ?? 'image', size: tile.sourceSize ?? tile.size, compressed: false, animated: tile.animated, animatedKind: tile.animatedKind, failed: tile.compressFailed },
        mode.value,
        autoCompress.value,
      )
      return verdict.state === 'blocked'
    })
    if (first) {
      document.getElementById(`tile-${first.id}`)?.scrollIntoView({ block: 'center', behavior: 'smooth' })
    }
    return {
      problem: `有 ${blocked.length} 项超出当前模式上限且无法自动压缩，请调整模式、开启压缩或移除后再提交（已定位：${blocked[0]}）`,
      summary: '',
    }
  }
  if (pending.length === 0) return { problem: '', summary: '' }
  const updated = tiles.value.slice()
  const results: CompressResult[] = []
  const failures: string[] = []
  let kept = 0
  working.value = true
  try {
    for (const [index, tile] of pending.entries()) {
      message.value = `正在压缩（${index + 1} / ${pending.length}）：${tile.name} (${formatBytes(tile.sourceSize ?? tile.size)})…`
      const position = updated.findIndex((item) => item.id === tile.id)
      try {
        const target = frozenMode === 'document' ? COMPRESS_TARGET_DOCUMENT : COMPRESS_TARGET_BYTES
        const result = await compressImage(tile.sourceBlob ?? tile.blob, target)
        if (position >= 0) {
          if (keepOriginalAfterCompress(tile.sourceSize ?? tile.size, result.toSize, limit)) {
            // 压缩没有收益且原图合规：保留原图，角标不再停在待压缩。
            kept += 1
            updated[position] = restoreSource(tile)
          } else {
            updated[position] = {
              ...tile,
              compressFailed: undefined,
              keptOriginal: undefined,
              blob: result.blob,
              size: result.toSize,
              mime: result.blob.type || 'image/webp',
              compressedFrom: result.fromSize,
            }
            results.push(result)
          }
        }
      } catch (error) {
        if (position >= 0) {
          if (compressFallback(tile.sourceSize ?? tile.size, limit) === 'keep') {
            // 编码失败但原图合规：允许按原图提交，避免整批卡住。
            kept += 1
            updated[position] = restoreSource(tile)
          } else {
            updated[position] = { ...updated[position], compressFailed: true }
            const first = updated[position]
            document.getElementById(`tile-${first.id}`)?.scrollIntoView({ block: 'center', behavior: 'smooth' })
            failures.push(`${tile.name} 压缩失败：${error instanceof Error ? error.message : '请重试'}`)
          }
        }
      }
    }
    tiles.value = updated
  } finally {
    working.value = false
  }
  if (failures.length > 0) {
    return { problem: `${failures.join('；')}（需压到上限内才能提交，已定位）`, summary: '' }
  }
  return { problem: '', summary: compressionSummary(results, kept) }
}

// snapshotFiles 按原文件名与原始体积生成续传快照，压缩不改变匹配依据。
function snapshotFiles(): SnapshotFile[] {
  return tiles.value.map((tile) => ({ name: tile.name, size: tile.sourceSize ?? tile.size }))
}

function releaseTiles(items: WorkbenchTile[]): void {
  for (const item of items) {
    if (item.previewUrl !== '') URL.revokeObjectURL(item.previewUrl)
    if (item.thumbUrl !== '') URL.revokeObjectURL(item.thumbUrl)
    item.thumbBlob = undefined
  }
}

// 重建封面缩略图：宽度 400px 的 WebP，同时用于列表展示与建任务请求体。
async function refreshCover(): Promise<void> {
  const tile = coverTile.value
  if (!tile) {
    coverPreview.value = ''
    coverDataUrl.value = ''
    coverBytes.value = 0
    return
  }
  try {
    const cover = await makeCover(tile.posterBlob ?? tile.blob)
    coverPreview.value = cover.dataUrl
    coverDataUrl.value = cover.dataUrl
    coverBytes.value = cover.bytes
  } catch {
    coverPreview.value = ''
    coverDataUrl.value = ''
    coverBytes.value = 0
    pushError('封面生成失败，可换一张图片试试')
  }
}

function applyTiles(next: WorkbenchTile[]): void {
  tiles.value = assignSortOrders(next)
  coverId.value = selectCover(tiles.value, coverId.value)
  // 导入后立刻排队生成网格小图：整个工作台同一时刻只解码一项。
  for (const tile of tiles.value) queueLocalThumb(tile)
}

// localThumbs 按稳定 ID 去重并校验回写：重排、删除、清空与离页都有确定的回收口径。
// 生成前与回写前各确认一次该项仍在列表里，迟到的结果不会挂回网格。
const localThumbs = new LocalThumbScheduler(thumbQueue, {
  isAlive: (id) => tiles.value.some((tile) => tile.id === id),
  render: async (id) => {
    const tile = tiles.value.find((item) => item.id === id)
    if (tile === undefined) throw new Error('该项已不在列表中')
    const source = tile.kind === 'video' ? (tile.posterBlob ?? tile.blob) : tile.blob
    return renderThumb(source, THUMB_MAX_PIXELS)
  },
  attach: (id, blob) => {
    const tile = tiles.value.find((item) => item.id === id)
    if (tile === undefined) return
    tile.thumbBlob = blob
    tile.thumbUrl = URL.createObjectURL(blob)
  },
})

// queueLocalThumb 生成网格用的 512px 小图；网格只挂这份小图，绝不挂原图 Blob。
// 失败保留静默占位，等抽屉按需补齐或重试。
function queueLocalThumb(tile: WorkbenchTile): void {
  if (tile.thumbUrl !== '' || tile.thumbBlob !== undefined) return
  localThumbs.request(tile.id)
}

// restoreOrder 用名称与体积把本地文件放回上次任务的顺序；失败时给出提示。
function restoreOrder(): boolean {
  const saved = snapshot.value
  if (!saved) return true
  const local: SnapshotFile[] = snapshotFiles()
  const matched = matchSnapshotFiles(saved.files, local)
  if ('error' in matched) {
    pushError(`${matched.error}。如需放弃上次任务 #${saved.postId}，点「放弃此任务」`)
    return false
  }
  const byIndex = new Map(tiles.value.map((tile, index) => [index, tile]))
  const ordered = matched.order
    .map((index) => byIndex.get(index))
    .filter((tile): tile is WorkbenchTile => tile !== undefined)
  applyTiles(ordered)
  statuses.value = {}
  message.value = `已按上次任务 #${saved.postId} 的顺序恢复 ${ordered.length} 张，发布时会补传缺少的序号`
  return true
}

// 切换画质模式前先确认现有图片在新上限内，避免静默失效。
async function setMode(next: UploadMode): Promise<void> {
  if (next === mode.value || locked.value) return
  mode.value = next
  modeTouched = true
  // 模式只影响判定与提交时的压缩目标，草稿可自由切换并回到原图重新评估。
  resetToSource()
  announceStatus()
  void refreshCover()
}

function onAutoCompress(enabled: boolean): void {
  if (locked.value) return
  autoCompress.value = enabled
  saveAutoCompress(storage, enabled)
  resetToSource()
  announceStatus()
}

// announceStatus 用一条状态播报当前不满足与待压缩数量，颜色之外还有文字。
function announceStatus(): void {
  let blocked = 0
  let pending = 0
  for (const tile of tiles.value) {
    const verdict = evaluateMedia(
      {
        kind: tile.kind ?? 'image',
        size: tile.sourceSize ?? tile.size,
        compressed: tile.compressedFrom !== undefined,
        animated: tile.animated,
        animatedKind: tile.animatedKind,
        failed: tile.compressFailed,
        keptOriginal: tile.keptOriginal,
      },
      mode.value,
      autoCompress.value,
    )
    if (verdict.state === 'blocked') blocked += 1
    if (verdict.state === 'pending') pending += 1
  }
  if (blocked > 0) {
    message.value = `有 ${blocked} 项不满足当前模式上限，请开启自动压缩、切换模式或移除后再提交`
  } else if (pending > 0) {
    message.value = `有 ${pending} 项超过自动压缩触发值，提交时会尝试压缩`
  } else if (tiles.value.length > 0) {
    message.value = `全部 ${tiles.value.length} 项满足当前要求`
  }
}

async function onZip(file: File): Promise<void> {
  working.value = true
  message.value = '正在解压压缩包…'
  try {
    // 解压放到 Worker 里逐项展开：大包不会占住主线程，内存峰值只有单张体积。
    const result = await unpackArchive(file, {
      metaNames: metaNames.value,
      // photo 模式下允许更大的静态图进入压缩流程；视频始终按 20,000,000 字节。
      limits: {
        maxImageBytes: 50000000,
      },
    })
    message.value = ''
    if (!result.ok) {
      pushError(result.error)
      return
    }
    if (result.images.length > MAX_IMAGE_COUNT) {
      pushError(`单个帖子最多 ${MAX_IMAGE_COUNT} 张图片，请先精简压缩包`)
      return
    }
    releaseTiles(tiles.value)
    // 压缩包内同样统一预处理：视频读首帧、photo 模式超大静态图先压缩。
    const problems: string[] = []
    const next = await prepareTiles(
      result.images.map((image) => ({
        name: image.name,
        blob: imageBlob(image, mimeForName(image.name) || 'application/octet-stream'),
      })),
      problems,
    )
    if (problems.length > 0) pushError(problems.join('；'))
    metaText.value = result.metaText
    metaSource.value = result.metaText === '' ? '' : result.metaName
    applyTiles(next)
    restoreOrder()
    if (result.metaText !== '') {
      // 包内存在说明文件时自动预填，解析方式与手动重读一致，缺失字段仍可手动补充。
      draft.value = prefillFromMeta(result.metaText, metaParseMode.value)
    }
    if (result.metaError !== '') {
      message.value = result.metaError
    } else if (result.skipped > 0 && message.value === '') {
      message.value = `已忽略 ${result.skipped} 个非媒体文件`
    }
    void refreshCover()
  } catch {
    pushError('压缩包读取失败，请确认文件完整且没有加密')
  } finally {
    working.value = false
  }
}

async function onImages(files: File[], unread: string[] = []): Promise<void> {
  message.value = ''
  // 续传重选会整体替换旧选择，配额按新批次计算，避免旧列表占满 500 张上限。
  const replace = snapshot.value !== null && !resuming.value
  const base = quotaUsed(tiles.value.length, replace)
  const entries: { name: string; blob: Blob }[] = []
  const tally: SelectionTally = { read: files.length + unread.length, added: 0, unread, skipped: [] }
  const skip = (label: string, name: string): void => {
    const group = tally.skipped.find((item) => item.label === label)
    if (group) group.names.push(name)
    else tally.skipped.push({ label, names: [name] })
  }
  working.value = true
  try {
    for (const file of files) {
      if (base + entries.length >= MAX_IMAGE_COUNT) {
        skip('超出 500 项上限', baseName(file.name))
        continue
      }
      const problem = checkImportFile({ name: file.name, size: file.size })
      if (problem) {
        const label = isVideoName(file.name)
          ? file.size > MAX_VIDEO_BYTES
            ? '超过 20MB'
            : '格式不支持'
          : file.size >= 50000000
            ? '超过 50MB'
            : '格式不支持'
        skip(label, baseName(file.name))
        continue
      }
      entries.push({ name: baseName(file.name), blob: file })
    }
    if (entries.length === 0) {
      pushError(selectionSummary(tally))
      return
    }
    const problems: string[] = []
    const accepted = await prepareTiles(entries, problems)
    tally.added = accepted.length
    if (accepted.length === 0) {
      pushError([selectionSummary(tally), ...problems].join('；'))
      return
    }
    // 文件夹与多图入口默认按文件名自然排序；续传时失配的旧选择整体替换掉。
    const sorted = sortByNaturalName(accepted, (tile) => tile.name)
    if (replace) {
      releaseTiles(tiles.value)
      coverId.value = ''
    }
    applyTiles(appendSelection(tiles.value, sorted, replace))
    restoreOrder()
    void refreshCover()
    const summary = selectionSummary(tally)
    const detail = [summary, ...problems].filter((text) => text !== '')
    if (detail.length > 0) message.value = detail.join('；')
  } finally {
    working.value = false
  }
}

function onMove(payload: { from: number; to: number }): void {
  if (locked.value) return
  applyTiles(moveItem(tiles.value, payload.from, payload.to))
  statuses.value = {}
}

function onRemove(id: string): void {
  if (locked.value) return
  const removed = tiles.value.find((tile) => tile.id === id)
  statuses.value = {}
  applyTiles(removeItem(tiles.value, id))
  if (removed) {
    if (removed.previewUrl !== '') URL.revokeObjectURL(removed.previewUrl)
    if (removed.thumbUrl !== '') URL.revokeObjectURL(removed.thumbUrl)
  }
  if (tiles.value.length === 0) {
    coverPreview.value = ''
    coverDataUrl.value = ''
    coverBytes.value = 0
  } else {
    void refreshCover()
  }
}

async function refreshServerProgress(): Promise<void> {
  if (postId.value === 0) return
  try {
    applyServerProgress(await fetchProgress(postId.value))
  } catch {
    // 刷新失败保留当前展示，下一次轮询会再试。
  }
}

const controlBusy = ref(false)

async function runControl(action: 'pause' | 'resume' | 'stop'): Promise<void> {
  if (postId.value === 0 || controlBusy.value) return
  controlBusy.value = true
  try {
    // 主动暂停或停止时先结束在途上传；服务端仍会复核轮次。
    if (action !== 'resume' && uploadAbort !== null) uploadAbort.abort()
    if (action === 'pause') await pauseTask(postId.value)
    else if (action === 'resume') await resumeTask(postId.value)
    else await stopTask(postId.value)
    await refreshServerProgress()
    if (action === 'stop') {
      phase.value = 'idle'
      message.value = '任务已停止，未完成的项不再上传'
    } else if (action === 'pause') {
      message.value = '任务已暂停，继续时只补未完成的项'
    } else {
      message.value = '任务已继续，可点「继续上传」补齐缺项'
    }
  } catch (error) {
    pushError(error instanceof ApiError ? error.message : '任务操作失败，请稍后重试')
  } finally {
    controlBusy.value = false
  }
}

function onSelectCover(id: string): void {
  coverId.value = id
  void refreshCover()
  // 任务已创建时同步把新封面写给服务端；提交压缩期间先不改，避免与提交冲突。
  if (postId.value === 0 || coverLocked.value) return
  const tile = tiles.value.find((item) => item.id === id)
  if (tile === undefined) return
  void (async () => {
    try {
      const cover = await makeCover(tile.posterBlob ?? tile.blob)
      await changeCover(postId.value, tile.sortOrder, cover.dataUrl)
      if (serverProgress.value !== null) {
        serverProgress.value = { ...serverProgress.value, cover_sort_order: tile.sortOrder }
      }
      message.value = '封面已更新'
    } catch (error) {
      // 失败时回到服务端实际封面，不让界面显示一份没生效的选择。
      const actual = serverProgress.value?.cover_sort_order ?? 0
      const fallback = tiles.value.find((item) => item.sortOrder === actual)
      coverId.value = fallback?.id ?? ''
      void refreshCover()
      pushError(error instanceof ApiError ? error.message : '更换封面失败，请稍后重试')
    }
  })()
}

function onSortByName(): void {
  if (locked.value) return
  statuses.value = {}
  applyTiles(sortByName(tiles.value))
  void refreshCover()
}

function stopPolling(): void {
  if (pollTimer !== undefined) {
    window.clearInterval(pollTimer)
    pollTimer = undefined
  }
}

// 用服务端状态刷新图格标记，已存入频道与已传至服务器都视为完成。
function applyServerProgress(progress: ServerProgress): void {
  serverProgress.value = progress
  const next: Record<number, UploadStatus> = { ...statuses.value }
  for (const image of progress.images) {
    if (image.status === 'stored') {
      next[image.sort_order] = 'stored'
    } else if (image.status === 'uploaded') {
      next[image.sort_order] = 'uploaded'
    } else if (image.status === 'failed') {
      next[image.sort_order] = 'failed'
    } else {
      next[image.sort_order] = 'pending'
    }
  }
  statuses.value = next
}

async function refreshProgress(id: number): Promise<void> {
  try {
    const progress = await fetchProgress(id)
    applyServerProgress(progress)
    // 存图完成后仍可能处于发布中，发布任务结束前继续轮询。
    if (!isActiveStatus(progress.status) && !publishActive(progress)) {
      stopPolling()
      if (progress.publish_state === 'published') {
        message.value = '已发布到 Telegraph，分享链接见下方'
      } else if (progress.publish_state === 'failed') {
        message.value = 'Telegraph 发布失败，可重新发布'
      } else if (progress.status === 'completed') {
        message.value = '已存入频道'
      } else if (progress.status === 'failed') {
        message.value = '部分图片存图失败，可以点击重试'
      }
    }
  } catch {
    // 轮询失败时保持上次状态，下一轮继续
  }
}

// 轮询服务端进度，任务活跃期间每 2 秒刷新一次。
function startPolling(id: number): void {
  stopPolling()
  void refreshProgress(id)
  pollTimer = window.setInterval(() => {
    void refreshProgress(id)
  }, pollIntervalMs)
}

// 管理员确认发布：开启图片访问并进入 Telegraph 发布队列。
async function publishToTelegraph(): Promise<void> {
  if (publishing.value || postId.value === 0) return
  publishing.value = true
  message.value = ''
  try {
    await publishPost(postId.value)
    message.value = '已提交发布，正在生成 Telegraph 页面'
    startPolling(postId.value)
  } catch (error) {
    pushError(error instanceof ApiError ? error.message : '发布失败，请稍后重试')
  } finally {
    publishing.value = false
  }
}

// 手动重试失败的图片，随后恢复轮询。
async function retryFailed(): Promise<void> {
  const target = postId.value
  if (retrying.value || target === 0) return
  retrying.value = true
  message.value = ''
  try {
    const result = await retryPost(target)
    phase.value = result.status === 'queued' ? 'queued' : 'failed'
    startPolling(target)
  } catch (error) {
    pushError(error instanceof ApiError ? error.message : '重试失败，请稍后重试')
  } finally {
    retrying.value = false
  }
}

function resetProgress(): void {
  postId.value = 0
  phase.value = 'idle'
  statuses.value = {}
  serverProgress.value = null
  message.value = ''
  stopPolling()
  clearTask(storage, snapshot.value?.postId)
}

// discardSnapshot 放弃上次任务的续传入口，允许直接开新任务。
function discardSnapshot(): void {
  // 压缩/上传进行中不能清任务，否则会丢掉正在使用的策略与快照。
  if (submitBlocked({ busy: busy.value, submitting: submitting.value })) return
  if (!canLeaveResume(ready.value, phase.value)) return
  const abandoned = snapshot.value?.postId ?? postId.value
  snapshot.value = null
  clearTask(storage, abandoned)
  stopPolling()
  postId.value = 0
  phase.value = 'idle'
  statuses.value = {}
  serverProgress.value = null
  resetToDefaultMode()
  message.value =
    tiles.value.length === 0
      ? '已放弃上次任务，可以重新选择图片'
      : `已放弃任务 #${abandoned}，发布会创建新的任务`
}

function clearAll(): void {
  if (!ready.value) return
  // 换一批文件：作废在途生成，迟到的结果不再挂回空网格。
  localThumbs.reset()
  releaseTiles(tiles.value)
  tiles.value = []
  coverId.value = ''
  coverPreview.value = ''
  coverDataUrl.value = ''
  coverBytes.value = 0
  metaText.value = ''
  metaSource.value = ''
  draft.value = { title: '', description: '', tags: [], author: '' }
  snapshot.value = null
  compressDone.value = null
  resetProgress()
  resetToDefaultMode()
}

// 建任务：已存在任务或上次未完成任务时复用，避免重复建帖。
async function ensureTask(): Promise<number | null> {
  if (postId.value > 0) {
    const current = snapshot.value
    if (current) {
      const matched = matchSnapshotFiles(current.files, snapshotFiles())
      if ('error' in matched) {
        pushError(`${matched.error}。如需放弃上次任务，点「放弃此任务」`)
        return null
      }
    }
    return postId.value
  }
  const saved = snapshot.value
  if (saved) {
    const matched = matchSnapshotFiles(saved.files, snapshotFiles())
    if (!('error' in matched)) {
      // 恢复已建任务的画质与压缩策略，避免续传混用新设置。
      postId.value = saved.postId
      if (saved.mode) mode.value = saved.mode
      if (typeof saved.autoCompress === 'boolean') autoCompress.value = saved.autoCompress
      return postId.value
    }
  }
  const created = await createPost({
    title: draft.value.title.trim(),
    description: draft.value.description,
    author_name: draft.value.author.trim(),
    upload_mode: mode.value,
    image_count: tiles.value.length,
    cover_thumbnail: coverDataUrl.value,
    cover_sort_order: coverTile.value?.sortOrder ?? 0,
    // 压缩在浏览器侧一次性完成，建任务时把已处理项数一并上报。
    compressed_count: compressDone.value ?? undefined,
    tags: draft.value.tags,
  })
  postId.value = created.id
  // 快照按原始体积生成，后续压缩不改变续传身份；同时固化模式与压缩策略。
  const files = snapshotFiles()
  const next: TaskSnapshot = {
    postId: created.id,
    imageCount: files.length,
    files,
    createdAt: Date.now(),
    mode: mode.value,
    autoCompress: autoCompress.value,
  }
  snapshot.value = next
  saveTask(storage, next)
  return postId.value
}

// 发布：建任务 → 按服务端进度补传 → 提交入队，快照保留以便刷新后查看与重试。
async function publish(): Promise<void> {
  // 提交锁覆盖压缩与上传整段：双击、换模式、排序都不能插进来。
  // 压缩/上传进行中、正在追加文件或解包 ZIP 时都不接受新的提交。
  if (submitBlocked({ busy: busy.value, submitting: submitting.value }) || working.value) return
  message.value = ''
  if (tiles.value.length === 0) {
    pushError('请先选择图片或视频')
    return
  }
  if (snapshot.value && !resuming.value) {
    // 选错文件时先说明身份不符，避免被标题校验挡住真正的提示。
    pushError(`当前选择与上次任务 #${snapshot.value.postId} 不一致，请重新选择同一批文件，或放弃此任务后重新开始`)
    return
  }
  const invalid = validatePublishInput({
    title: draft.value.title,
    coverReady: coverDataUrl.value !== '',
    resuming: resuming.value,
  })
  if (invalid !== null) {
    pushError(invalid)
    return
  }

  if (postId.value > 0 && snapshot.value === null) {
    pushError('当前任务没有可信的本地文件清单，无法安全补传；请在原设备续传')
    return
  }
  if (serverProgress.value !== null && serverProgress.value.control_state !== 'running') {
    pushError(serverProgress.value.control_state === 'stopped' ? '任务已停止，不能再继续上传' : '任务已暂停，请先继续任务')
    return
  }

  submitting.value = true
  try {
    const frozen = freezeSubmitStrategy(mode.value, autoCompress.value)
    const compression = await compressForSubmit(frozen.mode, frozen.autoCompress)
    if (compression.problem !== '') {
      pushError(compression.problem)
      return
    }
    if (compression.summary !== '') {
      message.value = compression.summary
    }
    compressDone.value = tiles.value.length

    phase.value = 'creating'
    await runUpload()
  } finally {
    submitting.value = false
  }
}

// runUpload 建任务并按服务端进度补传；由 publish 在提交锁内调用。
async function runUpload(): Promise<void> {
  try {
    const id = await ensureTask()
    if (id === null) {
      // 文件与任务不匹配：恢复按钮状态，保留重新选择与放弃入口。
      phase.value = 'idle'
      return
    }

    const progress = await fetchProgress(id)
    statuses.value = initialStatuses(progress)
    if (progress.status !== 'uploading') {
      // 任务已进入队列或已结束，交给服务端继续处理，这里只展示进度。
      phase.value = progress.status === 'failed' ? 'failed' : 'queued'
      applyServerProgress(progress)
      startPolling(id)
      return
    }

    const planned = planUploads(progress, tiles.value.length)
    if ('error' in planned) {
      phase.value = 'idle'
      pushError(planned.error)
      return
    }

    // 上传一开始就展示服务端进度，暂停与停止入口在首轮传输中即可用。
    applyServerProgress(progress)
    phase.value = 'uploading'
    // 用开始上传时的列表生成队列，中途任何列表变化都不会影响已排定的请求。
    const queue = planUploadQueue(tiles.value.slice(), planned.plan.pending)
    // 受控并发：首错停止派发，已在途的请求收尾，全部成功才提交入队。
    const round = progress.upload_round
    uploadAbort = new AbortController()
    // 每轮开始时读取一次「同时上传数」，设置页调整后下一轮生效；
    // 再按服务端声明的上限收敛，避免多占一个必然排队的名额。
    const concurrency = effectiveUploadConcurrency(
      readUploadConcurrency(window.localStorage),
      rules.value.uploadConcurrency,
    )
    const failure = await runUploadPool(queue, concurrency, async (item) => {
      statuses.value = { ...statuses.value, [item.sortOrder]: 'uploading' }
      try {
        const uploaded = await uploadImage(id, item.sortOrder, item.tile.blob, item.tile.mime, round, uploadAbort?.signal)
        statuses.value = { ...statuses.value, [item.sortOrder]: 'uploaded' }
        // 小图失败不影响存图与发布：服务端缺这份时抽屉会按需补。生成与回存走串行队列。
        queueThumbQuietly(id, item.sortOrder, item.tile, uploaded.media_version)
      } catch (error) {
        statuses.value = { ...statuses.value, [item.sortOrder]: 'failed' }
        throw error
      }
    })
    if (failure !== null) {
      const item = queue[failure.index]
      if (uploadAbort?.signal.aborted === true) {
        // 用户主动暂停或停止：清掉本轮控制器，按服务端状态恢复全部图片项。
        uploadAbort = null
        await refreshServerProgress()
        const restored: Record<number, UploadStatus> = {}
        for (const tile of tiles.value) restored[tile.sortOrder] = 'pending'
        for (const image of serverProgress.value?.images ?? []) {
          if (image.status === 'uploaded') restored[image.sort_order] = 'uploaded'
          else if (image.status === 'stored') restored[image.sort_order] = 'stored'
        }
        statuses.value = restored
        phase.value = 'idle'
        return
      }
      phase.value = 'failed'
      const reason = failure.error instanceof ApiError ? failure.error.message : '网络中断或超时'
      pushError(`第 ${failure.index + 1}/${queue.length} 项 ${item.tile.name} 上传失败：${reason}；已停止本轮，点「继续上传」只补未完成的项`)
      return
    }

    const committed = await commitPost(id)
    phase.value = committed.status === 'queued' ? 'queued' : 'failed'
    if (committed.missing && committed.missing.length > 0) {
      pushError('还有图片未上传完整，请继续上传')
      phase.value = 'idle'
      return
    }
    snapshot.value = loadTask(storage)
    if (snapshot.value) {
      snapshot.value.mode = mode.value
      snapshot.value.autoCompress = autoCompress.value
      saveTask(storage, snapshot.value)
    }
    startPolling(id)
  } catch (error) {
    phase.value = 'idle'
    pushError(error instanceof ApiError ? error.message : '发布失败，请稍后重试')
  }
}

onBeforeUnmount(() => {
  window.removeEventListener('keydown', onPreviewKeydown)
  stopPolling()
  closePreview()
  localThumbs.dispose()
  releaseTiles(tiles.value)
})
</script>

<template>
  <div class="min-h-full">
    <AppHeader />

    <main class="mx-auto w-full max-w-[1240px] px-5 py-6">
      <div class="mb-3 flex items-center justify-end gap-2">
        <span class="tnum text-xs text-muted">{{ imageCount }} 项 · {{ formatBytes(totalSize) }}</span>
        <button
          v-if="tiles.length > 0 && !locked"
          type="button"
          class="h-11 rounded-md border border-line px-3 text-sm font-medium transition hover:border-ink/25 hover:bg-canvas"
          @click="clearAll"
        >
          清空
        </button>
      </div>

      <div
        v-if="resumableHint && tiles.length === 0 && snapshot === null && postId === 0"
        class="mb-4 flex flex-wrap items-center justify-between gap-2 rounded-xl border border-line bg-surface px-3.5 py-2.5 text-sm"
      >
        <span class="min-w-0 truncate text-muted">
          有未完成任务 <span class="tnum font-medium text-ink">#{{ resumableHint.postId }}</span>（{{ resumableHint.files.length }} 个文件），不影响直接拖入新素材。
        </span>
        <router-link
          :to="{ name: 'workbench', query: { resume: resumableHint.postId } }"
          class="shrink-0 rounded-full px-3 py-1.5 text-sm font-medium text-brand-strong transition hover:bg-canvas"
        >
          前往续传 →
        </router-link>
      </div>

      <p v-if="message" class="mb-4 rounded-2xl bg-surface px-3.5 py-2.5 text-sm text-muted shadow-[0_1px_0_0_rgba(27,26,23,0.05)]" role="status">
        {{ message }}
      </p>

      <div v-if="snapshot" class="mb-4 rounded-2xl bg-surface p-4 shadow-[0_1px_0_0_rgba(27,26,23,0.05)]" role="status">
        <div class="flex flex-wrap items-start justify-between gap-3">
          <div class="min-w-0">
            <p class="flex items-center gap-2 text-sm font-semibold">
              上次任务 #{{ snapshot.postId }}
              <span
                class="rounded px-1.5 py-0.5 text-[11px] font-medium"
                :class="resuming ? 'bg-accent/10 text-accent' : 'bg-danger-soft text-danger'"
                >{{ resuming ? '已匹配' : '未匹配' }}</span
              >
            </p>
            <p class="mt-1 text-xs leading-relaxed text-muted">
              {{ snapshot.imageCount }} 张，文件：
              {{ snapshot.files.slice(0, 2).map((file) => file.name).join('、')
              }}{{ snapshot.files.length > 2 ? ' 等' : '' }}。
              <template v-if="viewingTask">重新选择同一批文件会按原序号续传。</template>
              <template v-else-if="resuming">续传沿用任务已保存的标题、说明与标签，无需重新填写，发布时只补传缺失序号。</template>
              <template v-else>当前选择与上次任务不一致，请重新选择同一批文件，或放弃此任务后重新开始。</template>
            </p>
          </div>
          <button
            type="button"
            class="h-11 rounded-full px-3.5 text-sm font-medium text-muted transition hover:bg-canvas hover:text-ink disabled:opacity-50"
            :disabled="!canLeaveResume(ready, phase) || busy || working"
            @click="discardSnapshot"
          >
            放弃此任务
          </button>
        </div>
      </div>

      <div v-if="tiles.length === 0 && serverProgress" class="mx-auto mb-4 w-full max-w-[720px]">
        <TaskProgressCard
          :stages="stages"
          :progress="serverProgress"
          :retrying="retrying"
          :publishing="publishing"
          :control-busy="controlBusy"
          @retry="retryFailed"
          @publish="publishToTelegraph"
          @pause="runControl('pause')"
          @resume="runControl('resume')"
          @stop="runControl('stop')"
        />
      </div>

      <div v-if="tiles.length === 0" class="mx-auto w-full max-w-[720px]">
        <DropZone :disabled="!ready || working || locked" @zip="onZip" @images="onImages" @error="pushError">
          <ModeSwitch layout="stack" :mode="mode" :auto-compress="autoCompress" :disabled="!ready || locked" @select="setMode" @toggle-compress="onAutoCompress" />
        </DropZone>
        <p v-if="!ready" class="mt-3 text-center text-sm text-muted">正在读取设置与续传信息…</p>
        <p v-else-if="working" class="mt-3 text-center text-sm text-muted">正在解压压缩包…</p>
      </div>

      <template v-else>
        <div class="lg:grid lg:grid-cols-[minmax(0,1fr)_300px] lg:items-start lg:gap-5">
          <div class="min-w-0 space-y-4">
            <TaskProgressCard
              :stages="stages"
              :progress="serverProgress"
              :retrying="retrying"
              :publishing="publishing"
              :control-busy="controlBusy"
              @retry="retryFailed"
              @publish="publishToTelegraph"
              @pause="runControl('pause')"
              @resume="runControl('resume')"
              @stop="runControl('stop')"
            />

            <MetaPanel
              v-model="draft"
              :meta-text="metaText"
              :meta-source="metaSource"
              :parse-default="metaParseMode"
              :disabled="locked"
              @error="pushError"
            />

            <div class="flex flex-wrap items-end justify-between gap-3">
              <div class="min-w-0">
                <h2 class="text-base font-semibold tracking-tight">已选媒体</h2>
                <p class="mt-1 text-xs text-muted">拖拽或用箭头调整顺序，序号即上传顺序，点选封面用于列表展示。</p>
              </div>
              <button
                type="button"
                class="h-11 rounded-full px-3.5 text-xs font-medium text-muted transition hover:bg-canvas hover:text-ink disabled:opacity-50"
                :disabled="locked"
                @click="onSortByName"
              >
                按文件名排序
              </button>
            </div>

            <div class="rounded-2xl bg-surface px-3 py-3 shadow-[0_1px_0_0_rgba(27,26,23,0.05)] lg:hidden">
              <ModeSwitch :mode="mode" :auto-compress="autoCompress" :disabled="!ready || locked" @select="setMode" @toggle-compress="onAutoCompress" />
            </div>

            <ImageGrid
              :blurred="blurEnabled"
              :items="tiles"
              :mode="mode"
              :auto-compress="autoCompress"
              :cover-id="coverId"
              :statuses="statuses"
              :disabled="working || locked"
              :cover-disabled="coverLocked"
              @move="onMove"
              @remove="onRemove"
              @select-cover="onSelectCover"
              @preview="openPreview"
            />

            <DropZone compact :disabled="working || locked" @zip="onZip" @images="onImages" @error="pushError" />
          </div>

          <aside class="hidden lg:sticky lg:top-[76px] lg:block">
            <div class="space-y-3">
              <section class="rounded-2xl bg-surface p-3.5 shadow-[0_1px_0_0_rgba(27,26,23,0.05)]">
                <p class="text-xs font-medium text-muted">封面缩略图</p>
                <button
                  type="button"
                  class="mt-2 block aspect-[4/3] w-full overflow-hidden rounded bg-canvas"
                  :aria-label="coverBlurred ? '点按揭开封面预览' : '封面缩略图'"
                  @click="coverRevealed = coverBlurred"
                >
                  <img
                    v-if="coverPreview"
                    :src="coverPreview"
                    alt="封面缩略图"
                    class="h-full w-full object-cover transition-[filter] duration-200"
                    :class="coverBlurred ? 'blur-lg' : ''"
                  />
                  <span v-else class="flex h-full items-center justify-center text-xs text-muted">生成中…</span>
                </button>
                <p v-if="coverBlurred" class="mt-1 text-[11px] text-muted">防社死模糊已开启，点按封面可临时揭开。</p>
                <dl class="tnum mt-3 space-y-1 text-xs text-muted">
                  <div class="flex justify-between gap-2">
                    <dt>宽度</dt>
                    <dd>400px WebP</dd>
                  </div>
                  <div class="flex justify-between gap-2">
                    <dt>体积</dt>
                    <dd>{{ coverBytes > 0 ? formatBytes(coverBytes) : '—' }}</dd>
                  </div>
                  <div class="flex justify-between gap-2">
                    <dt>封面文件</dt>
                    <dd class="max-w-[62%] truncate" :title="coverTile?.name">{{ coverTile?.name ?? '—' }}</dd>
                  </div>
                </dl>
              </section>

              <section class="rounded-2xl bg-surface p-3.5 shadow-[0_1px_0_0_rgba(27,26,23,0.05)]">
                <ModeSwitch :mode="mode" :auto-compress="autoCompress" :disabled="!ready || locked" @select="setMode" @toggle-compress="onAutoCompress" />

                <div class="mt-3 border-t border-line pt-3">
                  <p class="tnum text-sm font-medium">
                    <template v-if="postId > 0">任务 #{{ postId }} · {{ uploadedCount }} / {{ imageCount }} 张已提交</template>
                    <template v-else>共 {{ imageCount }} 张 · {{ formatBytes(totalSize) }}</template>
                  </p>
                  <p v-if="progressText" class="mt-1 text-xs leading-relaxed text-muted">{{ progressText }}</p>
                  <p v-else class="mt-1 text-xs leading-relaxed text-muted">封面 {{ coverTile?.name ?? '—' }}</p>
                </div>

                <div class="mt-3 space-y-2">
                  <button
                    type="button"
                    class="h-11 w-full rounded-full bg-brand px-4 text-sm font-medium text-brand-ink transition hover:bg-brand-strong disabled:opacity-60"
                    :disabled="busy || phase === 'queued' || finished"
                    @click="publish"
                  >
                    {{ busy ? '正在上传…' : finished ? '已存入频道' : serverProgress?.status === 'uploading' ? '继续上传' : '提交存图' }}
                  </button>
                  <button
                    v-if="finished"
                    type="button"
                    class="h-11 w-full rounded-full px-3 text-sm font-medium text-muted transition hover:bg-canvas hover:text-ink"
                    @click="clearAll"
                  >
                    再发一组
                  </button>
                  <button
                    v-else-if="phase === 'failed' || phase === 'queued'"
                    type="button"
                    class="h-11 w-full rounded-full px-3 text-sm font-medium text-muted transition hover:bg-canvas hover:text-ink disabled:opacity-50"
                    :disabled="busy || working"
                    @click="discardSnapshot"
                  >
                    重新开始
                  </button>
                </div>
              </section>
            </div>
          </aside>
        </div>

        <div class="sticky bottom-0 z-30 -mx-5 mt-4 border-t border-line bg-surface px-5 py-2.5 lg:hidden">
          <div class="flex items-center gap-3">
            <div class="min-w-0 flex-1">
              <p class="tnum truncate text-sm font-medium">
                <template v-if="postId > 0">任务 #{{ postId }} · {{ uploadedCount }} / {{ imageCount }}</template>
                <template v-else>{{ imageCount }} 张 · {{ formatBytes(totalSize) }}</template>
              </p>
              <p class="truncate text-xs text-muted">{{ progressText !== '' ? progressText : '封面 ' + (coverTile?.name ?? '—') }}</p>
            </div>
            <button
              v-if="finished"
              type="button"
              class="h-11 shrink-0 rounded-full px-3.5 text-sm font-medium text-muted transition hover:bg-canvas hover:text-ink"
              @click="clearAll"
            >
              再发一组
            </button>
            <button
              v-else-if="phase === 'failed' || phase === 'queued'"
              type="button"
              class="h-11 shrink-0 rounded-full px-3.5 text-sm font-medium text-muted transition hover:bg-canvas hover:text-ink disabled:opacity-50"
              :disabled="busy || working"
              @click="discardSnapshot"
            >
              重新开始
            </button>
            <button
              type="button"
              class="h-11 shrink-0 rounded-full bg-brand px-4 text-sm font-medium text-brand-ink transition hover:bg-brand-strong disabled:opacity-60"
              :disabled="busy || phase === 'queued' || finished"
              @click="publish"
            >
              {{ busy ? '正在上传…' : finished ? '已存入频道' : serverProgress?.status === 'uploading' ? '继续上传' : '提交存图' }}
            </button>
          </div>
        </div>
      </template>

      <!-- 视频预览：点击才创建对象 URL，关闭与离开页面都会释放。 -->
      <div
        v-if="previewUrl"
        class="fixed inset-0 z-50 flex items-center justify-center bg-ink/75 p-4"
        role="dialog"
        aria-modal="true"
        aria-label="视频预览"
        @click.self="closePreview"
      >
        <div class="relative w-full max-w-[720px]">
          <video
            :src="previewUrl"
            controls
            autoplay
            playsinline
            class="w-full rounded-2xl bg-ink transition-[filter] duration-200"
            :class="previewBlurred ? 'blur-lg' : ''"
            @click="previewRevealed = previewBlurred"
          />
          <button
            v-if="previewBlurred"
            type="button"
            class="absolute inset-0 flex items-end justify-center rounded-2xl bg-ink/25 pb-3 text-xs font-medium text-white"
            aria-label="点按揭开视频预览"
            @click="previewRevealed = true"
          >
            防社死模糊已开启，点按画面揭开
          </button>
          <button
            type="button"
            class="absolute -top-12 right-0 flex h-11 w-11 items-center justify-center rounded-full bg-surface text-lg text-ink shadow transition hover:bg-canvas"
            aria-label="关闭预览"
            @click="closePreview"
          >
            ×
          </button>
        </div>
      </div>
    </main>
  </div>
</template>
