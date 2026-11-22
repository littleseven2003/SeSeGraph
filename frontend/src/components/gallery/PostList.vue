<script setup lang="ts">
import type { GalleryItem } from '../../api/gallery.ts'
import PostRow from './PostRow.vue'

defineProps<{ items: GalleryItem[]; blurred: boolean }>()
const emit = defineEmits<{ open: [id: number] }>()
</script>

<template>
  <div class="rounded-2xl bg-surface px-4 shadow-[0_1px_0_0_rgba(27,26,23,0.05)]">
    <div class="hidden items-center gap-3 border-b border-line/60 py-2 text-xs text-muted sm:flex">
      <span class="w-16 shrink-0">封面</span>
      <span class="flex-1">标题</span>
      <span class="w-20 shrink-0 text-right">状态</span>
      <span class="w-[92px] shrink-0"></span>
    </div>
    <div
      v-for="(item, index) in items"
      :key="item.id"
      :class="index > 0 ? 'border-t border-line/60' : ''"
    >
      <PostRow :item="item" :blurred="blurred" @open="emit('open', $event)" />
    </div>
  </div>
</template>
