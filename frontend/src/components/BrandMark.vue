<script setup lang="ts">
// 品牌徽标：与 favicon 共用同一份 /sesegraph-mark.svg，避免两处图形走样。
withDefaults(defineProps<{ size?: number }>(), { size: 32 })
</script>

<template>
  <img
    :src="'/sesegraph-mark.svg'"
    :width="size"
    :height="size"
    alt=""
    aria-hidden="true"
    class="shrink-0 select-none"
    decoding="async"
  />
</template>
