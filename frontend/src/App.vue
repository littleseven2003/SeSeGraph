<script setup lang="ts">
import VersionFooter from './components/VersionFooter.vue'
</script>

<template>
  <div class="flex min-h-dvh flex-col bg-canvas text-ink">
    <div class="flex-1">
      <router-view />
    </div>
    <VersionFooter />
  </div>
</template>
