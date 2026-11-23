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

const { showBlur, activate, remask, onCoverLeave } = useCoverReveal(() => props.blurred)

function onOpen(event: MouseEvent): void {
  activate(() => emit('open', props.item.id), event, { fromCover: true })
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
  if (copyTimer !== undefined) clearTimeout(copyTimer)
})

const status = computed(() => statusLabel(props.item.status, props.item.publish_state, props.item.disabled))
// chipClass 按状态分层：待发布走暖琥珀，异常走柔红，其余走松石青实体白底。
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
// 卡片只显示日期，时间留在详情里，避免两列布局下折行。
const publishedDate = computed(() => published.value.split(' ')[0] ?? '')
const visibleTags = computed(() => props.item.tags.slice(0, 2))
const restTags = computed(() => Math.max(0, props.item.tags.length - visibleTags.value.length))
// volume 是卡片上的卷号，用帖子编号补两位，形成归档索引感。
const volume = computed(() => '#' + String(props.item.id).padStart(2, '0'))
// 没有发布日期时只显示卷号，发布状态由左上角状态签负责，避免同一条信息出现两次。
// 有日期时窄屏隐藏卷号前缀，把宽度留给日期与浏览量，避免日期被截成「2…」。
const showViews = computed(() => showsViews(props.item))
const viewsText = computed(() => formatViews(props.item.views_count))
const viewsTitle = computed(() => formatViewsExact(props.item.views_count) + ' 次浏览')
const copyLabel = computed(() => {
  if (copyState.value === 'copied') return '已复制链接'
  if (copyState.value === 'failed') return '复制失败，请手动选取链接'
  return '复制链接'
})
</script>

<template>
  <article class="group flex flex-col" :class="showBlur ? 'is-blurred' : ''">
    <div class="relative">
    <button
      type="button"
      class="relative block aspect-[4/3] w-full overflow-hidden rounded-xl bg-canvas"
      :aria-label="`打开《${item.title}》详情`"
      data-reveal="cover"
      @click="onOpen"
      @mouseleave="onCoverLeave"
    >
      <img
        v-if="item.cover_url !== ''"
        :src="item.cover_url"
        :alt="item.title + ' 的封面'"
        class="h-full w-full object-cover transition-[transform] duration-300 motion-safe:group-hover:scale-[1.03]"
        loading="lazy"
      />
      <span v-else class="flex h-full w-full items-center justify-center text-muted" aria-hidden="true">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" class="h-9 w-9">
          <rect x="3" y="4" width="18" height="16" rx="2" />
          <path d="M3 15l5-4 4 3 3-2 6 4" stroke-linecap="round" stroke-linejoin="round" />
          <circle cx="9" cy="9" r="1.5" />
        </svg>
      </span>

      <span class="absolute inset-0 bg-ink/5" aria-hidden="true"></span>

      <span
        class="absolute left-2 top-2 inline-flex items-center gap-1.5 whitespace-nowrap rounded-full border px-2 py-1 text-xs font-medium leading-none"
        :class="chipClass"
      >
        <span v-if="status.tone === 'ok'" class="h-1.5 w-1.5 rounded-full bg-[#34d399]" aria-hidden="true"></span>
        {{ status.label }}
      </span>
      <span
        class="absolute bottom-2 right-2 rounded-full bg-white/90 px-2 py-1 text-xs font-medium leading-none text-ink tnum"
        >{{ item.image_count }}P</span
      >

      <span
        v-if="showBlur"
        class="pointer-events-none absolute inset-x-0 bottom-0 flex items-center justify-center pb-3"
        aria-hidden="true"
      >
        <span class="inline-flex items-center gap-1.5 rounded-full bg-ink/80 px-3 py-1.5 text-xs font-medium leading-none text-white">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" class="h-3.5 w-3.5">
            <path d="M2.5 12S6 5.5 12 5.5 21.5 12 21.5 12 18 18.5 12 18.5 2.5 12 2.5 12Z" stroke-linecap="round" stroke-linejoin="round" />
            <circle cx="12" cy="12" r="3" />
          </svg>
          点击查看
        </span>
      </span>

    </button>

    <button
      v-if="!showBlur && props.blurred"
      type="button"
      class="absolute bottom-2 left-2 inline-flex h-9 items-center gap-1 rounded-full bg-white/90 px-2.5 text-xs font-medium leading-none text-ink transition hover:bg-white"
      @click="remask"
    >
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" class="h-3.5 w-3.5" aria-hidden="true">
        <path d="M3 12s3.5-6.5 9-6.5S21 12 21 12s-3.5 6.5-9 6.5S3 12 3 12Z" stroke-linecap="round" stroke-linejoin="round" />
        <path d="M4 20L20 4" stroke-linecap="round" />
      </svg>
      重新遮罩
    </button>
    </div>

    <div class="mt-2 flex flex-col gap-0.5">
      <button
        type="button"
        class="line-clamp-2 w-full break-words text-left text-[13px] font-semibold leading-snug underline-offset-4 transition hover:text-brand-strong hover:underline focus-visible:underline [overflow-wrap:anywhere]"
        @click="emit('open', item.id)"
      >
        {{ item.title }}
      </button>

      <div class="flex items-center gap-1">
        <p class="min-w-0 flex-1 truncate text-xs text-muted tnum">
          <template v-if="publishedDate !== ''">
            <span class="hidden sm:inline">{{ volume }} · </span>{{ publishedDate }}
          </template>
          <template v-else>{{ volume }}</template>
        </p>
        <span
          v-if="showViews"
          class="inline-flex shrink-0 items-center gap-1 text-xs text-muted tnum"
          :title="viewsTitle"
        >
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" class="h-3 w-3" aria-hidden="true">
            <path d="M2.5 12S6 5.5 12 5.5 21.5 12 21.5 12 18 18.5 12 18.5 2.5 12 2.5 12Z" stroke-linecap="round" stroke-linejoin="round" />
            <circle cx="12" cy="12" r="3" />
          </svg>
          {{ viewsText }}
        </span>
        <button
          v-if="shareUrl !== ''"
          type="button"
          class="-my-1.5 inline-flex h-11 w-11 shrink-0 items-center justify-center rounded-full"
          :aria-label="copyLabel"
          :title="copyLabel"
          @click="copyLink"
        >
          <span
            class="inline-flex h-8 w-8 items-center justify-center rounded-full border transition motion-safe:group-hover:scale-[1.04]"
            :class="
              copyState === 'idle'
                ? 'border-brand/25 bg-brand-soft text-brand-strong'
                : copyState === 'copied'
                  ? 'border-accent/30 bg-accent-soft text-accent'
                  : 'border-danger/30 bg-white text-danger'
            "
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
          </span>
        </button>
        <span role="status" aria-live="polite" class="sr-only">{{ copyState === 'idle' ? '' : copyLabel }}</span>
      </div>

      <div v-if="visibleTags.length > 0" class="flex min-w-0 flex-wrap items-center gap-1">
        <span
          v-for="tag in visibleTags"
          :key="tag"
          class="min-w-0 max-w-full break-words rounded-full bg-canvas px-1.5 py-0.5 text-xs leading-relaxed text-muted [overflow-wrap:anywhere]"
          >#{{ tag }}</span
        >
        <span v-if="restTags > 0" class="px-1 py-0.5 text-xs leading-relaxed text-muted">+{{ restTags }}</span>
      </div>
    </div>
  </article>
</template>

<style scoped>
.is-blurred img {
  filter: blur(18px);
  transform: scale(1.06);
}

/* 悬停与键盘聚焦都不揭开，只有显式点按一次才去模糊。 */
@media (prefers-reduced-motion: reduce) {
  .is-blurred img {
    transition: none;
  }
}
</style>
