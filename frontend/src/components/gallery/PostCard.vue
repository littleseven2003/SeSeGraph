<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue'
import type { GalleryItem } from '../../api/gallery.ts'
import { copyText } from '../../utils/clipboard.ts'
import {
  TOUCH_REVEAL_MS,
  formatPublishedAt,
  postShareUrl,
  shouldRevealOnTap,
  shouldShowBlur,
  statusLabel,
} from '../../utils/gallery.ts'

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

// 分享链接只在已发布、公开且未撤回时可用；列表数据已带地址，无需额外请求。
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
  if (revealTimer !== undefined) clearTimeout(revealTimer)
  if (copyTimer !== undefined) clearTimeout(copyTimer)
})

const status = computed(() => statusLabel(props.item.status, props.item.publish_state, props.item.disabled))
// chipClass 按状态分层：完成走墨色胶囊，进行中走暖琥珀，异常走柔红，保证亮封面上也读得清。
const chipClass = computed(() => {
  switch (status.value.tone) {
    case 'idle':
      return 'border-warn/40 bg-warn-soft text-warn'
    case 'progress':
      return 'border-warn/40 bg-warn-soft text-warn'
    case 'error':
      return 'border-danger/30 bg-white text-danger'
    default:
      return 'border-accent/30 bg-white text-accent'
  }
})
const published = computed(() => formatPublishedAt(props.item.published_at))
// 卡片只显示日期，时间留在详情里，避免两列布局下折行。
const publishedDate = computed(() => published.value.split(' ')[0] ?? '')
const visibleTags = computed(() => props.item.tags.slice(0, 2))
const restTags = computed(() => Math.max(0, props.item.tags.length - visibleTags.value.length))
// volume 是相纸角上的卷号，用帖子编号补两位，形成归档索引感。
const volume = computed(() => '#' + String(props.item.id).padStart(2, '0'))
const copyLabel = computed(() => {
  if (copyState.value === 'copied') return '已复制'
  if (copyState.value === 'failed') return '复制失败'
  return '复制链接'
})
</script>

<template>
  <article
    class="polaroid group flex flex-col rounded-md border border-line bg-surface p-2 pb-0 transition duration-200 motion-safe:hover:-translate-y-0.5 hover:border-brand/30 hover:shadow-[0_10px_24px_-12px_rgba(208,66,35,0.18)]"
    :class="showBlur ? 'is-blurred' : ''"
  >
    <button
      type="button"
      class="relative block aspect-[4/3] w-full overflow-hidden rounded-[4px] bg-canvas"
      :aria-label="`打开《${item.title}》详情`"
      data-reveal="cover"
      @click="onOpen"
    >
      <img
        v-if="item.cover_url !== ''"
        :src="item.cover_url"
        :alt="item.title + ' 的封面'"
        class="h-full w-full object-cover transition duration-200 motion-safe:group-hover:scale-[1.02]"
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
        class="absolute left-2 top-2 inline-flex -rotate-2 items-center gap-1.5 rounded border px-2 py-1 text-xs font-medium leading-none shadow-[0_1px_0_0_rgba(27,26,23,0.12)]"
        :class="chipClass"
      >
        <span v-if="status.tone === 'ok'" class="h-1.5 w-1.5 rounded-full bg-[#34d399]" aria-hidden="true"></span>
        {{ status.label }}
      </span>
      <span
        class="absolute bottom-2 right-2 rounded bg-white/85 px-1.5 py-0.5 text-xs font-medium leading-none text-ink tnum"
        >{{ item.image_count }}P</span
      >
    </button>

    <div class="flex flex-1 flex-col gap-2 px-1.5 pb-3.5 pt-2.5">
      <p class="line-clamp-2 text-sm font-semibold leading-snug">
        {{ item.title }}
      </p>

      <div v-if="visibleTags.length > 0" class="flex flex-wrap gap-1">
        <span
          v-for="tag in visibleTags"
          :key="tag"
          class="rounded-full bg-canvas px-1.5 py-0.5 text-xs leading-relaxed text-muted"
          >#{{ tag }}</span
        >
        <span v-if="restTags > 0" class="px-1 py-0.5 text-xs leading-relaxed text-muted">+{{ restTags }}</span>
      </div>

      <p class="mt-auto truncate text-xs text-muted tnum">
        {{ publishedDate !== '' ? volume + ' · ' + publishedDate : volume + ' · 待冲印' }}
      </p>

      <div class="flex items-center gap-2">
        <button
          type="button"
          class="h-11 flex-1 rounded-md border border-line text-xs font-medium transition hover:bg-canvas sm:text-sm"
          @click="onOpen"
        >
          打开
        </button>
        <button
          v-if="shareUrl !== ''"
          type="button"
          class="inline-flex h-11 flex-1 items-center justify-center gap-1.5 whitespace-nowrap rounded-md border px-1 text-xs font-medium transition motion-safe:active:scale-[0.98] sm:px-2 sm:text-sm"
          :class="
            copyState === 'idle'
              ? 'border-brand/25 bg-brand-soft text-brand-strong hover:border-brand/45 hover:bg-brand-soft/70'
              : 'border-accent/30 bg-accent-soft text-accent'
          "
          @click="copyLink"
        >
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" class="hidden h-4 w-4 shrink-0 sm:block" aria-hidden="true">
            <template v-if="copyState === 'idle'">
              <rect x="9" y="9" width="11" height="11" rx="2" />
              <path d="M15 5.5A2.5 2.5 0 0 0 12.5 3H6a2 2 0 0 0-2 2v7.5A2.5 2.5 0 0 0 6.5 15" stroke-linecap="round" />
            </template>
            <path v-else d="M5 12.5l4.5 4.5L19 7" stroke-linecap="round" stroke-linejoin="round" />
          </svg>
          {{ copyLabel }}
        </button>
      </div>
    </div>
  </article>
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

/* 键盘去模糊只认焦点可见状态：鼠标点按会聚焦按钮，用 focus-within 会让模糊失效。 */
.is-blurred:has([data-reveal='cover']:focus-visible) img {
  filter: none;
  transform: none;
}

@media (prefers-reduced-motion: reduce) {
  .is-blurred img {
    transition: none;
  }
}
</style>
