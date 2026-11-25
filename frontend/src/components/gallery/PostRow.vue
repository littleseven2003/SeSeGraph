<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue'
import type { GalleryItem } from '../../api/gallery.ts'
import { copyText } from '../../utils/clipboard.ts'
import {
  formatPublishedAt,
  formatViews,
  formatViewsExact,
  postShareUrl,
  showsViews,
  statusLabel,
} from '../../utils/gallery.ts'
import { useCoverReveal } from './useCoverReveal.ts'

const props = defineProps<{ item: GalleryItem; blurred: boolean }>()
const emit = defineEmits<{ open: [id: number] }>()

const { showBlur, activate, onCoverLeave } = useCoverReveal(() => props.blurred)

// 封面图点击保留「首击揭罩、再击打开」；行内按钮与其它操作一律直接执行，不经过遮罩。
function onCoverOpen(event: MouseEvent): void {
  activate(() => emit('open', props.item.id), event, { fromCover: true })
}

function onRowOpen(): void {
  emit('open', props.item.id)
}

const showViews = computed(() => showsViews(props.item))
const viewsText = computed(() => formatViews(props.item.views_count))
const viewsTitle = computed(() => formatViewsExact(props.item.views_count) + ' 次浏览')

const shareUrl = computed(() => postShareUrl(props.item))
// 旧接口或演示数据可能没有说明字段，统一按空串处理，避免渲染出空行。
const excerpt = computed(() => props.item.description_excerpt ?? '')
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
  <div
    class="-mx-4 grid grid-cols-[auto_minmax(0,1fr)_auto] items-start gap-3 px-4 py-3.5 transition-colors hover:bg-canvas/60 md:grid-cols-[4.25rem_minmax(11rem,1.2fr)_minmax(9rem,1.4fr)_8.5rem_6.5rem] md:items-center md:gap-4 lg:grid-cols-[5rem_minmax(15rem,1.15fr)_minmax(16rem,1.5fr)_9rem_6.75rem]"
    :class="showBlur ? 'is-blurred' : ''"
  >
    <button
      type="button"
      class="relative block h-12 w-16 shrink-0 overflow-hidden rounded-lg bg-canvas md:h-14 md:w-20"
      :aria-label="`打开《${item.title}》详情`"
      data-reveal="cover"
      @click="onCoverOpen"
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

    <div class="min-w-0">
      <p class="truncate text-sm font-semibold" :title="item.title">{{ item.title }}</p>

      <!-- 窄屏只保留描述、浏览量与状态；卷号、日期与张数收进详情抽屉，避免挤压操作区。 -->
      <p
        v-if="excerpt !== ''"
        class="mt-0.5 line-clamp-2 break-words text-xs leading-snug text-muted md:hidden"
        :title="excerpt"
      >
        {{ excerpt }}
      </p>
      <div class="mt-1.5 flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted">
        <span class="tnum hidden shrink-0 md:inline">{{ volume }}</span>
        <span v-if="publishedDate !== ''" class="tnum shrink-0">{{ publishedDate }}</span>
        <span class="tnum hidden shrink-0 md:inline">{{ item.image_count }}P</span>
        <span v-if="showViews" class="inline-flex shrink-0 items-center gap-1 tnum" :title="viewsTitle">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" class="h-3.5 w-3.5" aria-hidden="true">
            <path d="M2.5 12S6 5.5 12 5.5 21.5 12 21.5 12 18 18.5 12 18.5 2.5 12 2.5 12Z" stroke-linecap="round" stroke-linejoin="round" />
            <circle cx="12" cy="12" r="3" />
          </svg>
          {{ viewsText }}
        </span>
        <span
          class="inline-flex shrink-0 items-center whitespace-nowrap rounded-full border px-1.5 py-0.5 text-[11px] font-medium leading-none md:hidden"
          :class="chipClass"
          >{{ status.label }}</span
        >
        <!-- 窄屏保留一个标签，桌面端展示两个。 -->
        <span v-if="item.tags.length > 0" class="max-w-[6rem] truncate md:hidden">#{{ item.tags[0] }}</span>
        <span class="hidden min-w-0 flex-wrap items-center gap-1 md:flex">
          <span v-for="tag in item.tags.slice(0, 2)" :key="tag" class="max-w-[8rem] truncate">#{{ tag }}</span>
        </span>
      </div>
    </div>

    <!-- 桌面端独立描述列：最多两行，空描述用极轻占位符收住列宽。 -->
    <p
      v-if="excerpt !== ''"
      class="hidden line-clamp-2 break-words text-xs leading-[1.6] text-muted md:line-clamp-2"
      :title="excerpt"
    >
      {{ excerpt }}
    </p>
    <p v-else class="hidden select-none text-xs text-muted/45 md:block" aria-hidden="true">—</p>

    <span
      class="hidden shrink-0 items-center gap-1.5 justify-self-start whitespace-nowrap rounded-full border px-2 py-1 text-xs font-medium leading-none md:inline-flex"
      :class="chipClass"
    >
      {{ status.label }}
    </span>

    <div class="flex shrink-0 items-center justify-end gap-1.5">
      <button
        v-if="shareUrl !== ''"
        type="button"
        class="inline-flex h-11 w-11 items-center justify-center rounded-lg border border-brand/25 bg-brand-soft text-brand-strong transition hover:border-brand/45"
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
        class="inline-flex h-11 items-center rounded-xl bg-ink px-3.5 text-xs font-medium text-white transition hover:bg-ink/90"
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
