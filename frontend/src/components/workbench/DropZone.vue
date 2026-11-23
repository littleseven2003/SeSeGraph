<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { collectEntryFiles, type CollectedFiles } from '../../utils/entries.ts'

// 选图入口：ZIP 压缩包、本地文件夹或多张图片，支持拖拽。
const props = defineProps<{ disabled?: boolean; compact?: boolean }>()
const emit = defineEmits<{
  (event: 'zip', file: File): void
  (event: 'images', files: File[], unread?: string[]): void
  (event: 'error', message: string): void
}>()

const zipInput = ref<HTMLInputElement | null>(null)
const folderInput = ref<HTMLInputElement | null>(null)
const imageInput = ref<HTMLInputElement | null>(null)
const dragging = ref(false)

onMounted(() => {
  // 文件夹选择依赖非标准属性，Vue 模板无法直接声明。
  folderInput.value?.setAttribute('webkitdirectory', '')
})

function open(target: HTMLInputElement | null): void {
  if (props.disabled) return
  target?.click()
}

function takeZip(event: Event): void {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (file) emit('zip', file)
}

function takeImages(event: Event): void {
  const input = event.target as HTMLInputElement
  const files = Array.from(input.files ?? [])
  input.value = ''
  if (files.length > 0) emit('images', files, [])
}

async function onDrop(event: DragEvent): Promise<void> {
  dragging.value = false
  if (props.disabled) return
  const transfer = event.dataTransfer
  if (!transfer) return
  const result: CollectedFiles = { files: [], failed: [] }
  const entries: FileSystemEntry[] = []
  for (const item of Array.from(transfer.items)) {
    const entry = item.webkitGetAsEntry?.()
    if (entry) entries.push(entry)
  }
  if (entries.length > 0) {
    for (const entry of entries) {
      await collectEntryFiles(entry, result)
    }
    // 部分条目可能拿不到 entry，用 files 里尚未收录的同名同体积文件补回，避免静默少读。
    for (const file of Array.from(transfer.files)) {
      if (!result.files.some((item) => item.name === file.name && item.size === file.size)) {
        result.files.push(file)
      }
    }
  } else {
    result.files.push(...Array.from(transfer.files))
  }
  const collected = result.files
  if (collected.length === 0) {
    emit('error', result.failed.length > 0 ? `有 ${result.failed.length} 个文件读取失败，请重新拖入或改用选择器` : '没有读取到可用的文件')
    return
  }
  const zip = collected.find((file) => file.name.toLowerCase().endsWith('.zip'))
  if (zip) {
    if (collected.length > 1) {
      emit('error', '一次只能放入一个 ZIP 压缩包，请先解压或分批上传')
      return
    }
    emit('zip', zip)
    return
  }
  emit('images', collected, result.failed)
}
</script>

<template>
  <section
    v-if="props.compact"
    class="flex flex-wrap items-center justify-center gap-x-3 gap-y-2 rounded-2xl bg-surface px-4 py-3 shadow-[0_1px_0_0_rgba(27,26,23,0.05)] transition"
    :class="dragging ? 'border-brand bg-brand/5' : 'border-line'"
    @dragover.prevent="dragging = !props.disabled"
    @dragleave.prevent="dragging = false"
    @drop.prevent="onDrop"
  >
    <p class="text-xs text-muted">拖入更多图片或压缩包，会追加到当前列表</p>
    <div class="flex items-center gap-1.5">
      <button
        type="button"
        :disabled="props.disabled"
        class="h-11 rounded-full bg-canvas px-3 text-xs font-medium text-muted transition hover:bg-line/50 hover:text-ink disabled:opacity-60"
        @click="open(imageInput)"
      >
        选择图片 / 视频
      </button>
      <button
        type="button"
        :disabled="props.disabled"
        class="h-11 rounded-full bg-canvas px-3 text-xs font-medium text-muted transition hover:bg-line/50 hover:text-ink disabled:opacity-60"
        @click="open(folderInput)"
      >
        选择文件夹
      </button>
      <button
        type="button"
        :disabled="props.disabled"
        class="h-11 rounded-full bg-canvas px-3 text-xs font-medium text-muted transition hover:bg-line/50 hover:text-ink disabled:opacity-60"
        @click="open(zipInput)"
      >
        选择 ZIP
      </button>
    </div>
    <input ref="zipInput" type="file" accept=".zip,application/zip" class="hidden" @change="takeZip" />
    <input ref="folderInput" type="file" multiple class="hidden" @change="takeImages" />
    <input ref="imageInput" type="file" accept="image/*,video/mp4,.mp4" multiple class="hidden" @change="takeImages" />
  </section>

  <section
    v-else
    class="rounded-2xl bg-surface px-6 py-12 text-center shadow-[0_1px_0_0_rgba(27,26,23,0.05),0_18px_40px_-32px_rgba(27,26,23,0.28)] transition"
    :class="dragging ? 'border-brand bg-brand/5' : 'border-line'"
    @dragover.prevent="dragging = !props.disabled"
    @dragleave.prevent="dragging = false"
    @drop.prevent="onDrop"
  >
    <div class="mx-auto flex h-11 w-11 items-center justify-center rounded-lg bg-canvas" aria-hidden="true">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" class="h-5 w-5 text-muted">
        <path d="M12 16V4m0 0L8 8m4-4l4 4" stroke-linecap="round" stroke-linejoin="round" />
        <path d="M4 15v3a2 2 0 002 2h12a2 2 0 002-2v-3" stroke-linecap="round" />
      </svg>
    </div>
    <p class="mt-3 text-sm font-medium">把 ZIP、文件夹、图片或 MP4 短视频拖到这里</p>
    <p class="mt-1 text-xs text-muted">拖入 ZIP、整个文件夹或一组图片，在浏览器本地完成排序与压图，不占服务器算力</p>

    <div class="mt-4 flex flex-wrap items-center justify-center gap-2">
      <button
        type="button"
        :disabled="props.disabled"
        class="h-11 rounded-full bg-brand px-5 text-sm font-medium text-brand-ink transition hover:bg-brand-strong disabled:opacity-60"
        @click="open(zipInput)"
      >
        选择 ZIP
      </button>
      <button
        type="button"
        :disabled="props.disabled"
        class="h-11 rounded-full bg-canvas px-4 text-sm font-medium text-muted transition hover:bg-line/50 hover:text-ink disabled:opacity-60"
        @click="open(folderInput)"
      >
        选择文件夹
      </button>
      <button
        type="button"
        :disabled="props.disabled"
        class="h-11 rounded-full bg-canvas px-4 text-sm font-medium text-muted transition hover:bg-line/50 hover:text-ink disabled:opacity-60"
        @click="open(imageInput)"
      >
        选择图片 / 视频
      </button>
    </div>

    <div v-if="$slots.default" class="mt-5 border-t border-line pt-4">
      <slot />
    </div>

    <input ref="zipInput" type="file" accept=".zip,application/zip" class="hidden" @change="takeZip" />
    <input ref="folderInput" type="file" multiple class="hidden" @change="takeImages" />
    <input ref="imageInput" type="file" accept="image/*,video/mp4,.mp4" multiple class="hidden" @change="takeImages" />
  </section>
</template>
