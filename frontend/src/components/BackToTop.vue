<script setup lang="ts">
// 回到顶部：滚过首屏后淡入，只滚动窗口，不动筛选、页码与已加载数据。
import { onBeforeUnmount, onMounted, ref } from 'vue'

const props = defineProps<{ liftOnNarrow?: boolean }>()

const shown = ref(false)

function onScroll(): void {
  shown.value = window.scrollY > 360
}

function toTop(): void {
  const reduce = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches ?? false
  window.scrollTo({ top: 0, behavior: reduce ? 'auto' : 'smooth' })
}

onMounted(() => {
  onScroll()
  window.addEventListener('scroll', onScroll, { passive: true })
})

onBeforeUnmount(() => {
  window.removeEventListener('scroll', onScroll)
})
</script>

<template>
  <button
    v-show="shown"
    type="button"
    class="fixed right-4 z-20 flex h-9 w-9 items-center justify-center rounded-xl border border-line bg-surface/95 text-muted shadow-sm backdrop-blur-sm transition hover:text-ink focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ink/25 sm:right-6 sm:h-10 sm:w-10"
    :class="props.liftOnNarrow ? 'bottom-24 lg:bottom-6' : 'bottom-5 sm:bottom-6'"
    aria-label="回到顶部"
    title="回到顶部"
    @click="toTop"
  >
    <svg
      class="h-4 w-4"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      stroke-width="1.75"
      stroke-linecap="round"
      stroke-linejoin="round"
      aria-hidden="true"
    >
      <path d="M12 19V5" />
      <path d="M5 12l7-7 7 7" />
    </svg>
  </button>
</template>
