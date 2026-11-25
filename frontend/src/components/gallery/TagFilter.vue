<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import type { TagCount } from '../../api/gallery.ts'

// 标签筛选条：已选置顶、高频标签常显，其余收进「全部标签」浮层，避免上百个标签撑爆页面。
const props = defineProps<{ tags: TagCount[]; selected: string[] }>()
const emit = defineEmits<{
  (event: 'toggle', name: string): void
  (event: 'clear'): void
}>()

const open = ref(false)
const query = ref('')
const panel = ref<HTMLElement | null>(null)
const trigger = ref<HTMLButtonElement | null>(null)
const searchInput = ref<HTMLInputElement | null>(null)

// 展示上限随视口变化：桌面 8 个、窄屏 4 个，超出部分一律走「全部标签」入口。
const WIDE_LIMIT = 8
const NARROW_LIMIT = 1
const wide = ref(true)
let mediaQuery: MediaQueryList | undefined

function onMedia(event: MediaQueryListEvent): void {
  wide.value = event.matches
}

onMounted(() => {
  mediaQuery = window.matchMedia('(min-width: 640px)')
  wide.value = mediaQuery.matches
  mediaQuery.addEventListener('change', onMedia)
})

const selectedTags = computed(() => props.selected)
const restTags = computed(() => props.tags.filter((tag) => !props.selected.includes(tag.name)))
const visibleLimit = computed(() => (wide.value ? WIDE_LIMIT : NARROW_LIMIT))
const visibleTags = computed(() => restTags.value.slice(0, visibleLimit.value))
const panelTags = computed(() => {
  const keyword = query.value.trim().toLowerCase()
  if (keyword === '') return props.tags
  return props.tags.filter((tag) => tag.name.toLowerCase().includes(keyword))
})

function toggle(name: string): void {
  emit('toggle', name)
}

function clearAll(): void {
  emit('clear')
}

async function openPanel(): Promise<void> {
  open.value = true
  query.value = ''
  await nextTick()
  searchInput.value?.focus()
}

function closePanel(): void {
  open.value = false
  query.value = ''
  trigger.value?.focus()
}

function onDocumentPointer(event: PointerEvent): void {
  if (!open.value) return
  const target = event.target as Node | null
  if (target === null) return
  if (panel.value?.contains(target) || trigger.value?.contains(target)) return
  closePanel()
}

function onDocumentKey(event: KeyboardEvent): void {
  if (open.value && event.key === 'Escape') {
    event.preventDefault()
    closePanel()
  }
}

watch(open, (value) => {
  if (value) {
    document.addEventListener('pointerdown', onDocumentPointer)
    document.addEventListener('keydown', onDocumentKey)
  } else {
    document.removeEventListener('pointerdown', onDocumentPointer)
    document.removeEventListener('keydown', onDocumentKey)
  }
})

onBeforeUnmount(() => {
  document.removeEventListener('pointerdown', onDocumentPointer)
  document.removeEventListener('keydown', onDocumentKey)
  mediaQuery?.removeEventListener('change', onMedia)
})
</script>

<template>
  <div v-if="props.tags.length > 0" class="relative min-w-0">
    <div class="flex min-w-0 items-center gap-2 pb-1">
      <div class="relative min-w-0 flex-1">
      <div class="flex min-w-0 flex-nowrap items-center gap-2 overflow-x-auto">
        <span class="hidden shrink-0 text-xs text-muted sm:inline">标签</span>

      <button
        v-for="name in selectedTags"
        :key="'on-' + name"
        type="button"
        class="inline-flex h-11 min-w-0 max-w-full shrink-0 items-center gap-1.5 rounded-full border border-accent bg-accent px-3.5 text-xs font-medium text-white transition motion-safe:active:scale-[0.97] [overflow-wrap:anywhere]"
        :aria-label="'移除标签 ' + name"
        @click="toggle(name)"
      >
        #{{ name }}
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" class="h-3 w-3" aria-hidden="true">
          <path d="M6 6l12 12M18 6L6 18" stroke-linecap="round" />
        </svg>
      </button>

      <button
        v-for="(tag, index) in visibleTags"
        :key="tag.name"
        type="button"
        class="inline-flex h-11 min-w-0 max-w-full shrink-0 items-center gap-1 rounded-full border border-line bg-surface px-3.5 text-xs font-medium text-muted transition hover:border-brand/40 hover:bg-brand-soft/60 hover:text-ink motion-safe:active:scale-[0.97] [overflow-wrap:anywhere]"
        @click="toggle(tag.name)"
      >
        <span class="tnum text-muted">#</span>{{ tag.name
        }}<span class="tnum rounded-full bg-canvas px-1.5 text-xs text-muted">{{ tag.count }}</span>
      </button>

      </div>
        <span class="pointer-events-none absolute inset-y-0 right-0 w-8 bg-gradient-to-l from-canvas to-transparent" aria-hidden="true"></span>
      </div>

      <button
        ref="trigger"
        type="button"
        class="inline-flex h-11 shrink-0 items-center gap-1.5 rounded-full border border-dashed border-line px-3.5 text-xs font-medium text-muted transition hover:border-brand/40 hover:text-ink"
        :aria-expanded="open"
        aria-haspopup="dialog"
        @click="open ? closePanel() : openPanel()"
      >
        全部标签
        <span class="tnum rounded-full bg-canvas px-1.5 text-xs">{{ props.tags.length }}</span>
      </button>

      <button
        v-if="selectedTags.length > 0"
        type="button"
        class="h-11 shrink-0 rounded-lg px-2 text-xs font-medium text-danger underline-offset-4 hover:underline"
        @click="clearAll"
      >
        清除标签
      </button>
    </div>

    <div
      v-if="open"
      ref="panel"
      role="dialog"
      aria-label="全部标签"
      class="fixed inset-x-3 bottom-3 z-40 max-h-[70vh] overflow-auto rounded-2xl bg-surface p-4 shadow-[0_1px_0_0_rgba(27,26,23,0.05),0_28px_60px_-30px_rgba(27,26,23,0.4)] sm:absolute sm:inset-x-auto sm:bottom-auto sm:right-0 sm:top-full sm:mt-2 sm:w-[520px]"
    >
      <div class="flex items-center justify-between gap-3">
        <p class="text-sm font-semibold">全部标签 <span class="tnum text-xs font-normal text-muted">{{ props.tags.length }}</span></p>
        <button
          type="button"
          class="inline-flex h-9 w-9 items-center justify-center rounded-lg text-muted transition hover:bg-canvas hover:text-ink"
          aria-label="关闭标签面板"
          @click="closePanel"
        >
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" class="h-4 w-4" aria-hidden="true">
            <path d="M6 6l12 12M18 6L6 18" stroke-linecap="round" />
          </svg>
        </button>
      </div>

      <label class="mt-3 block">
        <span class="sr-only">搜索标签</span>
        <input
          ref="searchInput"
          v-model="query"
          type="search"
          placeholder="搜索标签…"
          class="h-11 w-full rounded-xl border border-line bg-canvas px-3.5 text-sm outline-none transition focus:border-ink/40 focus:bg-surface"
        />
      </label>

      <div v-if="selectedTags.length > 0" class="mt-3 flex flex-wrap items-center gap-1.5">
        <span class="text-xs text-muted">已选</span>
        <button
          v-for="name in selectedTags"
          :key="'pick-' + name"
          type="button"
          class="inline-flex min-w-0 max-w-full items-center gap-1 rounded-full bg-accent-soft px-2.5 py-1 text-xs font-medium text-accent [overflow-wrap:anywhere]"
          :aria-label="'取消 ' + name"
          @click="toggle(name)"
        >
          #{{ name }}
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" class="h-3 w-3" aria-hidden="true">
            <path d="M6 6l12 12M18 6L6 18" stroke-linecap="round" />
          </svg>
        </button>
        <button type="button" class="px-1 text-xs font-medium text-danger hover:underline" @click="clearAll">清空</button>
      </div>

      <div class="mt-3 flex flex-wrap gap-2">
        <button
          v-for="tag in panelTags"
          :key="'panel-' + tag.name"
          type="button"
          class="inline-flex h-9 min-w-0 max-w-full items-center gap-1 rounded-full border px-3 text-xs font-medium transition [overflow-wrap:anywhere]"
          :class="
            selectedTags.includes(tag.name)
              ? 'border-accent bg-accent text-white'
              : 'border-line bg-canvas text-muted hover:border-brand/40 hover:text-ink'
          "
          :aria-pressed="selectedTags.includes(tag.name)"
          @click="toggle(tag.name)"
        >
          #{{ tag.name }}
          <span class="tnum px-1 text-xs" :class="selectedTags.includes(tag.name) ? 'text-white/80' : 'text-muted'">{{ tag.count }}</span>
        </button>
        <p v-if="panelTags.length === 0" class="py-4 text-sm text-muted">没有匹配的标签</p>
      </div>
    </div>
  </div>
</template>
