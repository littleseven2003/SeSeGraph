<script setup lang="ts">
import { onMounted, ref } from 'vue'

// 选图入口：ZIP 压缩包、本地文件夹或多张图片，支持拖拽。
const props = defineProps<{ disabled?: boolean }>()
const emit = defineEmits<{
  (event: 'zip', file: File): void
  (event: 'images', files: File[]): void
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
  if (files.length > 0) emit('images', files)
}

// 递归读取拖入的目录，收集其中的文件。
async function readEntry(entry: FileSystemEntry, collected: File[]): Promise<void> {
  if (entry.isFile) {
    const file = await new Promise<File | null>((resolve) => {
      ;(entry as FileSystemFileEntry).file(
        (value) => resolve(value),
        () => resolve(null),
      )
    })
    if (file) collected.push(file)
    return
  }
  if (!entry.isDirectory) return
  const reader = (entry as FileSystemDirectoryEntry).createReader()
  const readBatch = (): Promise<FileSystemEntry[]> =>
    new Promise((resolve) => {
      reader.readEntries(
        (entries) => resolve(entries),
        () => resolve([]),
      )
    })
  let batch = await readBatch()
  while (batch.length > 0) {
    for (const child of batch) {
      await readEntry(child, collected)
    }
    batch = await readBatch()
  }
}

async function onDrop(event: DragEvent): Promise<void> {
  dragging.value = false
  if (props.disabled) return
  const transfer = event.dataTransfer
  if (!transfer) return
  const collected: File[] = []
  const entries: FileSystemEntry[] = []
  for (const item of Array.from(transfer.items)) {
    const entry = item.webkitGetAsEntry?.()
    if (entry) entries.push(entry)
  }
  if (entries.length > 0) {
    for (const entry of entries) {
      await readEntry(entry, collected)
    }
  } else {
    collected.push(...Array.from(transfer.files))
  }
  if (collected.length === 0) {
    emit('error', '没有读取到可用的文件')
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
  emit('images', collected)
}
</script>

<template>
  <section
    class="rounded-lg border border-dashed bg-surface px-6 py-10 text-center transition"
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
    <p class="mt-3 text-sm font-medium">把 ZIP、文件夹或多张图片拖到这里</p>
    <p class="mt-1 text-xs text-muted">解压、排序与封面压缩都在浏览器内完成，服务器不承担解码压力</p>

    <div class="mt-4 flex flex-wrap items-center justify-center gap-2">
      <button
        type="button"
        :disabled="props.disabled"
        class="h-9 rounded-md bg-brand px-3 text-sm font-medium text-brand-ink transition hover:bg-brand-strong disabled:opacity-60"
        @click="open(zipInput)"
      >
        选择 ZIP
      </button>
      <button
        type="button"
        :disabled="props.disabled"
        class="h-9 rounded-md border border-line px-3 text-sm font-medium transition hover:border-muted/40 hover:bg-canvas disabled:opacity-60"
        @click="open(folderInput)"
      >
        选择文件夹
      </button>
      <button
        type="button"
        :disabled="props.disabled"
        class="h-9 rounded-md border border-line px-3 text-sm font-medium transition hover:border-muted/40 hover:bg-canvas disabled:opacity-60"
        @click="open(imageInput)"
      >
        选择图片
      </button>
    </div>

    <input ref="zipInput" type="file" accept=".zip,application/zip" class="hidden" @change="takeZip" />
    <input ref="folderInput" type="file" multiple class="hidden" @change="takeImages" />
    <input ref="imageInput" type="file" accept="image/*" multiple class="hidden" @change="takeImages" />
  </section>
</template>
