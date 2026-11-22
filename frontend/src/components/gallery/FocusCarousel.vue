<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import type { GalleryItem } from '../../api/gallery.ts'
import { copyText } from '../../utils/clipboard.ts'
import { formatPublishedAt, postShareUrl, statusLabel, stepIndex } from '../../utils/gallery.ts'
import { useCoverReveal } from './useCoverReveal.ts'

// 最新焦点：左侧大图 + 右侧标题索引，默认自动轮播，悬停、聚焦、解锁或减少动效时暂停。
const props = defineProps<{ items: GalleryItem[]; blurred: boolean }>()
const emit = defineEmits<{ open: [id: number] }>()

const AUTO_MS = 5500
const index = ref(0)
// 三种暂停原因各自独立：手动按钮、悬停、区内焦点，再加上封面已解锁。
const manualPaused = ref(false)
const hoverPaused = ref(false)
const focusPaused = ref(false)
const paused = computed(() => manualPaused.value || hoverPaused.value || focusPaused.value)
let timer: ReturnType<typeof setInterval> | undefined

const current = computed<GalleryItem | null>(() => props.items[index.value] ?? null)
const position = computed(() => String(index.value + 1).padStart(2, '0'))
const total = computed(() => String(props.items.length).padStart(2, '0'))

const { showBlur, revealed, activate, remask, onCoverEnter, onCoverLeave } = useCoverReveal(() => props.blurred)

const status = computed(() =>
  current.value === null ? null : statusLabel(current.value.status, current.value.publish_state, current.value.disabled),
)
const publishedDate = computed(() => formatPublishedAt(current.value?.published_at ?? '').split(' ')[0] ?? '')

const copiedId = ref<number | null>(null)
let copyTimer: ReturnType<typeof setTimeout> | undefined

async function copyItem(item: GalleryItem): Promise<void> {
  const url = postShareUrl(item)
  if (url === '') return
  const ok = await copyText(url)
  copiedId.value = ok ? item.id : null
  if (copyTimer !== undefined) clearTimeout(copyTimer)
  copyTimer = setTimeout(() => {
    copiedId.value = null
  }, 1800)
}

function formatDate(value: string | undefined): string {
  return value === undefined ? '' : formatPublishedAt(value).split(' ')[0] ?? ''
}

function statusOf(item: GalleryItem) {
  return statusLabel(item.status, item.publish_state, item.disabled)
}

// entries 预先算好日期与状态，模板里不再重复格式化，缺少日期时只显示状态。
const entries = computed(() =>
  props.items.map((item) => {
    const status = statusOf(item)
    return {
      item,
      date: formatDate(item.published_at),
      label: status.label,
      tone: status.tone,
      shareUrl: postShareUrl(item),
    }
  }),
)

function select(target: number): void {
  if (props.items.length === 0) return
  index.value = ((target % props.items.length) + props.items.length) % props.items.length
}

function step(delta: number): void {
  select(stepIndex(index.value, props.items.length, delta))
}

function onCover(): void {
  const item = current.value
  if (item === null) return
  activate(() => emit('open', item.id))
}

const reducedMotion = ref(false)
let mediaQuery: MediaQueryList | undefined

// 轮播只在没有暂停原因时前进；解锁当前大图也算用户操作，自动停下。
function startTimer(): void {
  stopTimer()
  if (props.items.length < 2 || reducedMotion.value) return
  timer = setInterval(() => {
    if (paused.value || revealed.value) return
    step(1)
  }, AUTO_MS)
}

// 区内焦点：只有焦点真正离开轮播区才解除暂停。
function onFocusOut(event: FocusEvent): void {
  const next = event.relatedTarget as Node | null
  const root = event.currentTarget as HTMLElement | null
  if (root !== null && next !== null && root.contains(next)) return
  focusPaused.value = false
}

function stopTimer(): void {
  if (timer !== undefined) {
    clearInterval(timer)
    timer = undefined
  }
}

watch([index], () => {
  remask()
})

watch([reducedMotion, () => props.items.length], () => {
  if (reducedMotion.value) stopTimer()
  else startTimer()
})

function onMedia(event: MediaQueryListEvent): void {
  reducedMotion.value = event.matches
}

onMounted(() => {
  mediaQuery = window.matchMedia('(prefers-reduced-motion: reduce)')
  reducedMotion.value = mediaQuery.matches
  mediaQuery.addEventListener('change', onMedia)
  startTimer()
})

onBeforeUnmount(() => {
  stopTimer()
  mediaQuery?.removeEventListener('change', onMedia)
  if (copyTimer !== undefined) clearTimeout(copyTimer)
})
</script>

<template>
  <section
    v-if="current !== null"
    class="rounded-2xl bg-surface p-4 shadow-[0_1px_0_0_rgba(27,26,23,0.05),0_24px_60px_-46px_rgba(27,26,23,0.4)] lg:p-5"
    aria-roledescription="carousel"
    aria-label="最新图集"
    @mouseenter="hoverPaused = true"
    @mouseleave="hoverPaused = false"
    @focusin="focusPaused = true"
    @focusout="onFocusOut"
    @keydown.left.prevent="step(-1)"
    @keydown.right.prevent="step(1)"
  >
    <div class="grid gap-4 lg:grid-cols-[minmax(0,1.32fr)_minmax(0,1fr)] lg:items-stretch lg:gap-6">
      <div class="min-w-0" :class="showBlur ? 'is-blurred' : ''">
        <div class="relative">
          <button
            type="button"
            class="relative block aspect-[16/10] w-full overflow-hidden rounded-2xl bg-canvas lg:aspect-auto lg:h-[360px]"
            :aria-label="`打开《${current.title}》详情`"
            data-reveal="cover"
            @click="onCover"
            @mouseenter="onCoverEnter"
            @mouseleave="onCoverLeave"
          >
            <img
              v-if="current.cover_url !== ''"
              :key="current.id"
              :src="current.cover_url"
              :alt="current.title + ' 的封面'"
              class="focus-fade h-full w-full object-cover"
            />
            <span v-else class="flex h-full w-full items-center justify-center text-muted" aria-hidden="true">
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" class="h-10 w-10">
                <rect x="3" y="4" width="18" height="16" rx="2" />
                <path d="M3 15l5-4 4 3 3-2 6 4" stroke-linecap="round" stroke-linejoin="round" />
              </svg>
            </span>

            <span class="absolute inset-0 bg-ink/5" aria-hidden="true"></span>

            <span
              v-if="status !== null"
              class="absolute left-3 top-3 inline-flex items-center gap-1.5 rounded-full border px-2.5 py-1 text-xs font-medium leading-none"
              :class="
                status.tone === 'error'
                  ? 'border-danger/30 bg-white text-danger'
                  : status.tone === 'ok'
                    ? 'border-accent/30 bg-white text-accent'
                    : 'border-warn/40 bg-warn-soft text-warn'
              "
            >
              {{ status.label }}
            </span>
            <span
              class="absolute bottom-3 right-3 rounded-full bg-white/90 px-2 py-1 text-xs font-medium leading-none text-ink tnum"
              >{{ current.image_count }}P</span
            >

            <span v-if="showBlur" class="pointer-events-none absolute inset-x-0 bottom-0 flex justify-center pb-3" aria-hidden="true">
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
            type="button"
            class="absolute right-3 top-3 inline-flex h-8 items-center rounded-full bg-white/85 px-2.5 text-xs font-medium leading-none text-ink backdrop-blur transition hover:bg-white"
            :aria-label="manualPaused ? '继续自动切换' : '暂停自动切换'"
            :aria-pressed="manualPaused"
            @click="manualPaused = !manualPaused"
          >
            {{ manualPaused ? '继续' : '暂停' }}
          </button>

          <button
            v-if="!showBlur && props.blurred"
            type="button"
            class="absolute bottom-3 left-3 inline-flex h-9 items-center gap-1 rounded-full bg-white/90 px-2.5 text-xs font-medium leading-none text-ink transition hover:bg-white"
            @click="remask"
          >
            重新遮罩
          </button>
        </div>
      </div>

      <div class="flex min-w-0 flex-col">
        <ul class="flex h-full flex-1 flex-col divide-y divide-line/60">
          <li
            v-for="(entry, itemIndex) in entries"
            :key="entry.item.id"
            class="flex min-h-0 flex-1 items-center"
            @mouseenter="select(itemIndex)"
          >
            <div class="relative flex w-full items-center gap-1.5 rounded-xl px-2 py-1.5" :class="itemIndex === index ? 'bg-brand-soft' : ''">
              <span
                v-if="itemIndex === index"
                class="absolute left-0 top-1/2 h-6 w-[3px] -translate-y-1/2 rounded-full bg-brand"
                aria-hidden="true"
              ></span>
              <button
                type="button"
                class="flex min-w-0 flex-1 flex-col items-start gap-0.5 px-1 text-left"
                :aria-label="`查看第 ${itemIndex + 1} 组：${entry.item.title}`"
                :aria-current="itemIndex === index ? 'true' : undefined"
                @click="select(itemIndex)"
                @focus="select(itemIndex)"
              >
                <span class="block max-w-full truncate text-sm" :class="itemIndex === index ? 'font-semibold text-ink' : 'text-ink/80'">{{
                  entry.item.title
                }}</span>
                <span class="flex items-center gap-2 text-xs text-muted">
                  <span v-if="entry.date !== ''" class="tnum">{{ entry.date }}</span>
                  <span class="truncate">{{ entry.label }}</span>
                </span>
              </button>

              <button
                type="button"
                class="inline-flex h-11 w-11 shrink-0 items-center justify-center rounded-full"
                :aria-label="`打开《${entry.item.title}》`"
                :title="`打开《${entry.item.title}》`"
                @click="emit('open', entry.item.id)"
              >
                <span class="inline-flex h-8 w-8 items-center justify-center rounded-full border border-line bg-surface text-ink transition hover:border-ink/30 hover:bg-ink hover:text-white">
                  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" class="h-4 w-4" aria-hidden="true">
                    <path d="M7 17 17 7M9 7h8v8" stroke-linecap="round" stroke-linejoin="round" />
                  </svg>
                </span>
              </button>

              <button
                v-if="entry.shareUrl !== ''"
                type="button"
                class="inline-flex h-11 w-11 shrink-0 items-center justify-center rounded-full"
                :aria-label="copiedId === entry.item.id ? `已复制《${entry.item.title}》链接` : `复制《${entry.item.title}》链接`"
                :title="copiedId === entry.item.id ? '已复制链接' : '复制链接'"
                @click="copyItem(entry.item)"
              >
                <span
                  class="inline-flex h-8 w-8 items-center justify-center rounded-full border transition"
                  :class="
                    copiedId === entry.item.id
                      ? 'border-accent/30 bg-accent-soft text-accent'
                      : 'border-brand/25 bg-brand-soft text-brand-strong'
                  "
                >
                  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" class="h-4 w-4" aria-hidden="true">
                    <template v-if="copiedId === entry.item.id">
                      <path d="M5 12.5l4.5 4.5L19 7" stroke-linecap="round" stroke-linejoin="round" />
                    </template>
                    <template v-else>
                      <rect x="9" y="9" width="11" height="11" rx="2" />
                      <path d="M15 5.5A2.5 2.5 0 0 0 12.5 3H6a2 2 0 0 0-2 2v7.5A2.5 2.5 0 0 0 6.5 15" stroke-linecap="round" />
                    </template>
                  </svg>
                </span>
              </button>
            </div>
          </li>
        </ul>
      </div>
    </div>
  </section>
</template>

<style scoped>
.is-blurred img {
  filter: blur(18px);
  transform: scale(1.06);
}
</style>
