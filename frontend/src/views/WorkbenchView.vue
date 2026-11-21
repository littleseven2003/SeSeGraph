<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import DropZone from '../components/workbench/DropZone.vue'
import ImageGrid from '../components/workbench/ImageGrid.vue'
import MetaPanel from '../components/workbench/MetaPanel.vue'
import ProgressPanel from '../components/workbench/ProgressPanel.vue'
import { commitPost, createPost, fetchProgress, retryPost, uploadImage, type ServerProgress } from '../api/posts.ts'
import { ApiError } from '../api/client.ts'
import { makeCover } from '../utils/cover.ts'
import {
  MAX_IMAGE_COUNT,
  baseName,
  checkFile,
  formatBytes,
  limitForMode,
  mimeForName,
  type UploadMode,
} from '../utils/image.ts'
import { parseMetaText, type PostMeta } from '../utils/meta.ts'
import { sortByNaturalName } from '../utils/natural.ts'
import { browserStorage, clearTask, loadTask, saveTask, type StorageLike, type TaskSnapshot } from '../utils/task-store.ts'
import { isActiveStatus } from '../utils/progress.ts'
import { acceptedCount, initialStatuses, planUploads, type UploadStatus } from '../utils/upload.ts'
import { DEFAULT_META_FILENAMES, unpackZip } from '../utils/zip.ts'
import {
  assignSortOrders,
  createId,
  moveItem,
  removeItem,
  selectCover,
  sortByName,
  totalBytes,
  type WorkbenchTile,
} from '../utils/workbench.ts'

// 上传阶段：只有收尾后按钮才会重新可用。
type Phase = 'idle' | 'creating' | 'uploading' | 'queued' | 'failed'

const storage: StorageLike | null = browserStorage()

const mode = ref<UploadMode>('photo')
const tiles = ref<WorkbenchTile[]>([])
const coverId = ref('')
const working = ref(false)
const message = ref('')
const metaText = ref('')
const metaSource = ref('')
const coverPreview = ref('')
const coverDataUrl = ref('')
const coverBytes = ref(0)
const draft = ref<PostMeta>({ title: '', description: '', tags: [], author: '' })
const postId = ref(0)
const phase = ref<Phase>('idle')
const statuses = ref<Record<number, UploadStatus>>({})
const resumed = ref<TaskSnapshot | null>(null)
const serverProgress = ref<ServerProgress | null>(null)
const retrying = ref(false)
const pollIntervalMs = 2000
let pollTimer: number | undefined

const imageCount = computed(() => tiles.value.length)
const totalSize = computed(() => totalBytes(tiles.value))
const coverTile = computed(() => tiles.value.find((tile) => tile.id === coverId.value))
const uploadedCount = computed(() => acceptedCount(statuses.value))
const busy = computed(() => phase.value === 'creating' || phase.value === 'uploading')
const locked = computed(() => postId.value > 0 || busy.value)
const finished = computed(() => serverProgress.value?.status === 'completed')
const phaseText = computed(() => {
  if (serverProgress.value?.status === 'completed') {
    return '存图完成，图片已进入频道，可以关闭网页'
  }
  if (serverProgress.value?.status === 'failed') {
    return '部分图片存图失败，可在下方重试'
  }
  switch (phase.value) {
    case 'creating':
      return '正在创建任务…'
    case 'uploading':
      return `正在上传 ${uploadedCount.value} / ${imageCount.value}`
    case 'queued':
      return '已加入上传队列，关闭网页后服务端会继续处理'
    case 'failed':
      return '上传中断，重新选择同一批文件后可以继续'
    default:
      return ''
  }
})

onMounted(() => {
  // 上次未完成的任务：提示重新选择同一批文件续传。
  resumed.value = loadTask(storage)
})

function pushError(text: string): void {
  message.value = text
}

function addTile(name: string, blob: Blob, size: number): WorkbenchTile {
  return {
    id: createId(),
    name,
    size,
    mime: mimeForName(name),
    blob,
    sortOrder: 0,
    previewUrl: URL.createObjectURL(blob),
  }
}

function releaseTiles(items: WorkbenchTile[]): void {
  for (const item of items) {
    URL.revokeObjectURL(item.previewUrl)
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
    const cover = await makeCover(tile.blob)
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
}

// 切换画质模式前先确认现有图片在新上限内，避免静默失效。
function setMode(next: UploadMode): void {
  if (next === mode.value || locked.value) return
  const limit = limitForMode(next)
  const oversized = tiles.value.filter((tile) => tile.size > limit)
  if (oversized.length > 0) {
    pushError(`有 ${oversized.length} 张超过 ${limit / (1024 * 1024)}MB 上限，请先移除这些图片再切换`)
    return
  }
  mode.value = next
}

function onZip(file: File): void {
  working.value = true
  message.value = ''
  const reader = new FileReader()
  reader.onload = () => {
    try {
      const result = unpackZip(new Uint8Array(reader.result as ArrayBuffer), { metaNames: DEFAULT_META_FILENAMES })
      if (!result.ok) {
        pushError(result.error)
        return
      }
      releaseTiles(tiles.value)
      const next = result.images.map((image) =>
        addTile(baseName(image.name), new Blob([image.data.slice()], { type: mimeForName(image.name) }), image.data.length),
      )
      metaText.value = result.metaText
      metaSource.value = result.metaText === '' ? '' : result.metaName
      statuses.value = {}
      applyTiles(next)
      if (result.metaText !== '') {
        // 包内存在说明文件时自动预填，缺失字段仍可手动补充。
        const parsed = parseMetaText(result.metaText, 'first-line')
        draft.value = {
          title: parsed.title,
          description: parsed.description,
          tags: parsed.tags,
          author: parsed.author,
        }
      }
      if (result.skipped > 0) {
        message.value = `已忽略 ${result.skipped} 个非图片文件`
      }
      void refreshCover()
    } catch {
      pushError('压缩包读取失败，请确认文件完整且没有加密')
    } finally {
      working.value = false
    }
  }
  reader.onerror = () => {
    working.value = false
    pushError('压缩包读取失败，请重新选择文件')
  }
  reader.readAsArrayBuffer(file)
}

function onImages(files: File[]): void {
  message.value = ''
  const accepted: WorkbenchTile[] = []
  for (const file of files) {
    if (tiles.value.length + accepted.length >= MAX_IMAGE_COUNT) {
      pushError(`单个帖子最多 ${MAX_IMAGE_COUNT} 张图片`)
      break
    }
    const problem = checkFile({ name: file.name, size: file.size }, mode.value, tiles.value.length + accepted.length + 1)
    if (problem) {
      pushError(problem)
      continue
    }
    accepted.push(addTile(baseName(file.name), file, file.size))
  }
  if (accepted.length === 0) return
  // 文件夹与多图入口默认按文件名自然排序，已有顺序保持不变。
  const sorted = sortByNaturalName(accepted, (tile) => tile.name)
  statuses.value = {}
  applyTiles([...tiles.value, ...sorted])
  void refreshCover()
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
  if (removed) URL.revokeObjectURL(removed.previewUrl)
  if (tiles.value.length === 0) {
    coverPreview.value = ''
    coverDataUrl.value = ''
    coverBytes.value = 0
  } else {
    void refreshCover()
  }
}

function onSelectCover(id: string): void {
  coverId.value = id
  void refreshCover()
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

// 用服务端状态刷新图格标记，已存图与已落盘都视为完成。
function applyServerProgress(progress: ServerProgress): void {
  serverProgress.value = progress
  const next: Record<number, UploadStatus> = { ...statuses.value }
  for (const image of progress.images) {
    if (image.status === 'stored' || image.status === 'uploaded') {
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
    if (!isActiveStatus(progress.status)) {
      stopPolling()
      if (progress.status === 'completed') {
        message.value = '存图完成，图片已进入频道'
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

// 手动重试失败的图片，随后恢复轮询。
async function retryFailed(): Promise<void> {
  if (retrying.value || postId.value === 0) return
  retrying.value = true
  message.value = ''
  try {
    const result = await retryPost(postId.value)
    phase.value = result.status === 'queued' ? 'queued' : 'failed'
    startPolling(postId.value)
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
  resumed.value = null
  serverProgress.value = null
  message.value = ''
  stopPolling()
  clearTask(storage)
}

function clearAll(): void {
  releaseTiles(tiles.value)
  tiles.value = []
  coverId.value = ''
  coverPreview.value = ''
  coverDataUrl.value = ''
  coverBytes.value = 0
  metaText.value = ''
  metaSource.value = ''
  draft.value = { title: '', description: '', tags: [], author: '' }
  resetProgress()
}

// 建任务：已存在任务或上次未完成任务时复用，避免重复建帖。
async function ensureTask(): Promise<number | null> {
  if (postId.value > 0) return postId.value
  if (resumed.value && resumed.value.imageCount === tiles.value.length) {
    postId.value = resumed.value.postId
    return postId.value
  }
  if (resumed.value) {
    // 文件数量对不上，说明换了一批图，旧任务不再续传。
    clearTask(storage)
    resumed.value = null
  }
  const created = await createPost({
    title: draft.value.title.trim(),
    description: draft.value.description,
    author_name: draft.value.author.trim(),
    upload_mode: mode.value,
    image_count: tiles.value.length,
    cover_thumbnail: coverDataUrl.value,
    tags: draft.value.tags,
  })
  postId.value = created.id
  saveTask(storage, {
    postId: created.id,
    imageCount: tiles.value.length,
    fileNames: tiles.value.map((tile) => tile.name),
    createdAt: Date.now(),
  })
  return postId.value
}

// 发布：建任务 → 按服务端进度补传 → 提交入队。
async function publish(): Promise<void> {
  if (busy.value) return
  message.value = ''
  if (tiles.value.length === 0) {
    pushError('请先选择图片')
    return
  }
  if (draft.value.title.trim() === '') {
    pushError('请填写总标题')
    return
  }
  if (coverDataUrl.value === '' && postId.value === 0) {
    pushError('封面还在生成，请稍后重试')
    return
  }

  phase.value = 'creating'
  try {
    const id = await ensureTask()
    if (id === null) return

    const progress = await fetchProgress(id)
    statuses.value = initialStatuses(progress)
    if (progress.status !== 'uploading') {
      // 任务已进入队列，交给服务端继续处理。
      phase.value = progress.status === 'failed' ? 'failed' : 'queued'
      applyServerProgress(progress)
      clearTask(storage)
      resumed.value = null
      startPolling(id)
      return
    }

    const planned = planUploads(progress, tiles.value.length)
    if ('error' in planned) {
      phase.value = 'idle'
      postId.value = 0
      pushError(planned.error)
      return
    }

    phase.value = 'uploading'
    for (const item of planned.plan.pending) {
      const tile = tiles.value[item.index]
      statuses.value = { ...statuses.value, [item.sortOrder]: 'uploading' }
      try {
        await uploadImage(id, item.sortOrder, tile.blob, tile.mime)
        statuses.value = { ...statuses.value, [item.sortOrder]: 'uploaded' }
      } catch (error) {
        statuses.value = { ...statuses.value, [item.sortOrder]: 'failed' }
        phase.value = 'failed'
        pushError(error instanceof ApiError ? error.message : `${tile.name} 上传失败，请稍后继续`)
        return
      }
    }

    const committed = await commitPost(id)
    clearTask(storage)
    resumed.value = null
    phase.value = committed.status === 'queued' ? 'queued' : 'failed'
    if (committed.missing && committed.missing.length > 0) {
      pushError('还有图片未上传完整，请继续上传')
      phase.value = 'idle'
      return
    }
    startPolling(id)
  } catch (error) {
    phase.value = 'idle'
    pushError(error instanceof ApiError ? error.message : '发布失败，请稍后重试')
  }
}

onBeforeUnmount(() => {
  stopPolling()
  releaseTiles(tiles.value)
})
</script>

<template>
  <div class="min-h-full">
    <header class="border-b border-line bg-surface">
      <div class="mx-auto flex w-full max-w-[1100px] items-center justify-between gap-4 px-4 py-3">
        <div class="flex items-center gap-3">
          <router-link
            :to="{ name: 'home' }"
            class="flex h-9 w-9 items-center justify-center rounded-lg border border-line text-muted transition hover:bg-canvas hover:text-ink"
            aria-label="返回图集"
          >
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" class="h-4 w-4">
              <path d="M15 6l-6 6 6 6" stroke-linecap="round" stroke-linejoin="round" />
            </svg>
          </router-link>
          <div>
            <p class="text-xs font-medium tracking-wide text-muted">发帖工作台</p>
            <p class="text-sm font-semibold">新建图集</p>
          </div>
        </div>

        <div class="flex items-center gap-2">
          <span class="tnum hidden text-xs text-muted sm:inline"
            >{{ imageCount }} 张 · {{ formatBytes(totalSize) }}</span
          >
          <button
            v-if="tiles.length > 0 && !locked"
            type="button"
            class="h-9 rounded-md border border-line px-3 text-sm font-medium transition hover:border-muted/40 hover:bg-canvas"
            @click="clearAll"
          >
            清空
          </button>
        </div>
      </div>
    </header>

    <main class="mx-auto w-full max-w-[1100px] px-4 py-6">
      <p v-if="message" class="mb-4 rounded-md border border-line bg-surface px-3 py-2 text-sm text-muted" role="status">
        {{ message }}
      </p>

      <div
        v-if="resumed && tiles.length === 0"
        class="mb-4 rounded-md border border-line bg-surface px-3 py-2 text-sm"
        role="status"
      >
        <span class="font-medium">上次有未完成的任务 #{{ resumed.postId }}</span>
        <span class="text-muted">
          （{{ resumed.imageCount }} 张，文件：{{ resumed.fileNames.slice(0, 2).join('、')
          }}{{ resumed.fileNames.length > 2 ? ' 等' : '' }}）。重新选择同一批文件后会接着补传已缺的图片。
        </span>
      </div>

      <div class="mb-3 flex flex-wrap items-center justify-between gap-3 rounded-lg border border-line bg-surface px-4 py-3">
        <div>
          <p class="text-sm font-medium">画质模式</p>
          <p class="mt-0.5 text-xs text-muted">
            {{ mode === 'document' ? 'document 原图，单图上限 20MB' : 'photo 省流量，单图上限 10MB' }}
          </p>
        </div>
        <div class="flex rounded-md border border-line p-0.5" role="group" aria-label="画质模式">
          <button
            v-for="option in [
              { value: 'photo', label: 'photo 省流量' },
              { value: 'document', label: 'document 原图' },
            ]"
            :key="option.value"
            type="button"
            class="h-8 rounded px-2.5 text-xs font-medium transition disabled:opacity-50"
            :class="mode === option.value ? 'bg-brand text-brand-ink' : 'text-muted hover:bg-canvas'"
            :disabled="locked"
            @click="setMode(option.value as UploadMode)"
          >
            {{ option.label }}
          </button>
        </div>
      </div>

      <DropZone :disabled="working || locked" @zip="onZip" @images="onImages" @error="pushError" />

      <div v-if="working" class="mt-3 text-sm text-muted">正在解压压缩包…</div>

      <template v-if="tiles.length > 0">
        <div class="mt-5 flex flex-wrap items-end justify-between gap-3">
          <div>
            <h2 class="text-base font-semibold">图片顺序</h2>
            <p class="mt-0.5 text-xs text-muted">拖拽或使用箭头调整顺序，序号即上传顺序，点选封面用于列表展示。</p>
          </div>
          <button
            type="button"
            class="h-8 rounded-md border border-line px-2.5 text-xs font-medium transition hover:border-muted/40 hover:bg-canvas disabled:opacity-50"
            :disabled="locked"
            @click="onSortByName"
          >
            按文件名排序
          </button>
        </div>

        <div class="mt-3 flex flex-col gap-4 lg:flex-row">
          <div class="min-w-0 flex-1">
            <ImageGrid
              :items="tiles"
              :cover-id="coverId"
              :statuses="statuses"
              :disabled="working || locked"
              @move="onMove"
              @remove="onRemove"
              @select-cover="onSelectCover"
            />
          </div>

          <aside class="w-full shrink-0 lg:w-[280px]">
            <div class="rounded-lg border border-line bg-surface p-3">
              <p class="text-xs font-medium tracking-wide text-muted">封面缩略图</p>
              <div class="mt-2 aspect-[4/3] overflow-hidden rounded-md bg-canvas">
                <img v-if="coverPreview" :src="coverPreview" alt="封面缩略图" class="h-full w-full object-cover" />
                <div v-else class="flex h-full items-center justify-center text-xs text-muted">生成中…</div>
              </div>
              <dl class="tnum mt-3 space-y-1 text-xs text-muted">
                <div class="flex justify-between">
                  <dt>宽度</dt>
                  <dd>400px WebP</dd>
                </div>
                <div class="flex justify-between">
                  <dt>体积</dt>
                  <dd>{{ coverBytes > 0 ? formatBytes(coverBytes) : '—' }}</dd>
                </div>
                <div class="flex justify-between">
                  <dt>封面文件</dt>
                  <dd class="max-w-[60%] truncate" :title="coverTile?.name">{{ coverTile?.name ?? '—' }}</dd>
                </div>
              </dl>
            </div>
          </aside>
        </div>

        <div class="mt-5">
          <MetaPanel
            v-model="draft"
            :meta-text="metaText"
            :meta-source="metaSource"
            :disabled="locked"
            @error="pushError"
          />
        </div>

        <div v-if="serverProgress" class="mt-4">
          <ProgressPanel :progress="serverProgress" :retrying="retrying" @retry="retryFailed" />
        </div>

        <div class="mt-4 flex flex-wrap items-center justify-between gap-3 rounded-lg border border-line bg-surface px-4 py-3">
          <div class="min-w-0">
            <p class="tnum text-sm font-medium">
              <template v-if="postId > 0">任务 #{{ postId }} · {{ uploadedCount }} / {{ imageCount }} 张已提交</template>
              <template v-else>共 {{ imageCount }} 张 · 封面 {{ coverTile?.name ?? '—' }} · {{ formatBytes(totalSize) }}</template>
            </p>
            <p v-if="phaseText" class="mt-0.5 text-xs text-muted">{{ phaseText }}</p>
          </div>
          <div class="flex items-center gap-2">
            <button
              v-if="finished"
              type="button"
              class="h-10 rounded-md border border-line px-3 text-sm font-medium transition hover:border-muted/40 hover:bg-canvas"
              @click="clearAll"
            >
              再发一组
            </button>
            <button
              v-else-if="phase === 'failed' || phase === 'queued'"
              type="button"
              class="h-10 rounded-md border border-line px-3 text-sm font-medium transition hover:border-muted/40 hover:bg-canvas"
              @click="resetProgress"
            >
              重新开始
            </button>
            <button
              type="button"
              class="h-10 rounded-md bg-brand px-4 text-sm font-medium text-brand-ink transition hover:bg-brand-strong disabled:opacity-60"
              :disabled="busy || phase === 'queued' || finished"
              @click="publish"
            >
              {{ busy ? '正在上传…' : finished ? '存图完成' : resumed && tiles.length > 0 && postId === 0 ? '继续上传' : '发布' }}
            </button>
          </div>
        </div>
      </template>
    </main>
  </div>
</template>
