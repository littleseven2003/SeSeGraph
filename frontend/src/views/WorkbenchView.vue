<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue'
import DropZone from '../components/workbench/DropZone.vue'
import ImageGrid from '../components/workbench/ImageGrid.vue'
import MetaPanel from '../components/workbench/MetaPanel.vue'
import { createPost } from '../api/posts.ts'
import { ApiError } from '../api/client.ts'
import { makeCover } from '../utils/cover.ts'
import { MAX_IMAGE_COUNT, baseName, checkFile, formatBytes, mimeForName, type UploadMode } from '../utils/image.ts'
import { parseMetaText, type PostMeta } from '../utils/meta.ts'
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

const mode = ref<UploadMode>('photo')
const tiles = ref<WorkbenchTile[]>([])
const coverId = ref('')
const working = ref(false)
const message = ref('')
const metaText = ref('')
const metaSource = ref('')
const coverPreview = ref('')
const coverBytes = ref(0)
const coverDataUrl = ref('')
const draft = ref<PostMeta>({ title: '', description: '', tags: [], author: '' })
const creating = ref(false)
const createdId = ref(0)

const imageCount = computed(() => tiles.value.length)
const totalSize = computed(() => totalBytes(tiles.value))
const coverTile = computed(() => tiles.value.find((tile) => tile.id === coverId.value))

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

// 重建封面缩略图：宽度 400px 的 WebP，仅用于列表展示与建任务请求体。
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

function onZip(file: File): void {
  working.value = true
  message.value = ''
  const reader = new FileReader()
  reader.onload = () => {
    try {
      const unpacked = unpackZip(new Uint8Array(reader.result as ArrayBuffer), DEFAULT_META_FILENAMES)
      if (unpacked.images.length === 0) {
        pushError('压缩包里没有找到受支持的图片')
        return
      }
      if (unpacked.images.length > MAX_IMAGE_COUNT) {
        pushError(`单个帖子最多 ${MAX_IMAGE_COUNT} 张图片，请先精简压缩包`)
        return
      }
      releaseTiles(tiles.value)
      const next = unpacked.images.map((image) =>
        addTile(baseName(image.name), new Blob([image.data.slice()], { type: mimeForName(image.name) }), image.data.length),
      )
      metaText.value = unpacked.metaText
      metaSource.value = unpacked.metaText === '' ? '' : unpacked.metaName
      applyTiles(next)
      if (unpacked.metaText !== '') {
        // 包内存在说明文件时自动预填，缺失字段仍可手动补充。
        const parsed = parseMetaText(unpacked.metaText, 'first-line')
        draft.value = {
          title: parsed.title,
          description: parsed.description,
          tags: parsed.tags,
          author: parsed.author,
        }
      }
      if (unpacked.skipped > 0) {
        message.value = `已忽略 ${unpacked.skipped} 个非图片文件`
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
  applyTiles([...tiles.value, ...accepted])
  void refreshCover()
}

function onMove(payload: { from: number; to: number }): void {
  applyTiles(moveItem(tiles.value, payload.from, payload.to))
}

function onRemove(id: string): void {
  const removed = tiles.value.find((tile) => tile.id === id)
  applyTiles(removeItem(tiles.value, id))
  if (removed) URL.revokeObjectURL(removed.previewUrl)
  if (tiles.value.length === 0) {
    coverPreview.value = ''
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
  applyTiles(sortByName(tiles.value))
  void refreshCover()
}

// 创建任务：先落元数据与封面，逐张上传在后续步骤完成。
async function submitMetadata(): Promise<void> {
  if (creating.value) return
  if (tiles.value.length === 0) {
    pushError('请先选择图片')
    return
  }
  const title = draft.value.title.trim()
  if (title === '') {
    pushError('请填写总标题')
    return
  }
  if (coverDataUrl.value === '') {
    pushError('封面还在生成，请稍后重试')
    return
  }
  creating.value = true
  message.value = ''
  try {
    const created = await createPost({
      title,
      description: draft.value.description,
      author_name: draft.value.author.trim(),
      upload_mode: mode.value,
      image_count: tiles.value.length,
      cover_thumbnail: coverDataUrl.value,
      tags: draft.value.tags,
    })
    createdId.value = created.id
    message.value = `任务 #${created.id} 已创建，元数据与封面已保存`
  } catch (error) {
    pushError(error instanceof ApiError ? error.message : '创建任务失败，请稍后重试')
  } finally {
    creating.value = false
  }
}

function clearAll(): void {
  releaseTiles(tiles.value)
  tiles.value = []
  coverId.value = ''
  coverPreview.value = ''
  coverBytes.value = 0
  metaText.value = ''
  metaSource.value = ''
  message.value = ''
  draft.value = { title: '', description: '', tags: [], author: '' }
  createdId.value = 0
}

onBeforeUnmount(() => releaseTiles(tiles.value))
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
            v-if="tiles.length > 0"
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

      <DropZone :disabled="working" @zip="onZip" @images="onImages" @error="pushError" />

      <div v-if="working" class="mt-3 text-sm text-muted">正在解压压缩包…</div>

      <template v-if="tiles.length > 0">
        <div class="mt-5 flex flex-wrap items-end justify-between gap-3">
          <div>
            <h2 class="text-base font-semibold">图片顺序</h2>
            <p class="mt-0.5 text-xs text-muted">拖拽或使用箭头调整顺序，序号即上传顺序，点选封面用于列表展示。</p>
          </div>
          <div class="flex items-center gap-2">
            <div class="flex rounded-md border border-line bg-surface p-0.5" role="group" aria-label="画质模式">
              <button
                v-for="option in [
                  { value: 'photo', label: 'photo 省流量' },
                  { value: 'document', label: 'document 原图' },
                ]"
                :key="option.value"
                type="button"
                class="h-8 rounded px-2.5 text-xs font-medium transition"
                :class="mode === option.value ? 'bg-brand text-brand-ink' : 'text-muted hover:bg-canvas'"
                @click="mode = option.value as UploadMode"
              >
                {{ option.label }}
              </button>
            </div>
            <button
              type="button"
              class="h-8 rounded-md border border-line px-2.5 text-xs font-medium transition hover:border-muted/40 hover:bg-canvas"
              @click="onSortByName"
            >
              按文件名排序
            </button>
          </div>
        </div>

        <div class="mt-3 flex flex-col gap-4 lg:flex-row">
          <div class="min-w-0 flex-1">
            <ImageGrid
              :items="tiles"
              :cover-id="coverId"
              :disabled="working"
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
            :disabled="working || creating || createdId > 0"
            @error="pushError"
          />
        </div>

        <div class="mt-4 flex flex-wrap items-center justify-between gap-3 rounded-lg border border-line bg-surface px-4 py-3">
          <p class="text-xs text-muted">
            <template v-if="createdId > 0">任务 #{{ createdId }} 已保存，逐张上传将在后续步骤完成。</template>
            <template v-else>共 {{ imageCount }} 张，选中封面 {{ coverTile?.name ?? '—' }}，{{ formatBytes(totalSize) }}</template>
          </p>
          <button
            type="button"
            class="h-10 rounded-md bg-brand px-4 text-sm font-medium text-brand-ink transition hover:bg-brand-strong disabled:opacity-60"
            :disabled="creating || createdId > 0 || working"
            @click="submitMetadata"
          >
            {{ creating ? '正在创建…' : createdId > 0 ? '任务已创建' : '创建任务' }}
          </button>
        </div>
      </template>
    </main>
  </div>
</template>
