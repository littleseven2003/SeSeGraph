<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'
import AppHeader from '../components/AppHeader.vue'
import MetaPanel from '../components/workbench/MetaPanel.vue'
import { ApiError } from '../api/client.ts'
import { commitPost, createPost, uploadImage } from '../api/posts.ts'
import { splitTags, type PostMeta } from '../utils/meta.ts'
import { clearDraft, draftState, hasDraft } from '../stores/draft.ts'

const router = useRouter()

// 草稿页承接导入后的编辑：元信息、排序、封面、画质，确认后建任务并补传原件。
const meta = computed<PostMeta>({
  get: () => ({
    title: draftState.meta.title,
    description: draftState.meta.description,
    tags: splitTags(draftState.meta.tags),
    author: draftState.meta.authorName,
  }),
  set: (value) => {
    draftState.meta.title = value.title
    draftState.meta.description = value.description
    draftState.meta.tags = value.tags.join(',')
    draftState.meta.authorName = value.author
  },
})

const mode = computed<'photo' | 'document'>({
  get: () => draftState.meta.uploadMode,
  set: (value) => {
    draftState.meta.uploadMode = value
  },
})

const intent = computed<'' | 'auto'>({
  get: () => draftState.meta.publishIntent,
  set: (value) => {
    draftState.meta.publishIntent = value
  },
})

const totalBytes = computed(() => draftState.images.reduce((sum, image) => sum + image.size, 0))
const megabytes = computed(() => (totalBytes.value / (1 << 20)).toFixed(1))
const coverIndex = computed(() => {
  const index = draftState.images.findIndex((image) => image.key === draftState.meta.coverKey)
  return index >= 0 ? index : 0
})

const busy = ref(false)
const error = ref('')
const notice = ref('')
const progress = ref({ done: 0, total: 0 })

function moveImage(index: number, offset: number): void {
  const target = index + offset
  if (target < 0 || target >= draftState.images.length) return
  const list = draftState.images
  ;[list[index], list[target]] = [list[target], list[index]]
}

function removeImage(index: number): void {
  draftState.images.splice(index, 1)
  if (draftState.images.length === 0) draftState.restorable = false
}

function chooseCover(key: string): void {
  draftState.meta.coverKey = key
}

async function publish(): Promise<void> {
  if (busy.value) return
  error.value = ''
  notice.value = ''
  if (!hasDraft()) {
    error.value = '还没有可发布的草稿，请先到导入页选择图片'
    return
  }
  if (draftState.meta.title.trim() === '') {
    error.value = '请填写标题'
    return
  }

  busy.value = true
  progress.value = { done: 0, total: draftState.images.length }
  let postID = 0
  try {
    const chosen = draftState.images[coverIndex.value]
    const created = await createPost({
      title: draftState.meta.title.trim(),
      description: draftState.meta.description,
      author_name: draftState.meta.authorName.trim(),
      upload_mode: draftState.meta.uploadMode,
      image_count: draftState.images.length,
      cover_thumbnail: chosen !== undefined ? chosen.thumb : '',
      cover_sort_order: chosen !== undefined ? coverIndex.value + 1 : 0,
      publish_intent: draftState.meta.publishIntent === 'auto' ? 'auto' : undefined,
      tags: splitTags(draftState.meta.tags),
    })
    postID = created.id
    for (const [index, image] of draftState.images.entries()) {
      await uploadImage(postID, index + 1, image.file, image.file.type, 0)
      progress.value = { done: index + 1, total: draftState.images.length }
    }
    await commitPost(postID)
    notice.value = '已提交，正在后台存图与发布，可到任务页查看进度'
    clearDraft()
    await router.push({ name: 'tasks' })
  } catch (cause) {
    error.value = cause instanceof ApiError
      ? cause.message
      : postID > 0
        ? `已建任务 #${String(postID)}，上传中断，可到任务页续传`
        : '提交失败，请稍后重试'
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="min-h-dvh bg-canvas text-ink">
    <AppHeader />
    <main class="mx-auto w-full max-w-[1120px] px-4 pb-16 pt-8 sm:px-6">
      <div class="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 class="text-xl font-semibold tracking-tight">草稿</h1>
          <p class="mt-1 text-sm text-muted">补齐元信息、调整顺序与封面，确认后一次性建任务并补传原件。</p>
        </div>
        <router-link
          to="/workbench"
          class="inline-flex h-10 items-center rounded-lg border border-line bg-surface px-4 text-sm text-muted transition hover:text-ink"
        >
          回到导入
        </router-link>
      </div>

      <template v-if="hasDraft()">
        <section class="mt-6 rounded-2xl bg-surface p-5 shadow-[0_1px_0_0_rgba(27,26,23,0.05)]">
          <MetaPanel
            v-model="meta"
            meta-text=""
            meta-source=""
            :disabled="busy"
            @error="(message: string) => (error = message)"
          />
        </section>

        <section class="mt-4 rounded-2xl bg-surface p-5 shadow-[0_1px_0_0_rgba(27,26,23,0.05)]">
          <div class="flex flex-wrap items-center justify-between gap-2">
            <div>
              <h2 class="text-sm font-semibold">已选媒体</h2>
              <p class="mt-1 text-xs text-muted">拖拽顺序即上传顺序，点「设为封面」决定列表展示图。</p>
            </div>
            <span class="tnum text-xs text-muted">{{ draftState.images.length }} 项 · {{ megabytes }} MB</span>
          </div>

          <ul class="mt-3 grid grid-cols-3 gap-2 sm:grid-cols-5 lg:grid-cols-6">
            <li v-for="(image, index) in draftState.images" :key="image.key" class="min-w-0">
              <div class="group relative overflow-hidden rounded-lg border border-line bg-canvas" :class="coverIndex === index ? 'ring-2 ring-brand' : ''">
                <img :src="image.thumb" :alt="image.name" class="aspect-square w-full object-cover" />
                <span class="tnum absolute left-1.5 top-1.5 rounded bg-ink/70 px-1.5 py-0.5 text-[10px] font-medium text-surface">{{ index + 1 }}</span>
                <span v-if="coverIndex === index" class="absolute right-1.5 top-1.5 rounded bg-brand px-1.5 py-0.5 text-[10px] font-medium text-brand-ink">封面</span>
              </div>
              <div class="mt-1 flex flex-wrap items-center gap-1 text-[11px] text-muted">
                <button type="button" class="rounded border border-line px-1.5 py-0.5 disabled:opacity-40" :disabled="index === 0 || busy" @click="moveImage(index, -1)">前移</button>
                <button type="button" class="rounded border border-line px-1.5 py-0.5 disabled:opacity-40" :disabled="index === draftState.images.length - 1 || busy" @click="moveImage(index, 1)">后移</button>
                <button type="button" class="rounded border border-line px-1.5 py-0.5" :disabled="busy || coverIndex === index" @click="chooseCover(image.key)">设为封面</button>
                <button type="button" class="rounded border border-line px-1.5 py-0.5 text-danger" :disabled="busy" @click="removeImage(index)">移除</button>
              </div>
            </li>
          </ul>
        </section>

        <section class="mt-4 rounded-2xl bg-surface p-5 shadow-[0_1px_0_0_rgba(27,26,23,0.05)]">
          <div class="grid gap-3 sm:grid-cols-2">
            <label class="block">
              <span class="text-xs font-medium text-muted">画质</span>
              <select v-model="mode" :disabled="busy" class="mt-1 h-10 w-full rounded-lg border border-line bg-canvas px-2 text-sm outline-none">
                <option value="photo">省流量（照片，单张 10MB 内）</option>
                <option value="document">原图（文档，单张 20MB 内）</option>
              </select>
            </label>
            <label class="block">
              <span class="text-xs font-medium text-muted">发布方式</span>
              <select v-model="intent" :disabled="busy" class="mt-1 h-10 w-full rounded-lg border border-line bg-canvas px-2 text-sm outline-none">
                <option value="">只存图，稍后手动发布</option>
                <option value="auto">存图完成后自动发布</option>
              </select>
            </label>
          </div>

          <div class="mt-4 flex flex-wrap items-center gap-3">
            <button
              type="button"
              class="inline-flex h-11 items-center rounded-lg bg-ink px-5 text-sm font-medium text-surface transition hover:opacity-90 disabled:opacity-60"
              :disabled="busy"
              @click="publish"
            >
              {{ busy ? `提交中 ${String(progress.done)}/${String(progress.total)}` : '确认并开始上传' }}
            </button>
            <span v-if="notice !== ''" class="text-xs text-accent">{{ notice }}</span>
            <span v-if="error !== ''" class="text-xs text-danger">{{ error }}</span>
          </div>
        </section>
      </template>

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
