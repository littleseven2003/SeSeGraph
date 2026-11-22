<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue'
import type { GalleryItem } from '../../api/gallery.ts'
import { copyText } from '../../utils/clipboard.ts'
import { formatPublishedAt, postShareUrl, statusLabel } from '../../utils/gallery.ts'
import { useCoverReveal } from './useCoverReveal.ts'

const props = defineProps<{ item: GalleryItem; blurred: boolean }>()
const emit = defineEmits<{ open: [id: number] }>()

const { showBlur, activate, onCoverEnter, onCoverLeave } = useCoverReveal(() => props.blurred)

// 缩略图上的点击按鼠标离开计时回遮；行内「打开」只做短时揭开，不改变已有计时。
function onCoverOpen(event: MouseEvent): void {
  activate(() => emit('open', props.item.id), event, { fromCover: true })
}

function onRowOpen(event: MouseEvent): void {
  activate(() => emit('open', props.item.id), event)
}

const shareUrl = computed(() => postShareUrl(props.item))
const copyState = ref<'idle' | 'copied' | 'failed'>('idle')
let copyTimer: ReturnType<typeof setTimeout> | undefined

async function copyLink(): Promise<void> {
  if (shareUrl.value === '') return
  copyState.value = (await copyText(shareUrl.value)) ? 'copied' : 'failed'
  if (copyTimer !== undefined) clearTimeout(copyTimer)
  copyTimer = setTimeout(() => {
    copyState.value = 'idle'
  }, 1800)
}

onBeforeUnmount(() => {
  if (copyTimer !== undefined) clearTimeout(copyTimer)
})

const status = computed(() => statusLabel(props.item.status, props.item.publish_state, props.item.disabled))
const chipClass = computed(() => {
  switch (status.value.tone) {
    case 'idle':
    case 'progress':
      return 'border-warn/40 bg-warn-soft text-warn'
    case 'error':
      return 'border-danger/30 bg-white text-danger'
    default:
      return 'border-accent/30 bg-white text-accent'
  }
})
const published = computed(() => formatPublishedAt(props.item.published_at))
const publishedDate = computed(() => published.value.split(' ')[0] ?? '')
const volume = computed(() => '#' + String(props.item.id).padStart(2, '0'))
const copyLabel = computed(() => {
  if (copyState.value === 'copied') return '已复制链接'
  if (copyState.value === 'failed') return '复制失败'
  return '复制链接'
})
</script>

<template>
  <div class="flex items-center gap-3 py-3 sm:gap-4" :class="showBlur ? 'is-blurred' : ''">
    <button
      type="button"
      class="relative block h-12 w-16 shrink-0 overflow-hidden rounded-lg bg-canvas"
      :aria-label="`打开《${item.title}》详情`"
      data-reveal="cover"
      @click="onCoverOpen"
      @mouseenter="onCoverEnter"
      @mouseleave="onCoverLeave"
    >
      <img
        v-if="item.cover_url !== ''"
        :src="item.cover_url"
        :alt="item.title + ' 的封面'"
        class="h-full w-full object-cover"
        loading="lazy"
      />
      <span v-else class="flex h-full w-full items-center justify-center text-muted" aria-hidden="true">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" class="h-4 w-4">
          <rect x="3" y="4" width="18" height="16" rx="2" />
          <path d="M3 15l5-4 4 3 3-2 6 4" stroke-linecap="round" stroke-linejoin="round" />
        </svg>
      </span>
    </button>

    <div class="min-w-0 flex-1">
      <p class="truncate text-sm font-semibold">{{ item.title }}</p>
      <div class="mt-1 flex min-w-0 items-center gap-2 text-xs text-muted">
        <span class="tnum shrink-0">{{ volume }}</span>
        <span v-if="publishedDate !== ''" class="tnum shrink-0">{{ publishedDate }}</span>
        <span class="tnum shrink-0">{{ item.image_count }}P</span>
        <span
          class="inline-flex shrink-0 items-center rounded-full border px-1.5 py-0.5 text-[11px] font-medium leading-none sm:hidden"
          :class="chipClass"
          >{{ status.label }}</span
        >
        <span class="hidden min-w-0 flex-wrap items-center gap-1 sm:flex">
          <span v-for="tag in item.tags.slice(0, 2)" :key="tag" class="max-w-[8rem] truncate">#{{ tag }}</span>
        </span>
      </div>
    </div>

    <span
      class="hidden shrink-0 items-center gap-1.5 rounded-full border px-2 py-1 text-xs font-medium leading-none sm:inline-flex"
      :class="chipClass"
    >
      {{ status.label }}
    </span>

    <div class="flex shrink-0 items-center gap-1.5">
      <button
        v-if="shareUrl !== ''"
        type="button"
        class="inline-flex h-11 w-11 items-center justify-center rounded-full border border-brand/25 bg-brand-soft text-brand-strong transition hover:border-brand/45"
        :class="copyState === 'copied' ? 'border-accent/30 bg-accent-soft text-accent' : ''"
        :aria-label="copyLabel"
        :title="copyLabel"
        @click="copyLink"
      >
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" class="h-4 w-4" aria-hidden="true">
          <template v-if="copyState === 'idle'">
            <rect x="9" y="9" width="11" height="11" rx="2" />
            <path d="M15 5.5A2.5 2.5 0 0 0 12.5 3H6a2 2 0 0 0-2 2v7.5A2.5 2.5 0 0 0 6.5 15" stroke-linecap="round" />
          </template>
          <path v-else-if="copyState === 'copied'" d="M5 12.5l4.5 4.5L19 7" stroke-linecap="round" stroke-linejoin="round" />
          <template v-else>
            <path d="M12 8v5" stroke-linecap="round" />
            <circle cx="12" cy="16.5" r="0.9" fill="currentColor" stroke="none" />
          </template>
        </svg>
      </button>

      <button
        type="button"
        class="inline-flex h-11 items-center rounded-full bg-ink px-3.5 text-xs font-medium text-white transition hover:bg-ink/90"
        @click="onRowOpen"
      >
        打开
      </button>
    </div>
    <span role="status" aria-live="polite" class="sr-only">{{ copyState === 'idle' ? '' : copyLabel }}</span>
  </div>
</template>

<style scoped>
.is-blurred img {
  filter: blur(12px);
  transform: scale(1.08);
}
</style>
