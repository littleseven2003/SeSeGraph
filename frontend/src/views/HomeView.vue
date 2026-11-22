<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { ApiError, logout } from '../api/client'
import {
  fetchGallery,
  fetchPostDetail,
  fetchTags,
  type GalleryItem,
  type PostDetail,
  type TagCount,
} from '../api/gallery'
import {
  createLatestGuard,
  pageWindow,
  readBlurPreference,
  readViewPreference,
  saveBlurPreference,
  saveViewPreference,
  type GalleryView,
} from '../utils/gallery'
import DetailDrawer from '../components/gallery/DetailDrawer.vue'
import BrandMark from '../components/BrandMark.vue'
import PostCard from '../components/gallery/PostCard.vue'
import PostList from '../components/gallery/PostList.vue'
import FocusCarousel from '../components/gallery/FocusCarousel.vue'
import TagFilter from '../components/gallery/TagFilter.vue'

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
const blurEnabled = ref(true)
const viewMode = ref<GalleryView>('gallery')

const signingOut = ref(false)
const drawerOpen = ref(false)
const detail = ref<PostDetail | null>(null)
const detailLoading = ref(false)
const detailError = ref('')
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
  () => viewMode.value === 'gallery' && !hasFilter.value && page.value === 1 && items.value.length > 0,
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
  selectedTags.value = []
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

function toggleBlur(): void {
  blurEnabled.value = !blurEnabled.value
  saveBlurPreference(window.localStorage, blurEnabled.value)
}

async function openDetail(id: number): Promise<void> {
  drawerOpen.value = true
  detailId.value = id
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

function onToggled(): void {
  void loadDetail()
  void loadList(page.value)
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
}

async function signOut(): Promise<void> {
  if (signingOut.value) return
  signingOut.value = true
  try {
    await logout()
  } catch {
    // 登出失败也回到登录页，避免停留在已失效的界面
  }
  signingOut.value = false
  await router.replace({ name: 'login' })
}

watch(keyword, () => {
  if (searchTimer !== undefined) clearTimeout(searchTimer)
  searchTimer = setTimeout(() => {
    resetAndLoad()
  }, 250)
})

onMounted(() => {
  blurEnabled.value = readBlurPreference(window.localStorage)
  viewMode.value = readViewPreference(window.localStorage)
  void loadTags()
  void loadList()
})

onBeforeUnmount(() => {
  if (searchTimer !== undefined) clearTimeout(searchTimer)
})
</script>

<template>
  <div class="min-h-full">
    <header class="border-b border-line/60">
      <div class="mx-auto flex w-full max-w-[1240px] items-center justify-between gap-4 px-5 py-3">
        <div class="flex min-w-0 items-center gap-2.5">
          <BrandMark :width="132" />
          <span class="hidden h-4 w-px shrink-0 bg-line sm:block" aria-hidden="true"></span>
          <span class="hidden shrink-0 text-xs font-medium tracking-[0.04em] text-muted sm:inline">SeSe图册</span>
        </div>

        <div class="flex items-center gap-1">
          <router-link
            :to="{ name: 'settings' }"
            class="inline-flex h-11 items-center rounded-md px-3 text-sm text-muted transition hover:bg-surface hover:text-ink"
          >
            设置
          </router-link>
          <button
            type="button"
            :disabled="signingOut"
            class="inline-flex h-11 items-center rounded-md px-3 text-sm text-muted transition hover:bg-surface hover:text-ink disabled:opacity-60"
            @click="signOut"
          >
            {{ signingOut ? '正在退出…' : '退出登录' }}
          </button>
        </div>
      </div>
    </header>

    <main class="mx-auto w-full max-w-[1240px] px-5 py-7">
      <div class="flex flex-wrap items-center gap-x-3 gap-y-2">
        <h1 class="text-2xl font-semibold tracking-tight sm:text-3xl">图集</h1>
        <p class="rounded-full border border-line bg-surface px-2.5 py-0.5 text-xs text-muted tnum">
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
              class="h-11 w-full rounded-full border border-line bg-surface pl-9 pr-3 text-sm outline-none transition focus:border-ink/40 focus:shadow-[0_2px_0_0_rgba(27,26,23,0.06)]"
            />
          </label>

          <div class="flex w-full items-center gap-2 sm:w-auto">
            <button
              type="button"
              class="inline-flex h-11 flex-1 shrink-0 items-center justify-center gap-2 rounded-md border border-line bg-surface px-3 text-sm font-medium transition hover:border-ink/25 sm:flex-none"
              :aria-pressed="blurEnabled"
              @click="toggleBlur"
            >
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" class="h-4 w-4 shrink-0" aria-hidden="true">
                <path d="M2.5 12S6 6.5 12 6.5 21.5 12 21.5 12 18 17.5 12 17.5 2.5 12 2.5 12Z" stroke-linecap="round" />
                <circle cx="12" cy="12" r="2.6" />
                <path v-if="blurEnabled" d="M4 20L20 4" stroke-linecap="round" />
              </svg>
              <span
                class="relative inline-flex h-4 w-7 shrink-0 items-center rounded-full transition motion-safe:active:scale-95"
                :class="blurEnabled ? 'bg-accent' : 'bg-line'"
                aria-hidden="true"
              >
                <span
                  class="absolute h-3 w-3 rounded-full bg-white transition"
                  :class="blurEnabled ? 'left-3.5' : 'left-0.5'"
                ></span>
              </span>
              防社死模糊
            </button>

            <router-link
              :to="{ name: 'workbench' }"
              class="inline-flex h-11 flex-1 shrink-0 items-center justify-center rounded-full bg-brand px-4 text-sm font-medium text-brand-ink transition hover:bg-brand-strong sm:flex-none"
            >
              新建图集
            </router-link>
          </div>
        </div>
      </div>

      <TagFilter :tags="tags" :selected="selectedTags" @toggle="toggleTag" @clear="clearTags" />

      <section class="mt-5">
        <div v-if="loading" class="grid grid-cols-2 gap-4 md:grid-cols-3 md:gap-6">
          <div v-for="index in 7" :key="index" class="flex flex-col gap-2.5">
            <div class="aspect-[4/3] w-full animate-pulse rounded-2xl bg-canvas"></div>
            <div class="h-4 w-3/4 animate-pulse rounded bg-canvas"></div>
            <div class="h-3 w-1/2 animate-pulse rounded bg-canvas"></div>
          </div>
        </div>

        <div v-else-if="error !== ''" class="rounded-md border border-line bg-surface px-6 py-12 text-center">
          <p class="text-sm text-danger">{{ error }}</p>
          <button
            type="button"
            class="mt-3 h-11 rounded-md border border-line px-4 text-sm font-medium transition hover:bg-canvas"
            @click="loadList()"
          >
            重新加载
          </button>
        </div>

        <div
          v-else-if="items.length === 0"
          class="viewfinder-corners rounded-md border border-dashed border-line bg-surface px-6 py-16 text-center"
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
            class="mt-4 h-11 rounded-md border border-line px-4 text-sm font-medium transition hover:bg-canvas"
            @click="clearFilters"
          >
            清除筛选
          </button>
        </div>

        <template v-else>
          <div class="flex items-center justify-end gap-3 pb-1">
            <div class="flex rounded-full bg-canvas p-1" role="group" aria-label="视图切换">
              <button
                type="button"
                class="inline-flex h-11 items-center gap-1.5 rounded-full px-3.5 text-xs font-medium transition"
                :class="viewMode === 'gallery' ? 'bg-surface text-ink shadow-[0_1px_0_0_rgba(27,26,23,0.08)]' : 'text-muted hover:text-ink'"
                :aria-pressed="viewMode === 'gallery'"
                @click="setViewMode('gallery')"
              >
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" class="h-4 w-4" aria-hidden="true">
                  <rect x="3.5" y="3.5" width="7" height="7" rx="1.5" />
                  <rect x="13.5" y="3.5" width="7" height="7" rx="1.5" />
                  <rect x="3.5" y="13.5" width="7" height="7" rx="1.5" />
                  <rect x="13.5" y="13.5" width="7" height="7" rx="1.5" />
                </svg>
                画廊
              </button>
              <button
                type="button"
                class="inline-flex h-11 items-center gap-1.5 rounded-full px-3.5 text-xs font-medium transition"
                :class="viewMode === 'list' ? 'bg-surface text-ink shadow-[0_1px_0_0_rgba(27,26,23,0.08)]' : 'text-muted hover:text-ink'"
                :aria-pressed="viewMode === 'list'"
                @click="setViewMode('list')"
              >
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" class="h-4 w-4" aria-hidden="true">
                  <path d="M4 6h16M4 12h16M4 18h16" stroke-linecap="round" />
                </svg>
                列表
              </button>
            </div>
          </div>

          <FocusCarousel
            v-if="showFocus"
            :items="focusItems"
            :blurred="blurEnabled"
            class="mt-4"
            @open="openDetail"
          />

          <div v-if="viewMode === 'gallery'" class="mt-6 grid grid-cols-2 gap-4 md:grid-cols-3 md:gap-6">
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
              class="h-11 shrink-0 rounded-full border border-line px-3 text-sm transition hover:bg-surface disabled:opacity-50"
              :disabled="page <= 1"
              @click="loadList(page - 1)"
            >
              上一页
            </button>
            <button
              v-for="target in narrowPages"
              :key="'narrow-' + target"
              type="button"
              class="h-11 w-11 shrink-0 rounded-full text-sm transition tnum sm:hidden"
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
              class="hidden h-11 w-11 shrink-0 rounded-full text-sm transition tnum sm:inline-block"
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
              class="h-11 shrink-0 rounded-full border border-line px-3 text-sm transition hover:bg-surface disabled:opacity-50"
              :disabled="page >= totalPages"
              @click="loadList(page + 1)"
            >
              下一页
            </button>
          </nav>
        </template>
      </section>
    </main>

    <DetailDrawer
      v-if="drawerOpen"
      :detail="detail"
      :loading="detailLoading"
      :error="detailError"
      @close="closeDetail"
      @retry="loadDetail"
      @saved="onSaved"
      @toggled="onToggled"
    />
  </div>
</template>
