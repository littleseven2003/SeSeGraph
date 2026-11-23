<script setup lang="ts">
import { ref } from 'vue'
import { formatBytes } from '../../utils/image.ts'
import { evaluateMedia, formatDuration } from '../../utils/media.ts'
import type { UploadMode } from '../../utils/image.ts'
import type { UploadStatus } from '../../utils/upload.ts'
import type { WorkbenchTile } from '../../utils/workbench.ts'

// 图片列表：支持拖拽重排、点选封面、剔除单张。
const props = defineProps<{
  items: WorkbenchTile[]
  coverId: string
  /** 各序号的上传状态，未出现表示待上传 */
  statuses?: Record<number, UploadStatus>
  disabled?: boolean
  mode: UploadMode
  autoCompress: boolean
}>()

const STATUS_TEXT: Record<UploadStatus, string> = {
  pending: '待上传',
  uploading: '上传中',
  uploaded: '已传至服务器',
  stored: '已存入频道',
  failed: '上传失败',
}

const STATUS_CLASS: Record<UploadStatus, string> = {
  pending: 'bg-ink/60 text-white',
  uploading: 'bg-brand text-brand-ink',
  uploaded: 'bg-accent text-white',
  stored: 'bg-accent-soft text-accent',
  failed: 'bg-danger text-white',
}

// badgeOf 按当前模式与自动压缩开关实时判定合规状态。
function badgeOf(item: WorkbenchTile): { state: string; label: string } {
  return evaluateMedia(
    {
      kind: item.kind ?? 'image',
      size: item.sourceSize ?? item.size,
      compressed: item.compressedFrom !== undefined,
      animated: item.animated,
    },
    props.mode,
    props.autoCompress,
  )
}

const BADGE_CLASS: Record<string, string> = {
  ok: 'bg-accent-soft text-accent',
  compressed: 'bg-accent text-white',
  pending: 'bg-warn-soft text-warn',
  blocked: 'bg-danger text-white',
}

function statusOf(sortOrder: number): UploadStatus {
  return props.statuses?.[sortOrder] ?? 'pending'
}
const emit = defineEmits<{
  (event: 'select-cover', id: string): void
  (event: 'remove', id: string): void
  (event: 'move', payload: { from: number; to: number }): void
  (event: 'preview', id: string): void
}>()

const dragIndex = ref(-1)
const overIndex = ref(-1)

function onDragStart(index: number): void {
  if (props.disabled) return
  dragIndex.value = index
}

function onDragOver(index: number): void {
  if (props.disabled || dragIndex.value < 0) return
  overIndex.value = index
}

function onDrop(index: number): void {
  if (props.disabled) return
  const from = dragIndex.value
  dragIndex.value = -1
  overIndex.value = -1
  if (from < 0 || from === index) return
  emit('move', { from, to: index })
}

function onDragEnd(): void {
  dragIndex.value = -1
  overIndex.value = -1
}

function moveBy(index: number, delta: number): void {
  const target = index + delta
  if (target < 0 || target >= props.items.length) return
  emit('move', { from: index, to: target })
}
</script>

<template>
  <ul class="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-4">
    <li
      v-for="(item, index) in props.items"
      :key="item.id"
      :id="`tile-${item.id}`"
      class="group relative overflow-hidden rounded-2xl bg-surface shadow-[0_1px_0_0_rgba(27,26,23,0.06)] transition"
      :class="[
        item.id === props.coverId ? 'border-brand ring-1 ring-brand/30' : 'border-line',
        badgeOf(item).state === 'blocked' ? 'ring-1 ring-danger/45' : '',
        overIndex === index ? 'border-brand' : '',
      ]"
      :draggable="!props.disabled"
      @dragstart="onDragStart(index)"
      @dragover.prevent="onDragOver(index)"
      @drop.prevent="onDrop(index)"
      @dragend="onDragEnd"
    >
      <div class="relative aspect-[4/3] overflow-hidden bg-canvas">
        <img
          :src="item.previewUrl"
          :alt="item.name"
          class="h-full w-full object-cover"
          :class="item.kind === 'video' ? 'cursor-pointer' : ''"
          loading="lazy"
          @click="item.kind === 'video' ? emit('preview', item.id) : undefined"
        />
        <span class="tnum absolute left-2 top-2 rounded bg-ink/70 px-1.5 py-0.5 text-xs font-medium text-white">{{
          index + 1
        }}</span>
        <span
          v-if="item.id === props.coverId"
          class="absolute right-2 top-2 rounded bg-brand px-1.5 py-0.5 text-xs font-medium text-brand-ink"
          >封面</span
        >
        <span
          v-if="item.kind === 'video'"
          class="tnum absolute bottom-2 right-2 rounded bg-ink/75 px-1.5 py-0.5 text-[11px] font-medium text-white"
          >{{ item.duration ? `▶ ${formatDuration(item.duration)}` : '▶ MP4' }}</span
        >
        <span
          class="absolute bottom-2 left-2 rounded px-1.5 py-0.5 text-xs font-medium"
          :class="STATUS_CLASS[statusOf(item.sortOrder)]"
          >{{ STATUS_TEXT[statusOf(item.sortOrder)] }}</span
        >
      </div>

      <div class="px-2 py-2">
        <p class="truncate text-xs font-medium" :title="item.name">{{ item.name }}</p>
        <p class="tnum mt-0.5 text-xs text-muted">
          <template v-if="item.compressedFrom">
            {{ formatBytes(item.compressedFrom) }} → {{ formatBytes(item.size) }}
            <span class="ml-1 rounded bg-accent-soft px-1.5 py-0.5 text-[11px] font-medium text-accent">已压缩</span>
          </template>
          <template v-else>
            {{ formatBytes(item.size) }}<span v-if="item.kind === 'video' && item.duration"> · {{ formatDuration(item.duration) }}</span>
          </template>
        </p>
        <span
          class="mt-1 inline-block rounded px-1.5 py-0.5 text-[11px] font-medium"
          :class="BADGE_CLASS[badgeOf(item).state]"
          :title="`${badgeOf(item).label}（当前 ${props.mode === 'photo' ? '10MB' : '20MB'} 上限）`"
          >{{ badgeOf(item).label }}</span
        >
      </div>

      <div class="flex items-center justify-between gap-1 border-t border-line px-1.5 py-1.5">
        <div class="flex items-center gap-0.5">
          <button
            type="button"
            class="h-7 w-7 rounded text-xs text-muted transition hover:bg-canvas hover:text-ink disabled:opacity-40"
            :disabled="props.disabled || index === 0"
            :aria-label="`前移 ${item.name}`"
            @click="moveBy(index, -1)"
          >
            ←
          </button>
          <button
            type="button"
            class="h-7 w-7 rounded text-xs text-muted transition hover:bg-canvas hover:text-ink disabled:opacity-40"
            :disabled="props.disabled || index === props.items.length - 1"
            :aria-label="`后移 ${item.name}`"
            @click="moveBy(index, 1)"
          >
            →
          </button>
        </div>
        <div class="flex items-center gap-0.5">
          <button
            type="button"
            class="h-7 rounded px-1.5 text-xs font-medium text-muted transition hover:bg-canvas hover:text-brand-strong disabled:opacity-40"
            :disabled="props.disabled || item.id === props.coverId"
            :aria-label="`将 ${item.name} 设为封面`"
            @click="emit('select-cover', item.id)"
          >
            设为封面
          </button>
          <button
            type="button"
            class="h-7 w-7 rounded text-muted transition hover:bg-danger-soft hover:text-danger disabled:opacity-40"
            :disabled="props.disabled"
            :aria-label="`移除 ${item.name}`"
            @click="emit('remove', item.id)"
          >
            ×
          </button>
        </div>
      </div>
    </li>
  </ul>
</template>
