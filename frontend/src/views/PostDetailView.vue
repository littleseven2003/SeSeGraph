<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import AppHeader from '../components/AppHeader.vue'
import TaskStageBar from '../components/tasks/TaskStageBar.vue'
import {
  deletePost,
  fetchPostDetail,
  refreshPostViews,
  setPostDisabled,
  updatePost,
  type PostDetail,
} from '../api/gallery.ts'
import { changeCover, fetchProgress, thumbPath, type ServerProgress } from '../api/posts.ts'
import { ApiError } from '../api/client.ts'
import { copyText } from '../utils/clipboard.ts'
import { imageShareUrl, postShareUrl, statusLabel } from '../utils/gallery.ts'
import { detailUploadModeLabel } from '../utils/media.ts'
import { blurEnabled } from '../utils/blur.ts'
import { splitTags } from '../utils/meta.ts'
import { stageInputOf, stageOverall, stageSlots, stageSummary, taskStages } from '../utils/stages.ts'

// 独立详情页：图集浏览与管理都在这里完成，工作台只保留新建与续传。
const route = useRoute()
const router = useRouter()

const postId = computed(() => Number(route.params.id))
const detail = ref<PostDetail | null>(null)
const progress = ref<ServerProgress | null>(null)
const loading = ref(true)
const busy = ref('')
const error = ref('')
const notice = ref('')

// 编辑态与表单，保存时只提交这一份元信息。
const editing = ref(false)
const form = ref({ title: '', description: '', authorName: '', tagsText: '' })
const revealed = ref(new Set<number>())
const lightbox = ref<number | null>(null)
const confirmDelete = ref(false)

let pollTimer: ReturnType<typeof setInterval> | null = null
let disposed = false

const badge = computed(() =>
  detail.value === null ? { label: '', tone: 'idle' } : statusLabel(detail.value.status, detail.value.publish_state, detail.value.disabled),
)

const shareUrl = computed(() => (detail.value === null ? '' : postShareUrl(detail.value)))

// 四阶段口径复用任务页的同一份判定，动态分母也一致。
const stages = computed(() => (progress.value === null ? [] : taskStages(stageInputOf(stageInputOfSource()))))
const slots = computed(() =>
  progress.value === null
    ? []
    : stageSlots(stages.value, {
        publishIntent: '',
        publishState: progress.value.publish_state,
        hasCompressRecord: progress.value.compressed_count !== null && progress.value.compressed_count !== undefined,
      }),
)
const overall = computed(() =>
  stageOverall(stages.value, {
    publishIntent: '',
    publishState: progress.value?.publish_state ?? '',
    hasCompressRecord: progress.value === null ? true : progress.value.compressed_count !== null && progress.value.compressed_count !== undefined,
  }),
)

function stageInputOfSource() {
  const state = progress.value
  if (state === null) {
    return { image_count: 0, status: '', publish_state: '', control_state: '', received_count: 0, channel_count: 0, compressed_count: null }
  }
  const received = state.images.filter((image) => ['uploaded', 'stored', 'failed'].includes(image.status)).length
  return {
    image_count: state.image_count,
    status: state.status,
    publish_state: state.publish_state,
    control_state: state.control_state,
    received_count: received,
    channel_count: state.stored_count,
    compressed_count: state.compressed_count ?? null,
  }
}

const activeTask = computed(() => {
  const state = progress.value
  if (state === null) return false
  return ['uploading', 'queued'].includes(state.status) || ['pending', 'publishing'].includes(state.publish_state)
})

function messageOf(cause: unknown): string {
  if (cause instanceof ApiError) return cause.message
  return '网络异常，请稍后重试'
}

async function load(): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    detail.value = await fetchPostDetail(postId.value)
    form.value = {
      title: detail.value.title,
      description: detail.value.description,
      authorName: detail.value.author_name,
      tagsText: detail.value.tags.join('，'),
    }
    progress.value = await fetchProgress(postId.value).catch(() => null)
    syncPolling()
  } catch (cause) {
    error.value = messageOf(cause)
    detail.value = null
  } finally {
    loading.value = false
  }
}

async function reloadProgress(): Promise<void> {
  progress.value = await fetchProgress(postId.value).catch(() => null)
  syncPolling()
}

function syncPolling(): void {
  const need = !disposed && activeTask.value
  if (need && pollTimer === null) pollTimer = setInterval(() => void reloadProgress(), 2000)
  if (!need && pollTimer !== null) {
    clearInterval(pollTimer)
    pollTimer = null
  }
}

async function run(label: string, action: () => Promise<void>): Promise<void> {
  if (busy.value !== '') return
  busy.value = label
  error.value = ''
  notice.value = ''
  try {
    await action()
  } catch (cause) {
    error.value = messageOf(cause)
  } finally {
    busy.value = ''
  }
}

function startEdit(): void {
  editing.value = true
}

function cancelEdit(): void {
  editing.value = false
  if (detail.value !== null) {
    form.value = {
      title: detail.value.title,
      description: detail.value.description,
      authorName: detail.value.author_name,
      tagsText: detail.value.tags.join('，'),
    }
  }
}

async function saveMeta(): Promise<void> {
  await run('save', async () => {
    await updatePost(postId.value, {
      title: form.value.title.trim(),
      description: form.value.description.trim(),
      author_name: form.value.authorName.trim(),
      tags: splitTags(form.value.tagsText),
    })
    editing.value = false
    notice.value = '信息已保存'
    await load()
  })
}

async function toggleDisabled(): Promise<void> {
  if (detail.value === null) return
  await run('toggle', async () => {
    await setPostDisabled(postId.value, !detail.value?.disabled)
    notice.value = detail.value?.disabled === true ? '已恢复公开' : '已撤回公开'
    await load()
  })
}

async function removePost(): Promise<void> {
  await run('delete', async () => {
    await deletePost(postId.value)
    await router.replace({ name: 'home' })
  })
}

async function setCover(sortOrder: number): Promise<void> {
  await run('cover-' + String(sortOrder), async () => {
    await changeCover(postId.value, sortOrder)
    notice.value = '封面已更新'
    await load()
  })
}

async function refreshViews(): Promise<void> {
  await run('views', async () => {
    const result = await refreshPostViews(postId.value)
    if (detail.value !== null) {
      detail.value = { ...detail.value, views_count: result.views_count, views_updated_at: result.views_updated_at }
    }
    notice.value = result.cached ? '浏览量为缓存值' : '浏览量已刷新'
  })
}

async function copy(text: string, what: string): Promise<void> {
  if (text === '') return
  const ok = await copyText(text)
  notice.value = ok ? `${what}已复制` : `${what}复制失败，请手动选择`
}

function thumbUrl(image: { sort_order: number; media_version: string }): string {
  return thumbPath(postId.value, image.sort_order, image.media_version || String(image.sort_order))
}

function imageLink(image: { short_code: string; status: string }): string {
  if (detail.value === null) return ''
  return imageShareUrl(detail.value, image, detail.value.public_base_url)
}

// 防社死开启时先盖住缩略图，点一下只揭开这一张。
function masked(sortOrder: number): boolean {
  return blurEnabled.value && !revealed.value.has(sortOrder)
}

function reveal(sortOrder: number): void {
  if (!masked(sortOrder)) return
  revealed.value = new Set(revealed.value).add(sortOrder)
}

function toggleReveal(sortOrder: number): void {
  const next = new Set(revealed.value)
  if (next.has(sortOrder)) next.delete(sortOrder)
  else next.add(sortOrder)
  revealed.value = next
}

function openLightbox(sortOrder: number): void {
  if (masked(sortOrder)) {
    reveal(sortOrder)
    return
  }
  lightbox.value = sortOrder
}

watch(postId, () => void load())

onMounted(() => {
  void load()
})

onBeforeUnmount(() => {
  disposed = true
  if (pollTimer !== null) clearInterval(pollTimer)
})
</script>

<template>
  <div class="min-h-full">
    <AppHeader />

    <main class="mx-auto w-full max-w-[1120px] px-5 py-6">
      <div class="flex flex-wrap items-center gap-3">
        <router-link
          :to="{ name: 'home' }"
          class="inline-flex h-11 items-center gap-1.5 rounded-lg px-2 text-sm text-muted transition hover:bg-canvas hover:text-ink sm:h-9"
        >
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" class="h-4 w-4" aria-hidden="true">
            <path d="M15 6l-6 6 6 6" stroke-linecap="round" stroke-linejoin="round" />
          </svg>
          返回图集
        </router-link>
        <h1 class="min-w-0 flex-1 truncate text-xl font-semibold sm:text-2xl">{{ detail?.title ?? '图集详情' }}</h1>
        <span
          v-if="detail !== null"
          class="shrink-0 whitespace-nowrap rounded-md px-2.5 py-1 text-xs font-medium"
          :class="{
            'bg-accent-soft text-accent': badge.tone === 'ok',
            'bg-brand-soft text-brand-strong': badge.tone === 'progress',
            'bg-danger-soft text-danger': badge.tone === 'error',
            'bg-canvas text-muted': badge.tone === 'idle',
          }"
        >
          {{ badge.label }}
        </span>
      </div>

      <p v-if="error !== ''" class="mt-3 text-xs text-danger">{{ error }}</p>
      <p v-else-if="notice !== ''" class="mt-3 text-xs text-accent">{{ notice }}</p>

      <div v-if="loading" class="mt-6 rounded-xl border border-line bg-surface px-6 py-16 text-center text-sm text-muted">正在读取详情…</div>

      <div v-else-if="detail !== null" class="mt-5 grid gap-5 lg:grid-cols-[minmax(0,1fr)_340px]">
        <section class="min-w-0">
          <div class="rounded-xl border border-line bg-surface p-3 sm:p-4">
            <div class="flex flex-wrap items-center justify-between gap-2 pb-3">
              <p class="text-sm font-medium">媒体 <span class="tnum text-xs text-muted">{{ detail.images.length }} 张</span></p>
              <p class="text-xs text-muted">点图预览，长按或点眼睛可单张揭开</p>
            </div>
            <ul class="grid grid-cols-2 gap-2.5 sm:grid-cols-3 xl:grid-cols-4">
              <li v-for="image in detail.images" :key="image.sort_order" class="min-w-0">
                <div class="group relative overflow-hidden rounded-lg border border-line bg-canvas">
                  <button type="button" class="relative block aspect-square w-full" :aria-label="`预览第 ${image.sort_order} 张`" @click="openLightbox(image.sort_order)">
                    <img
                      v-if="thumbUrl(image) !== ''"
                      :src="thumbUrl(image)"
                      :alt="`第 ${image.sort_order} 张`"
                      class="h-full w-full object-cover transition"
                      :class="masked(image.sort_order) ? 'scale-105 blur-xl' : 'blur-0'"
                      loading="lazy"
                    />
                    <span v-if="masked(image.sort_order)" class="absolute inset-0 flex items-center justify-center bg-ink/10 text-ink/70">
                      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" class="h-6 w-6" aria-hidden="true">
                        <path d="M2.5 12S6 6.5 12 6.5 21.5 12 21.5 12 18 17.5 12 17.5 2.5 12 2.5 12Z" stroke-linecap="round" />
                        <circle cx="12" cy="12" r="2.6" />
                      </svg>
                    </span>
                    <span v-if="detail.cover_sort_order === image.sort_order" class="absolute left-1.5 top-1.5 rounded bg-brand px-1.5 py-0.5 text-[10px] font-medium text-brand-ink">封面</span>
                  </button>
                  <div class="flex items-center justify-between gap-1 border-t border-line/70 px-1.5 py-1">
                    <span class="tnum text-[11px] text-muted">#{{ image.sort_order }}</span>
                    <div class="flex items-center gap-0.5">
                      <button type="button" class="inline-flex h-8 w-8 items-center justify-center rounded-md text-muted transition hover:bg-canvas hover:text-ink" :aria-label="`揭开第 ${image.sort_order} 张`" @click="toggleReveal(image.sort_order)">
                        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" class="h-4 w-4" aria-hidden="true">
                          <path d="M2.5 12S6 6.5 12 6.5 21.5 12 21.5 12 18 17.5 12 17.5 2.5 12 2.5 12Z" stroke-linecap="round" />
                          <circle cx="12" cy="12" r="2.6" />
                        </svg>
                      </button>
                      <button type="button" class="inline-flex h-8 w-8 items-center justify-center rounded-md text-muted transition hover:bg-canvas hover:text-ink disabled:opacity-40" :disabled="imageLink(image) === ''" :aria-label="`复制第 ${image.sort_order} 张链接`" @click="copy(imageLink(image), '单图链接')">
                        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" class="h-4 w-4" aria-hidden="true">
                          <rect x="9" y="9" width="11" height="11" rx="2" />
                          <path d="M5 15V6a2 2 0 0 1 2-2h8" stroke-linecap="round" />
                        </svg>
                      </button>
                      <button type="button" class="inline-flex h-8 items-center rounded-md px-1.5 text-[11px] text-muted transition hover:bg-canvas hover:text-ink disabled:opacity-40" :disabled="image.status !== 'stored' || busy !== ''" @click="setCover(image.sort_order)">
                        设为封面
                      </button>
                    </div>
                  </div>
                </div>
              </li>
            </ul>
            <p v-if="detail.images.length === 0" class="py-10 text-center text-sm text-muted">这条图集还没有图片</p>
          </div>

          <div id="progress" class="mt-4 rounded-xl border border-line bg-surface p-4">
            <div class="flex flex-wrap items-center justify-between gap-2">
              <p class="text-sm font-medium">进度</p>
              <p class="tnum text-xs text-muted">总进度 {{ overall }}%</p>
            </div>
            <TaskStageBar v-if="stages.length > 0" class="mt-3" :stages="stages" />
            <p class="mt-2 text-xs text-muted">{{ stages.length > 0 ? stageSummary(stages) : '暂时读不到进度，稍后再看' }}</p>
            <div class="mt-3 flex flex-wrap gap-1.5" aria-hidden="true">
              <span v-for="slot in slots" :key="slot.key" class="h-2 w-16 overflow-hidden rounded-[2px] bg-canvas" :title="`${slot.label}${slot.inactive ? slot.reason : ''}`">
                <span v-if="slot.inactive" class="stage-slot-idle block h-full w-full"></span>
                <span v-else class="block h-full" :class="'bg-stage-' + slot.key" :style="{ width: `${String(Math.round(slot.ratio * 100))}%` }"></span>
              </span>
            </div>
          </div>
        </section>

        <aside class="min-w-0 lg:sticky lg:top-20 lg:self-start">
          <div class="rounded-xl border border-line bg-surface p-4">
            <div class="flex items-center justify-between gap-2">
              <p class="text-sm font-medium">信息</p>
              <button v-if="!editing" type="button" class="inline-flex h-9 items-center rounded-lg border border-line px-2.5 text-xs text-muted transition hover:bg-canvas hover:text-ink" @click="startEdit">编辑</button>
            </div>

            <dl v-if="!editing" class="mt-3 space-y-2 text-sm">
              <div class="flex items-start justify-between gap-3"><dt class="shrink-0 text-xs text-muted">张数</dt><dd class="tnum text-right">{{ detail.image_count }}</dd></div>
              <div class="flex items-start justify-between gap-3"><dt class="shrink-0 text-xs text-muted">画质</dt><dd class="text-right">{{ detailUploadModeLabel(detail.upload_mode) }}</dd></div>
              <div class="flex items-start justify-between gap-3"><dt class="shrink-0 text-xs text-muted">发布</dt><dd class="text-right">{{ detail.published_at === '' ? '未发布' : detail.published_at }}</dd></div>
              <div class="flex items-start justify-between gap-3"><dt class="shrink-0 text-xs text-muted">创建</dt><dd class="text-right">{{ detail.created_at }}</dd></div>
              <div class="flex items-start justify-between gap-3">
                <dt class="shrink-0 text-xs text-muted">浏览量</dt>
                <dd class="flex items-center gap-1.5 text-right"><span class="tnum">{{ detail.views_count }}</span>
                  <button type="button" class="inline-flex h-8 items-center rounded-md border border-line px-1.5 text-[11px] text-muted transition hover:bg-canvas hover:text-ink disabled:opacity-50" :disabled="busy !== ''" @click="refreshViews">刷新</button>
                </dd>
              </div>
              <div class="flex items-start justify-between gap-3"><dt class="shrink-0 text-xs text-muted">作者</dt><dd class="text-right">{{ detail.author_name === '' ? '—' : detail.author_name }}</dd></div>
              <div class="border-t border-line/70 pt-2">
                <dt class="text-xs text-muted">标签</dt>
                <dd class="mt-1 flex flex-wrap gap-1">
                  <span v-for="tag in detail.tags" :key="tag" class="rounded-md bg-canvas px-1.5 py-0.5 text-xs text-muted">#{{ tag }}</span>
                  <span v-if="detail.tags.length === 0" class="text-xs text-muted">无</span>
                </dd>
              </div>
              <div class="border-t border-line/70 pt-2">
                <dt class="text-xs text-muted">说明</dt>
                <dd class="mt-1 whitespace-pre-wrap text-xs leading-relaxed">{{ detail.description === '' ? '暂无说明' : detail.description }}</dd>
              </div>
            </dl>

            <form v-else class="mt-3 space-y-2.5" @submit.prevent="saveMeta">
              <label class="block"><span class="text-xs text-muted">标题</span>
                <input v-model="form.title" class="mt-1 h-10 w-full rounded-lg border border-line bg-canvas px-3 text-sm outline-none focus:border-line-control" maxlength="120" />
              </label>
              <label class="block"><span class="text-xs text-muted">作者</span>
                <input v-model="form.authorName" class="mt-1 h-10 w-full rounded-lg border border-line bg-canvas px-3 text-sm outline-none focus:border-line-control" maxlength="40" />
              </label>
              <label class="block"><span class="text-xs text-muted">标签（用逗号分隔）</span>
                <input v-model="form.tagsText" class="mt-1 h-10 w-full rounded-lg border border-line bg-canvas px-3 text-sm outline-none focus:border-line-control" />
              </label>
              <label class="block"><span class="text-xs text-muted">说明</span>
                <textarea v-model="form.description" rows="4" class="mt-1 w-full rounded-lg border border-line bg-canvas px-3 py-2 text-sm outline-none focus:border-line-control"></textarea>
              </label>
              <div class="flex items-center gap-2 pt-1">
                <button type="submit" class="inline-flex h-10 items-center rounded-lg bg-brand px-3 text-sm font-medium text-brand-ink transition hover:bg-brand-strong disabled:opacity-60" :disabled="busy !== ''">保存更改</button>
                <button type="button" class="inline-flex h-10 items-center rounded-lg border border-line px-3 text-sm text-muted transition hover:bg-canvas hover:text-ink" @click="cancelEdit">放弃</button>
              </div>
            </form>
          </div>

          <div class="mt-3 rounded-xl border border-line bg-surface p-4">
            <p class="text-sm font-medium">分享</p>
            <div class="mt-2 flex items-center gap-2">
              <input :value="shareUrl === '' ? '尚未发布，暂无公开链接' : shareUrl" readonly class="h-9 min-w-0 flex-1 rounded-lg border border-line bg-canvas px-2.5 text-xs text-muted outline-none" />
              <button type="button" class="inline-flex h-9 shrink-0 items-center rounded-lg border border-line px-2.5 text-xs font-medium text-muted transition hover:bg-canvas hover:text-ink disabled:opacity-40" :disabled="shareUrl === ''" @click="copy(shareUrl, '分享链接')">复制</button>
            </div>
            <ul v-if="detail.telegraph_urls.length > 1" class="mt-2 space-y-1">
              <li v-for="(url, index) in detail.telegraph_urls" :key="url" class="flex items-center gap-2 text-xs text-muted">
                <span class="tnum shrink-0">第 {{ index + 1 }} 页</span>
                <span class="min-w-0 flex-1 truncate">{{ url }}</span>
                <button type="button" class="shrink-0 text-xs text-brand-strong hover:underline" @click="copy(url, '分页链接')">复制</button>
              </li>
            </ul>
          </div>

          <div class="mt-3 rounded-xl border border-line bg-surface p-4">
            <p class="text-sm font-medium">管理</p>
            <div class="mt-2 flex flex-wrap gap-2">
              <button type="button" class="inline-flex h-10 items-center rounded-lg border border-line px-3 text-xs font-medium text-muted transition hover:bg-canvas hover:text-ink disabled:opacity-60" :disabled="busy !== '' || detail.pending_delete" @click="toggleDisabled">
                {{ detail.disabled ? '恢复公开' : '撤回公开' }}
              </button>
              <button type="button" class="inline-flex h-10 items-center rounded-lg border border-danger/40 px-3 text-xs font-medium text-danger transition hover:bg-danger-soft disabled:opacity-60" :disabled="busy !== ''" @click="confirmDelete = true">永久删除</button>
            </div>
            <p class="mt-2 text-[11px] leading-relaxed text-muted">撤回后短码立即 404，恢复沿用原有公开授权。</p>
          </div>
        </aside>
      </div>

      <div v-if="confirmDelete" class="fixed inset-0 z-40 flex items-center justify-center bg-ink/40 p-4" role="dialog" aria-modal="true" aria-label="确认永久删除">
        <div class="w-full max-w-sm rounded-xl border border-line bg-surface p-5">
          <p class="text-sm font-semibold">永久删除这条图集？</p>
          <p class="mt-2 text-xs text-muted">远端页面会先清空，成功后才移除本地记录，删除后无法恢复。</p>
          <div class="mt-4 flex justify-end gap-2">
            <button type="button" class="inline-flex h-10 items-center rounded-lg border border-line px-3 text-sm text-muted transition hover:bg-canvas hover:text-ink" @click="confirmDelete = false">取消</button>
            <button type="button" class="inline-flex h-10 items-center rounded-lg bg-danger px-3 text-sm font-medium text-white transition disabled:opacity-60" :disabled="busy !== ''" @click="removePost">确认删除</button>
          </div>
        </div>
      </div>

      <div v-if="lightbox !== null" class="fixed inset-0 z-40 flex items-center justify-center bg-ink/70 p-4" role="dialog" aria-modal="true" aria-label="图片预览" @click.self="lightbox = null">
        <img :src="`/api/posts/${String(postId)}/media/${String(lightbox)}`" :alt="`第 ${String(lightbox)} 张`" class="max-h-[86vh] max-w-full rounded-lg object-contain" />
        <button type="button" class="absolute right-4 top-4 inline-flex h-10 w-10 items-center justify-center rounded-lg bg-surface text-ink" aria-label="关闭预览" @click="lightbox = null">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" class="h-4 w-4" aria-hidden="true"><path d="M6 6l12 12M18 6L6 18" stroke-linecap="round" /></svg>
        </button>
      </div>
    </main>
  </div>
</template>
