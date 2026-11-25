<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { ApiError } from '../api/client'
import {
  deletePost,
  fetchGallery,
  fetchPostDetail,
  refreshPostViews,
  fetchTags,
  type GalleryItem,
  type PostDetail,
  type TagCount,
} from '../api/gallery'
import {
  createLatestGuard,
  deleteFailureText,
  pageWindow,
  readViewPreference,
  saveViewPreference,
  type GalleryView,
} from '../utils/gallery'
import AppHeader from '../components/AppHeader.vue'
import BackToTop from '../components/BackToTop.vue'
import DetailDrawer from '../components/gallery/DetailDrawer.vue'
import PostCard from '../components/gallery/PostCard.vue'
import PostList from '../components/gallery/PostList.vue'
import FocusCarousel from '../components/gallery/FocusCarousel.vue'
import TagFilter from '../components/gallery/TagFilter.vue'
import { blurEnabled } from '../utils/blur.ts'

const PAGE_SIZE = 12
const router = useRouter()

const items = ref<GalleryItem[]>([])
const total = ref(0)
const page = ref(1)
const tags = ref<TagCount[]>([])
const keyword = ref('')
const selectedTags = ref<string[]>([])
const loading = ref(false)
const error = ref('')
// 防社死开关由统一顶栏维护，页面只读它的值。
const viewMode = ref<GalleryView>('gallery')

const drawerOpen = ref(false)
const detail = ref<PostDetail | null>(null)
const detailLoading = ref(false)
const viewsRefreshing = ref(false)
const viewsNotice = ref('')
const viewsNoticeTone = ref<'ok' | 'muted'>('ok')
let viewsNoticeTimer: ReturnType<typeof setTimeout> | undefined
const detailError = ref('')
const deleting = ref(false)
const deleteError = ref('')
const detailId = ref<number | null>(null)

let searchTimer: ReturnType<typeof setTimeout> | undefined
// 列表与详情各自维护请求序号，只接受最后一次请求的结果。
const listGuard = createLatestGuard()
const detailGuard = createLatestGuard()

const totalPages = computed(() => Math.max(1, Math.ceil(total.value / PAGE_SIZE)))
const pages = computed(() => pageWindow(page.value, PAGE_SIZE, total.value))
const narrowPages = computed(() => pageWindow(page.value, PAGE_SIZE, total.value, 3))
const hasFilter = computed(() => keyword.value.trim() !== '' || selectedTags.value.length > 0)
// 焦点区只在未筛选的画廊首页出现；筛选、翻页或切到列表视图时一律隐藏，下方画廊保留完整数据。
const showFocus = computed(
  () =>
    viewMode.value === 'gallery' &&
    !hasFilter.value &&
    !loading.value &&
    page.value === 1 &&
    items.value.length > 0,
)
const focusItems = computed(() => items.value.slice(0, 5))

function setViewMode(next: GalleryView): void {
  viewMode.value = next
  saveViewPreference(storageOrUndefined(), next)
}

function storageOrUndefined(): Storage | undefined {
  try {
    return window.localStorage
  } catch {
    return undefined
  }
}

function messageOf(cause: unknown): string {
  if (cause instanceof ApiError) return cause.message
  return '网络异常，请稍后重试'
}

async function loadList(target = page.value): Promise<void> {
  const token = listGuard.begin()
  loading.value = true
  error.value = ''
  try {
    const result = await fetchGallery({
      q: keyword.value,
      tags: selectedTags.value,
      page: target,
      pageSize: PAGE_SIZE,
    })
    if (!listGuard.isCurrent(token)) return
    items.value = result.items
    total.value = result.total
    page.value = result.page
  } catch (cause) {
    if (!listGuard.isCurrent(token)) return
    error.value = messageOf(cause)
  } finally {
    if (listGuard.isCurrent(token)) loading.value = false
  }
}

async function loadTags(): Promise<void> {
  try {
    tags.value = await fetchTags()
  } catch {
    // 标签读取失败不影响列表浏览
  }
}

function resetAndLoad(): void {
  page.value = 1
  void loadList(1)
}

function clearTags(): void {
  if (selectedTags.value.length === 0) return
  selectedTags.value = []
  // 清空标签后回到第一页重新取数，等无筛选结果回来再恢复焦点区。
  resetAndLoad()
}

function toggleTag(name: string): void {
  const index = selectedTags.value.indexOf(name)
  if (index >= 0) {
    selectedTags.value = selectedTags.value.filter((tag) => tag !== name)
  } else {
    selectedTags.value = [...selectedTags.value, name]
  }
  resetAndLoad()
}

function clearFilters(): void {
  keyword.value = ''
  selectedTags.value = []
  resetAndLoad()
}

async function openDetail(id: number): Promise<void> {
  drawerOpen.value = true
  detailId.value = id
  viewsNotice.value = ''
  void loadDetail()
}

async function loadDetail(): Promise<void> {
  if (detailId.value === null) return
  const token = detailGuard.begin()
  detailLoading.value = true
  detailError.value = ''
  detail.value = null
  try {
    const fetched = await fetchPostDetail(detailId.value)
    if (!detailGuard.isCurrent(token)) return
    detail.value = fetched
  } catch (cause) {
    if (!detailGuard.isCurrent(token)) return
    detailError.value = messageOf(cause)
  } finally {
    if (detailGuard.isCurrent(token)) detailLoading.value = false
  }
}

// scheduleViewsNotice 让刷新反馈短暂出现后自动消失。
function scheduleViewsNotice(): void {
  if (viewsNoticeTimer !== undefined) clearTimeout(viewsNoticeTimer)
  viewsNoticeTimer = setTimeout(() => {
    viewsNotice.value = ''
  }, 1800)
}

// refreshViews 手动刷新当前详情帖的浏览量，切帖后旧响应不再回写。
async function refreshViews(): Promise<void> {
  const current = detail.value
  if (current === null || viewsRefreshing.value) return
  const id = current.id
  viewsRefreshing.value = true
  viewsNotice.value = ''
  try {
    const result = await refreshPostViews(id)
    if (detail.value?.id === id) {
      detail.value = {
        ...detail.value,
        views_count: result.views_count,
        views_updated_at: result.views_updated_at,
      }
      // 失败后的冷却命中不能声称「刚刚已更新」，只提示显示的是缓存。
      viewsNotice.value = result.failed ? '暂未更新' : result.cached ? '刚刚已更新' : '已更新'
      viewsNoticeTone.value = result.failed || result.cached ? 'muted' : 'ok'
      scheduleViewsNotice()
    }
    items.value = items.value.map((item) =>
      item.id === id
        ? { ...item, views_count: result.views_count, views_updated_at: result.views_updated_at }
        : item,
    )
  } catch (cause) {
    if (detail.value?.id === id) {
      viewsNotice.value = messageOf(cause)
      viewsNoticeTone.value = 'muted'
      scheduleViewsNotice()
    }
  } finally {
    viewsRefreshing.value = false
  }
}

function onToggled(): void {
  void loadDetail()
  void loadList(page.value)
}

async function onDelete(): Promise<void> {
  if (detail.value === null || deleting.value) return
  deleting.value = true
  deleteError.value = ''
  try {
    await deletePost(detail.value.id)
    closeDetail()
    await loadList(page.value)
  } catch (cause) {
    // 服务端已区分远端撤回失败与本地文件清理失败，两种都是 502，按原文展示。
    deleteError.value =
      cause instanceof ApiError
        ? deleteFailureText(cause.status, cause.message)
        : '删除失败，请稍后重试'
    // 失败或部分完成后刷新抽屉与列表，让待删除状态和短码停用立即可见。
    await loadDetail()
    await loadList(page.value)
  } finally {
    deleting.value = false
  }
}

function onSaved(): void {
  // 保存成功后刷新详情与列表，标题与发布时间可能变化。
  void loadDetail()
  void loadList(page.value)
}

function closeDetail(): void {
  // 关闭后作废在途请求，避免旧详情回填到下一次打开。
  detailGuard.begin()
  drawerOpen.value = false
  detail.value = null
  detailError.value = ''
  deleteError.value = ''
  viewsNotice.value = ''
  if (viewsNoticeTimer !== undefined) {
    clearTimeout(viewsNoticeTimer)
    viewsNoticeTimer = undefined
  }
}

watch(keyword, () => {
  if (searchTimer !== undefined) clearTimeout(searchTimer)
  searchTimer = setTimeout(() => {
    resetAndLoad()
  }, 250)
})

onMounted(() => {
  viewMode.value = readViewPreference(window.localStorage)
  void loadTags()
  void loadList()
})

onBeforeUnmount(() => {
  if (searchTimer !== undefined) clearTimeout(searchTimer)
  if (viewsNoticeTimer !== undefined) clearTimeout(viewsNoticeTimer)
})
</script>

<template>
  <div class="min-h-full">
    <AppHeader />

    <main class="mx-auto w-full max-w-[1120px] px-5 py-7">
      <div class="flex flex-wrap items-center gap-x-3 gap-y-2">
        <h1 class="text-2xl font-semibold tracking-tight sm:text-3xl">图集</h1>
        <p class="rounded-md border border-line bg-surface px-2.5 py-0.5 text-xs text-muted tnum">
          共 {{ total }} 组<template v-if="hasFilter"> · 已筛选</template>
        </p>
        <button
          v-if="hasFilter"
          type="button"
          class="text-xs font-medium text-danger underline-offset-4 hover:underline"
          @click="clearFilters"
        >
          清除筛选
        </button>

        <div class="flex w-full flex-wrap items-center gap-2 sm:ml-auto sm:w-auto sm:flex-nowrap">
          <label class="relative w-full min-w-0 sm:w-[260px] sm:flex-none">
            <svg
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              stroke-width="1.75"
              class="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted"
              aria-hidden="true"
            >
              <circle cx="11" cy="11" r="6.5" />
              <path d="M16 16l4 4" stroke-linecap="round" />
            </svg>
            <input
              v-model="keyword"
              type="search"
              aria-label="搜索图集"
              placeholder="搜索标题或标签"
              class="h-11 w-full rounded-xl border border-line bg-surface pl-9 pr-3 text-sm outline-none transition focus:border-ink/40 focus:shadow-[0_2px_0_0_rgba(27,26,23,0.06)]"
            />
          </label>

          <div class="flex w-full items-center gap-2 sm:w-auto">
            <router-link
              :to="{ name: 'workbench' }"
              class="inline-flex h-11 flex-1 shrink-0 items-center justify-center rounded-xl bg-brand px-4 text-sm font-medium text-brand-ink transition hover:bg-brand-strong sm:flex-none"
            >
              新建图集
            </router-link>
          </div>
        </div>
      </div>

      <div class="mt-4 flex min-w-0 items-center gap-2">
        <TagFilter class="min-w-0 flex-1" :tags="tags" :selected="selectedTags" @toggle="toggleTag" @clear="clearTags" />
        <div class="flex shrink-0 items-center rounded-xl border border-line/70 bg-canvas p-1" role="group" aria-label="视图切换">
          <button
            type="button"
            class="inline-flex h-10 items-center gap-1.5 rounded-lg px-2.5 text-xs font-medium transition sm:h-9 sm:px-3"
            :class="viewMode === 'gallery' ? 'bg-surface text-ink shadow-[0_1px_0_0_rgba(27,26,23,0.08)]' : 'text-muted hover:text-ink'"
            :aria-pressed="viewMode === 'gallery'"
            aria-label="画廊视图"
            @click="setViewMode('gallery')"
          >
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" class="h-4 w-4" aria-hidden="true">
              <rect x="3.5" y="3.5" width="7" height="7" rx="1.5" />
              <rect x="13.5" y="3.5" width="7" height="7" rx="1.5" />
              <rect x="3.5" y="13.5" width="7" height="7" rx="1.5" />
              <rect x="13.5" y="13.5" width="7" height="7" rx="1.5" />
            </svg>
            <span class="hidden sm:inline">画廊</span>
          </button>
          <button
            type="button"
            class="inline-flex h-10 items-center gap-1.5 rounded-lg px-2.5 text-xs font-medium transition sm:h-9 sm:px-3"
            :class="viewMode === 'list' ? 'bg-surface text-ink shadow-[0_1px_0_0_rgba(27,26,23,0.08)]' : 'text-muted hover:text-ink'"
            :aria-pressed="viewMode === 'list'"
            aria-label="列表视图"
            @click="setViewMode('list')"
          >
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" class="h-4 w-4" aria-hidden="true">
              <path d="M4 6h16M4 12h16M4 18h16" stroke-linecap="round" />
            </svg>
            <span class="hidden sm:inline">列表</span>
          </button>
        </div>
      </div>

      <section class="mt-5">
        <div v-if="loading" class="grid grid-cols-2 gap-3.5 md:grid-cols-3 md:gap-4 lg:grid-cols-4">
          <div v-for="index in 8" :key="index" class="flex flex-col gap-2.5">
            <div class="aspect-[4/3] w-full animate-pulse rounded-2xl bg-canvas"></div>
            <div class="h-4 w-3/4 animate-pulse rounded bg-canvas"></div>
            <div class="h-3 w-1/2 animate-pulse rounded bg-canvas"></div>
          </div>
        </div>

        <div v-else-if="error !== ''" class="rounded-2xl bg-surface px-6 py-12 text-center shadow-[0_1px_0_0_rgba(27,26,23,0.05)]">
          <p class="text-sm text-danger">{{ error }}</p>
          <button
            type="button"
            class="mt-3 h-11 rounded-xl px-4 text-sm font-medium text-muted transition hover:bg-canvas hover:text-ink"
            @click="loadList()"
          >
            重新加载
          </button>
        </div>

        <div
          v-else-if="items.length === 0"
          class="rounded-2xl bg-surface px-6 py-16 text-center shadow-[0_1px_0_0_rgba(27,26,23,0.05)]"
        >
          <div class="mx-auto flex h-12 w-12 items-center justify-center rounded bg-canvas" aria-hidden="true">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" class="h-6 w-6 text-muted">
              <rect x="3" y="4" width="18" height="16" rx="2" />
              <path d="M3 15l5-4 4 3 3-2 6 4" stroke-linecap="round" stroke-linejoin="round" />
              <circle cx="9" cy="9" r="1.5" />
            </svg>
          </div>
          <h2 class="mt-4 text-base font-semibold">{{ hasFilter ? '没有匹配的图集' : '还没有图集' }}</h2>
          <p class="mx-auto mt-2 max-w-[42ch] text-sm text-muted">
            {{
              hasFilter
                ? '换个关键词，或减少标签筛选后再试。'
                : '点右上角「新建图集」进入发布工作台，存图完成后图集会出现在这里。'
            }}
          </p>
          <button
            v-if="hasFilter"
            type="button"
            class="mt-4 h-11 rounded-xl px-4 text-sm font-medium text-muted transition hover:bg-canvas hover:text-ink"
            @click="clearFilters"
          >
            清除筛选
          </button>
        </div>

        <template v-else>
          <FocusCarousel
            v-if="showFocus"
            :items="focusItems"
            :blurred="blurEnabled"
            class="mt-4"
            @open="openDetail"
          />

          <div v-if="viewMode === 'gallery'" class="mt-6 grid grid-cols-2 gap-3.5 md:grid-cols-3 md:gap-4 lg:grid-cols-4">
            <PostCard
              v-for="item in items"
              :key="item.id"
              :item="item"
              :blurred="blurEnabled"
              @open="openDetail"
            />
          </div>

          <PostList
            v-else
            class="mt-4"
            :items="items"
            :blurred="blurEnabled"
            @open="openDetail"
          />

          <nav v-if="totalPages > 1" class="mt-8 flex items-center justify-center gap-1.5" aria-label="分页">
            <button
              type="button"
              class="inline-flex h-11 shrink-0 items-center justify-center gap-1 rounded-lg border border-line px-2.5 text-sm transition hover:bg-surface disabled:opacity-50 sm:h-8 sm:px-2"
              :disabled="page <= 1"
              aria-label="上一页"
              @click="loadList(page - 1)"
            >
              <svg class="h-3.5 w-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M15 18l-6-6 6-6" /></svg>
              <span class="hidden sm:inline">上一页</span>
            </button>
            <button
              v-for="target in narrowPages"
              :key="'narrow-' + target"
              type="button"
              class="inline-flex h-11 w-11 shrink-0 items-center justify-center rounded-lg text-sm transition tnum sm:hidden"
              :class="
                target === page
                  ? 'border border-ink bg-ink text-brand-ink'
                  : 'border border-line hover:bg-surface'
              "
              :aria-current="target === page ? 'page' : undefined"
              @click="loadList(target)"
            >
              {{ target }}
            </button>
            <button
              v-for="target in pages"
              :key="'wide-' + target"
              type="button"
              class="hidden h-8 min-w-8 shrink-0 items-center justify-center rounded-lg px-2 text-xs transition tnum sm:inline-flex"
              :class="
                target === page
                  ? 'border border-ink bg-ink text-brand-ink'
                  : 'border border-line hover:bg-surface'
              "
              :aria-current="target === page ? 'page' : undefined"
              @click="loadList(target)"
            >
              {{ target }}
            </button>
            <button
              type="button"
              class="inline-flex h-11 shrink-0 items-center justify-center gap-1 rounded-lg border border-line px-2.5 text-sm transition hover:bg-surface disabled:opacity-50 sm:h-8 sm:px-2"
              :disabled="page >= totalPages"
              aria-label="下一页"
              @click="loadList(page + 1)"
            >
              <span class="hidden sm:inline">下一页</span>
              <svg class="h-3.5 w-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M9 6l6 6-6 6" /></svg>
            </button>
          </nav>
        </template>
      </section>
    </main>

    <DetailDrawer
      v-if="drawerOpen"
      :detail="detail"
      :blurred="blurEnabled"
      :loading="detailLoading"
      :error="detailError"
      :views-refreshing="viewsRefreshing"
      :views-notice="viewsNotice"
      :views-notice-tone="viewsNoticeTone"
      :deleting="deleting"
      :delete-error="deleteError"
      @delete="onDelete"
      @refresh-views="refreshViews"
      @close="closeDetail"
      @retry="loadDetail"
      @saved="onSaved"
      @toggled="onToggled"
    />

    <BackToTop />
  </div>
</template>
