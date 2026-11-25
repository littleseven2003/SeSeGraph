<script setup lang="ts">
import { computed } from 'vue'
import rawWordmark from '../assets/sesegraph-wordmark.svg?raw'

// 品牌字标：跳色 SeSe + Graph 的整份矢量字标。内联后 Graph 走 currentColor、
// 四个字母走四阶段令牌，深浅两套主题都不用另做一份图片。
const props = withDefaults(defineProps<{ width?: number }>(), { width: 132 })

// 字标原始比例是 184×40，只给宽度即可保持比例，避免不同页面拉伸。
const height = computed(() => Math.round((props.width * 40) / 184))

const markup = computed(() => {
  const themed = rawWordmark
    .replace('fill="#D04223"', 'fill="var(--color-stage-compress)"')
    .replace('fill="#D97706"', 'fill="var(--color-stage-upload)"')
    .replace('fill="#0D7A72"', 'fill="var(--color-stage-sync)"')
    .replace('fill="#C0266A"', 'fill="var(--color-stage-publish)"')
    .replace('fill="#1B1A17"', 'fill="currentColor"')
    .replace('<svg ', `<svg width="${String(props.width)}" height="${String(height.value)}" `)
  return themed
})
</script>

<template>
  <!-- 用当前文字色承接 Graph，深色主题下自动变亮，不再有墨黑块看不清。 -->
  <span class="inline-flex shrink-0 select-none text-ink" role="img" aria-label="SeSeGraph" v-html="markup"></span>
</template>
