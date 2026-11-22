<script setup lang="ts">
import { computed } from 'vue'
import { limitForMode, type UploadMode } from '../../utils/image.ts'

// 画质模式切换：空态、桌面右栏与手机顶栏共用同一套按钮与说明。
const props = defineProps<{ mode: UploadMode; disabled?: boolean; layout?: 'row' | 'stack' }>()
const emit = defineEmits<{ (event: 'select', mode: UploadMode): void }>()

const options: { value: UploadMode; label: string }[] = [
  { value: 'photo', label: 'photo 省流量' },
  { value: 'document', label: 'document 原图' },
]

const limitText = computed(() =>
  props.mode === 'document' ? '单图上限 20MB' : '单图上限 10MB，频道内按图片保存',
)
</script>

<template>
  <div
    :class="
      props.layout === 'stack'
        ? 'flex flex-col items-center gap-2'
        : 'flex flex-wrap items-center justify-between gap-2'
    "
  >
    <div v-if="props.layout === 'stack'" class="flex flex-wrap items-center justify-center gap-2">
      <span class="text-xs text-muted">画质模式</span>
      <div class="flex rounded-md border border-line p-0.5" role="group" aria-label="画质模式">
        <button
          v-for="option in options"
          :key="option.value"
          type="button"
          class="h-11 rounded px-3 text-xs font-medium transition disabled:opacity-50"
          :class="props.mode === option.value ? 'bg-accent text-white' : 'text-muted hover:bg-canvas'"
          :disabled="props.disabled"
          @click="emit('select', option.value)"
        >
          {{ option.label }}
        </button>
      </div>
    </div>

    <div v-else class="min-w-0">
      <p class="text-xs font-medium text-ink">画质模式</p>
      <p class="mt-0.5 text-xs text-muted">{{ limitText }}</p>
    </div>

    <p v-if="props.layout === 'stack'" class="text-xs text-muted">{{ limitText }}</p>

    <div
      v-if="props.layout !== 'stack'"
      class="flex rounded-md border border-line p-0.5"
      role="group"
      aria-label="画质模式"
    >
        <button
          v-for="option in options"
          :key="option.value"
          type="button"
          class="h-11 rounded px-3 text-xs font-medium transition disabled:opacity-50"
          :class="props.mode === option.value ? 'bg-accent text-white' : 'text-muted hover:bg-canvas'"
          :disabled="props.disabled"
          @click="emit('select', option.value)"
        >
          {{ option.label }}
        </button>
      </div>
  </div>
</template>
