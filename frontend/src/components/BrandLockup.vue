<script setup lang="ts">
import BrandMark from './BrandMark.vue'

// 品牌锁排：顶栏横向、登录与初始化双行，共用大小时只改 size。
withDefaults(defineProps<{ size?: number; layout?: 'row' | 'stack' }>(), {
  size: 32,
  layout: 'row',
})
</script>

<template>
  <div v-if="layout === 'stack'" class="flex items-center gap-2.5">
    <BrandMark :size="size" />
    <div class="min-w-0">
      <p class="text-sm font-semibold tracking-[-0.02em] text-ink">SeSeGraph</p>
      <p class="mt-0.5 text-xs font-medium tracking-[0.04em] text-muted">SeSe图册</p>
    </div>
  </div>
  <div v-else class="flex min-w-0 items-center gap-2.5">
    <BrandMark :size="size" />
    <span class="truncate text-sm font-semibold tracking-[-0.02em] text-ink">SeSeGraph</span>
    <span class="hidden h-3.5 w-px shrink-0 bg-line sm:block" aria-hidden="true"></span>
    <span class="hidden shrink-0 text-xs font-medium tracking-[0.04em] text-muted sm:inline">SeSe图册</span>
  </div>
</template>
