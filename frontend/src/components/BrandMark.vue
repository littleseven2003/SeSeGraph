<script setup lang="ts">
import { computed } from 'vue'

// 品牌字标：跳色 SeSe + 墨黑 Graph 的完整矢量字标，与 favicon 共用同一份品牌资产。
const props = withDefaults(defineProps<{ width?: number }>(), { width: 132 })

// 字标原始比例是 184×40，只给宽度即可保持比例，避免不同页面拉伸。
const height = computed(() => Math.round((props.width * 40) / 184))
</script>

<template>
  <img
    :src="'/sesegraph-wordmark.svg'"
    :width="props.width"
    :height="height"
    alt="SeSeGraph"
    class="shrink-0 select-none"
    decoding="async"
  />
</template>
