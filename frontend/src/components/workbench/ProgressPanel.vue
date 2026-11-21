<script setup lang="ts">
import { computed } from 'vue'
import { imageStatusLabel, needsRetry, postStatusLabel, progressPercent, summaryText } from '../../utils/progress.ts'
import type { ServerProgress } from '../../api/posts.ts'

// 服务端进度面板：展示整体状态、逐图状态与重试入口。
const props = defineProps<{
  progress: ServerProgress
  retrying?: boolean
}>()
const emit = defineEmits<{ (event: 'retry'): void }>()

const percent = computed(() => progressPercent(props.progress))
const canRetry = computed(() => needsRetry(props.progress))
const tone = computed(() => {
  if (props.progress.status === 'completed') return 'border-accent/40 bg-accent/5'
  if (props.progress.status === 'failed') return 'border-danger/40 bg-danger-soft'
  return 'border-line bg-surface'
})
</script>

<template>
  <section class="rounded-lg border p-4" :class="tone">
    <div class="flex flex-wrap items-start justify-between gap-3">
      <div>
        <p class="flex items-center gap-2 text-sm font-semibold">
          <span
            class="inline-block h-1.5 w-1.5 rounded-full"
            :class="
              props.progress.status === 'completed'
                ? 'bg-accent'
                : props.progress.status === 'failed'
                  ? 'bg-danger'
                  : 'bg-brand'
            "
            aria-hidden="true"
          ></span>
          {{ postStatusLabel(props.progress.status) }}
        </p>
        <p class="tnum mt-1 text-xs text-muted">{{ summaryText(props.progress) }}</p>
      </div>

      <div class="flex items-center gap-2">
        <button
          v-if="canRetry"
          type="button"
          class="h-9 rounded-md bg-brand px-3 text-sm font-medium text-brand-ink transition hover:bg-brand-strong disabled:opacity-60"
          :disabled="props.retrying"
          @click="emit('retry')"
        >
          {{ props.retrying ? '正在重试…' : `重试 ${props.progress.failed_count} 张` }}
        </button>
      </div>
    </div>

    <div class="mt-3 h-1.5 w-full overflow-hidden rounded-full bg-canvas" role="progressbar" :aria-valuenow="percent" aria-valuemin="0" aria-valuemax="100">
      <div
        class="h-full rounded-full transition-[width]"
        :class="props.progress.status === 'failed' ? 'bg-danger' : 'bg-brand'"
        :style="{ width: `${percent}%` }"
      ></div>
    </div>

    <ul class="mt-3 grid grid-cols-2 gap-x-4 gap-y-1 sm:grid-cols-3 lg:grid-cols-4">
      <li
        v-for="image in props.progress.images"
        :key="image.sort_order"
        class="tnum flex items-center justify-between gap-2 text-xs"
      >
        <span class="text-muted">#{{ image.sort_order }}</span>
        <span
          class="truncate"
          :class="image.status === 'failed' ? 'text-danger' : image.status === 'stored' ? 'text-ink' : 'text-muted'"
          :title="image.error ?? ''"
          >{{ imageStatusLabel(image.status) }}</span
        >
      </li>
    </ul>
  </section>
</template>
