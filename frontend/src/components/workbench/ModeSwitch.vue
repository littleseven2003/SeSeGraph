<script setup lang="ts">
import { computed } from 'vue'
import { limitForMode, type UploadMode } from '../../utils/image.ts'

// 画质模式切换：空态、桌面右栏与手机顶栏共用同一套按钮与说明。
const props = defineProps<{
  mode: UploadMode
  disabled?: boolean
  layout?: 'row' | 'stack'
  autoCompress: boolean
}>()
const emit = defineEmits<{
  (event: 'select', mode: UploadMode): void
  (event: 'toggle-compress', enabled: boolean): void
}>()

const options: { value: UploadMode; label: string }[] = [
  { value: 'photo', label: 'photo 省流量' },
  { value: 'document', label: 'document 原图' },
]

const limitText = computed(() => {
  if (!props.autoCompress) return '已关闭自动压缩，超限文件需切换模式或移除后才能提交'
  return props.mode === 'document'
    ? '原图上限 20MB，超限静态图自动压缩（GIF 除外）；视频上限 20MB'
    : '单图上限 10MB，超限静态图自动压缩（GIF 除外）；视频上限 20MB'
})
const compressLabel = computed(() => (props.autoCompress ? '自动压缩：开' : '自动压缩：关'))
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
      <div class="flex rounded-full bg-canvas p-1" role="group" aria-label="画质模式">
        <button
          v-for="option in options"
          :key="option.value"
          type="button"
          class="h-11 rounded-full px-3.5 text-xs font-medium transition disabled:opacity-50"
          :class="
            props.mode === option.value
              ? 'bg-accent text-white'
              : 'text-muted hover:bg-surface hover:text-ink'
          "
          :disabled="props.disabled"
          @click="emit('select', option.value)"
        >
          {{ option.label }}
        </button>
      </div>
    </div>

    <div v-else class="flex min-w-0 flex-col gap-2">
      <div class="flex flex-wrap items-center justify-between gap-2">
        <div class="min-w-0">
          <p class="text-xs font-medium text-ink">画质模式</p>
        </div>
        <div class="flex rounded-full bg-canvas p-1" role="group" aria-label="画质模式">
          <button
            v-for="option in options"
            :key="option.value"
            type="button"
            class="h-11 rounded-full px-3.5 text-xs font-medium transition disabled:opacity-50"
            :class="
              props.mode === option.value
                ? 'bg-accent text-white'
                : 'text-muted hover:bg-surface hover:text-ink'
            "
            :disabled="props.disabled"
            @click="emit('select', option.value)"
          >
            {{ option.label }}
          </button>
        </div>
      </div>
      <button
        type="button"
        role="switch"
        :aria-checked="props.autoCompress"
        :aria-label="compressLabel"
        class="flex h-11 items-center justify-between gap-2 rounded-xl px-1 text-xs text-muted transition disabled:opacity-50"
        :disabled="props.disabled"
        @click="emit('toggle-compress', !props.autoCompress)"
      >
        <span class="whitespace-nowrap">自动压缩</span>
        <span
          class="relative inline-flex h-6 w-10 items-center rounded-full transition"
          :class="props.autoCompress ? 'bg-accent' : 'bg-line'"
        >
          <span
            class="absolute h-4 w-4 rounded-full bg-surface shadow transition"
            :class="props.autoCompress ? 'left-5' : 'left-1'"
          />
        </span>
      </button>
      <p class="text-xs text-muted">{{ limitText }}</p>
    </div>
    <p v-if="props.layout === 'stack'" class="text-xs text-muted">{{ limitText }}</p>
  </div>
</template>
