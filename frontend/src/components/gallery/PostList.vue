<script setup lang="ts">
import type { GalleryItem } from '../../api/gallery.ts'
import PostRow from './PostRow.vue'

defineProps<{ items: GalleryItem[]; blurred: boolean }>()
const emit = defineEmits<{ open: [id: number] }>()
</script>

<template>
  <div class="rounded-2xl bg-surface px-4 shadow-[0_1px_0_0_rgba(27,26,23,0.05)]">
    <!-- 表头与数据行共用同一套网格列宽，桌面端逐列对齐。 -->
    <div
      class="hidden border-b border-line/60 py-2 text-xs text-muted md:grid md:grid-cols-[4.25rem_minmax(11rem,1.2fr)_minmax(9rem,1.4fr)_8.5rem_6.5rem] md:items-center md:gap-4 lg:grid-cols-[5rem_minmax(15rem,1.15fr)_minmax(16rem,1.5fr)_9rem_6.75rem]"
    >
      <span>封面</span>
      <span>标题</span>
      <span>描述</span>
      <span>状态</span>
      <span></span>
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
