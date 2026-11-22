<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue'
import type { GalleryItem } from '../../api/gallery.ts'
import { TOUCH_REVEAL_MS, formatPublishedAt, shouldRevealOnTap, shouldShowBlur, statusLabel } from '../../utils/gallery.ts'

const props = defineProps<{
  item: GalleryItem
  blurred: boolean
}>()

const emit = defineEmits<{ open: [id: number] }>()

// 触屏没有悬停：第一次点按只临时去模糊，再点一次才打开详情。
const revealed = ref(false)
let revealTimer: ReturnType<typeof setTimeout> | undefined

function touchOnly(): boolean {
  if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') return false
  return window.matchMedia('(hover: none)').matches
}

const showBlur = computed(() => shouldShowBlur(props.blurred, revealed.value))

function onOpen(): void {
  if (shouldRevealOnTap({ blurred: props.blurred, touch: touchOnly(), revealed: revealed.value })) {
    revealed.value = true
    if (revealTimer !== undefined) clearTimeout(revealTimer)
    revealTimer = setTimeout(() => {
      revealed.value = false
    }, TOUCH_REVEAL_MS)
    return
  }
  emit('open', props.item.id)
}

onBeforeUnmount(() => {
  if (revealTimer !== undefined) clearTimeout(revealTimer)
})

const status = computed(() => statusLabel(props.item.status, props.item.publish_state, props.item.disabled))
const published = computed(() => formatPublishedAt(props.item.published_at))
const visibleTags = computed(() => props.item.tags.slice(0, 3))
const restTags = computed(() => Math.max(0, props.item.tags.length - visibleTags.value.length))

const toneClass = computed(() => {
  switch (status.value.tone) {
    case 'ok':
      return 'bg-accent/10 text-accent'
    case 'progress':
      return 'bg-brand/10 text-brand'
    case 'error':
      return 'bg-danger-soft text-danger'
    default:
      return 'bg-canvas text-muted'
  }
})
</script>

<template>
  <button
    type="button"
    class="group flex w-full flex-col overflow-hidden rounded-lg border border-line bg-surface text-left transition hover:border-brand/40 hover:shadow-[0_8px_24px_-16px_rgba(15,23,42,0.45)] focus-visible:border-brand"
    :class="showBlur ? 'is-blurred' : ''"
    @click="onOpen"
  >
    <span class="relative block aspect-[4/3] w-full overflow-hidden bg-canvas">
      <img
        v-if="item.cover_url !== ''"
        :src="item.cover_url"
        :alt="item.title + ' 的封面'"
        class="h-full w-full object-cover transition duration-200 group-hover:scale-[1.02]"
        loading="lazy"
      />
      <span v-else class="flex h-full w-full items-center justify-center text-muted" aria-hidden="true">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" class="h-9 w-9">
          <rect x="3" y="4" width="18" height="16" rx="2" />
          <path d="M3 15l5-4 4 3 3-2 6 4" stroke-linecap="round" stroke-linejoin="round" />
          <circle cx="9" cy="9" r="1.5" />
        </svg>
      </span>
      <span
        class="absolute left-2 top-2 rounded px-1.5 py-0.5 text-xs font-medium"
        :class="toneClass"
        >{{ status.label }}</span
      >
      <span
        class="absolute right-2 top-2 rounded bg-ink/70 px-1.5 py-0.5 text-xs font-medium text-white tnum"
        >{{ item.image_count }} 张</span
      >
    </span>

    <span class="flex flex-1 flex-col gap-2 px-3 py-3">
      <span class="line-clamp-2 text-sm font-semibold leading-snug">{{ item.title }}</span>
      <span v-if="visibleTags.length > 0" class="flex flex-wrap gap-1">
        <span
          v-for="tag in visibleTags"
          :key="tag"
          class="rounded bg-canvas px-1.5 py-0.5 text-xs text-muted"
          >{{ tag }}</span
        >
        <span v-if="restTags > 0" class="px-1 py-0.5 text-xs text-muted">+{{ restTags }}</span>
      </span>
      <span class="mt-auto flex items-center justify-between gap-2 text-xs text-muted">
        <span class="truncate">{{ published !== '' ? '发布时间' : '创建后待发布' }}</span>
        <span class="tnum">{{ published !== '' ? published : '未发布' }}</span>
      </span>
    </span>
  </button>
</template>

<style scoped>
.is-blurred img {
  filter: blur(16px);
  transform: scale(1.06);
}

/* 悬停去模糊只对支持悬停的设备生效，避免触屏保留点按后的悬停状态。 */
@media (hover: hover) {
  .is-blurred:hover img {
    filter: none;
    transform: none;
  }
}

/* 键盘去模糊绑定卡片按钮自身的可见焦点。 */
.is-blurred:focus-visible img {
  filter: none;
  transform: none;
}

@media (prefers-reduced-motion: reduce) {
  .is-blurred img {
    transition: none;
  }
}
</style>
