<script setup lang="ts">
import { computed } from 'vue'
import {
  canRequestPublish,
  imageStatusLabel,
  needsRetry,
  postStatusLabel,
  progressPercent,
  publishActive,
  publishStateLabel,
  publishSummary,
  summaryText,
} from '../../utils/progress.ts'
import type { ServerProgress } from '../../api/posts.ts'

// 服务端进度面板：展示整体状态、逐图状态与重试入口。
const props = defineProps<{
  progress: ServerProgress
  retrying?: boolean
  publishing?: boolean
}>()
const emit = defineEmits<{ (event: 'retry'): void; (event: 'publish'): void }>()
const canPublish = computed(() => canRequestPublish(props.progress))

const percent = computed(() => progressPercent(props.progress))
const canRetry = computed(() => needsRetry(props.progress))
const tone = computed(() => {
  if (props.progress.status === 'completed') return 'border-accent/30 bg-accent-soft'
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
          class="h-11 rounded-full bg-brand px-4 text-sm font-medium text-brand-ink transition hover:bg-brand-strong disabled:opacity-60"
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
        :class="props.progress.status === 'failed' ? 'bg-danger' : props.progress.status === 'completed' ? 'bg-accent' : 'bg-brand'"
        :style="{ width: `${percent}%` }"
      ></div>
    </div>

    <div v-if="props.progress.status === 'completed'" class="mt-4 rounded-xl bg-canvas px-3.5 py-2.5">
      <div class="flex flex-wrap items-center justify-between gap-2">
        <div>
          <p class="text-sm font-medium">Telegraph 发布</p>
          <p class="mt-0.5 text-xs text-muted">
            {{ publishSummary(props.progress) }} · 状态：{{ publishStateLabel(props.progress.publish_state) }}
          </p>
        </div>
        <button
          v-if="canPublish"
          type="button"
          class="h-11 rounded-full bg-brand px-4 text-sm font-medium text-brand-ink transition hover:bg-brand-strong disabled:opacity-60"
          :disabled="publishActive(props.progress) || props.publishing"
          @click="emit('publish')"
        >
          {{ props.publishing ? '正在提交…' : props.progress.publish_state === 'failed' ? '重新发布' : '发布到 Telegraph' }}
        </button>
      </div>
      <ul v-if="props.progress.telegraph_urls.length > 0" class="mt-2 space-y-1">
        <li v-for="url in props.progress.telegraph_urls" :key="url" class="text-xs">
          <a :href="url" target="_blank" rel="noreferrer noopener" class="text-brand-strong underline underline-offset-2">{{ url }}</a>
        </li>
      </ul>
    </div>

    <div
      class="mt-3 min-w-0 max-w-full overflow-x-auto overscroll-x-contain pb-2 focus:outline-none focus-visible:ring-2 focus-visible:ring-ink/30"
      tabindex="0"
      role="group"
      aria-label="图片明细，可左右滚动查看全部序号"
    >
      <ul
        class="grid grid-flow-col auto-cols-[minmax(8.5rem,max-content)] gap-x-4 gap-y-1 pr-1"
        :class="props.progress.images.length > 2 ? 'grid-rows-2' : 'grid-rows-1'"
      >
        <li
          v-for="image in props.progress.images"
          :key="image.sort_order"
          class="tnum flex items-center justify-between gap-2 whitespace-nowrap text-xs"
        >
          <span class="text-muted">#{{ image.sort_order }}</span>
          <span
            class="whitespace-nowrap"
            :class="image.status === 'failed' ? 'text-danger' : image.status === 'stored' ? 'text-ink' : 'text-muted'"
            :title="image.error ?? ''"
            >{{ imageStatusLabel(image.status) }}</span
          >
        </li>
      </ul>
    </div>
  </section>
</template>
