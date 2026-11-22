<script setup lang="ts">
// 品牌徽标：一张轻斜的珊瑚纸卡压着一张取景明信片，呼应图册与取景框。
withDefaults(defineProps<{ size?: number }>(), { size: 32 })
</script>

<template>
  <svg
    class="brand-mark"
    :width="size"
    :height="size"
    viewBox="0 0 32 32"
    fill="none"
    aria-hidden="true"
    focusable="false"
  >
    <g class="paper" transform="rotate(-7 16 16)">
      <rect x="7.2" y="6.4" width="20.6" height="18" rx="3" fill="#ffd7cc" />
    </g>
    <rect x="4.5" y="7.6" width="21.8" height="18.4" rx="3" fill="#ffffff" stroke="#e8e4da" stroke-width="1" />
    <circle cx="20.6" cy="12.8" r="2.6" fill="#e85d3f" />
    <path
      d="M5.6 24.9c2.7-4.4 5.4-4.6 8.1-2.1 2.1 1.9 4.2 1.6 6-1 1.6-2.3 3.9-2.2 5.4-0.9v1.9c0 .9-.7 1.6-1.6 1.6H7.2c-.9 0-1.6-.7-1.6-1.6z"
      fill="#0d7a72"
      opacity="0.92"
    />
    <path d="M4.5 21.4h21.8" stroke="#e8e4da" stroke-width="0.8" stroke-linecap="round" opacity="0.9" />
  </svg>
</template>

<style scoped>
.paper {
  transform-origin: 16px 16px;
}
</style>
