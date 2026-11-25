<script setup lang="ts">
import { computed } from 'vue'
import AppHeader from '../components/AppHeader.vue'
import { draftState, hasDraft } from '../stores/draft.ts'

// 这一屏承接工作台导入后的编辑：当前只展示草稿概要，元信息与发布在下一步迁入。
const summary = computed(() => ({
  count: draftState.images.length,
  bytes: draftState.images.reduce((sum, image) => sum + image.size, 0),
}))

const megabytes = computed(() => (summary.value.bytes / (1 << 20)).toFixed(1))
</script>

<template>
  <div class="min-h-dvh bg-canvas text-ink">
    <AppHeader />
    <main class="mx-auto w-full max-w-[1120px] px-4 pb-16 pt-8 sm:px-6">
      <div class="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 class="text-xl font-semibold tracking-tight">草稿</h1>
          <p class="mt-1 text-sm text-muted">在这里补齐元信息、调整顺序与封面，确认后一次性发布。</p>
        </div>
        <router-link
          to="/workbench"
          class="inline-flex h-10 items-center rounded-lg border border-line bg-surface px-4 text-sm text-muted transition hover:text-ink"
        >
          回到导入
        </router-link>
      </div>

      <section v-if="hasDraft()" class="mt-6 rounded-2xl bg-surface p-5 shadow-[0_1px_0_0_rgba(27,26,23,0.05)]">
        <h2 class="text-sm font-semibold">已接收 {{ summary.count }} 张 · {{ megabytes }} MB</h2>
        <p class="mt-1 text-xs text-muted">图片只在本机内存里，确认发布前不会上传；离开这一页会保留草稿。</p>
        <ul class="mt-4 grid grid-cols-3 gap-2 sm:grid-cols-6 lg:grid-cols-8">
          <li v-for="image in draftState.images" :key="image.key" class="overflow-hidden rounded-lg border border-line bg-canvas">
            <img :src="image.thumb" :alt="image.name" class="aspect-square w-full object-cover" />
          </li>
        </ul>
      </section>

      <section v-else class="mt-6 rounded-2xl border border-dashed border-line bg-surface px-6 py-12 text-center">
        <p class="text-sm font-medium">还没有草稿</p>
        <p class="mt-1 text-xs text-muted">先到导入页选择图片或压缩包，生成小图后会自动进入这里。</p>
        <router-link
          to="/workbench"
          class="mt-4 inline-flex h-10 items-center rounded-lg bg-ink px-4 text-sm font-medium text-surface transition hover:opacity-90"
        >
          去导入
        </router-link>
      </section>
    </main>
  </div>
</template>
