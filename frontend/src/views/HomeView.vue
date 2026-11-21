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
import { pageWindow, readBlurPreference, saveBlurPreference } from '../utils/gallery'
import DetailDrawer from '../components/gallery/DetailDrawer.vue'
import PostCard from '../components/gallery/PostCard.vue'

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

const signingOut = ref(false)
const drawerOpen = ref(false)
const detail = ref<PostDetail | null>(null)
const detailLoading = ref(false)
const detailError = ref('')
const detailId = ref<number | null>(null)

let searchTimer: ReturnType<typeof setTimeout> | undefined

const totalPages = computed(() => Math.max(1, Math.ceil(total.value / PAGE_SIZE)))
const pages = computed(() => pageWindow(page.value, PAGE_SIZE, total.value))
const hasFilter = computed(() => keyword.value.trim() !== '' || selectedTags.value.length > 0)

function messageOf(cause: unknown): string {
  if (cause instanceof ApiError) return cause.message
  return '网络异常，请稍后重试'
}

async function loadList(target = page.value): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    const result = await fetchGallery({
      q: keyword.value,
      tags: selectedTags.value,
      page: target,
      pageSize: PAGE_SIZE,
    })
    items.value = result.items
    total.value = result.total
    page.value = result.page
  } catch (cause) {
    error.value = messageOf(cause)
  } finally {
    loading.value = false
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
  detailLoading.value = true
  detailError.value = ''
  detail.value = null
  try {
    detail.value = await fetchPostDetail(detailId.value)
  } catch (cause) {
    detailError.value = messageOf(cause)
  } finally {
    detailLoading.value = false
  }
}

function closeDetail(): void {
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
  void loadTags()
  void loadList()
})

onBeforeUnmount(() => {
  if (searchTimer !== undefined) clearTimeout(searchTimer)
})
</script>

<template>
  <div class="min-h-full">
    <header class="border-b border-line bg-surface">
      <div class="mx-auto flex w-full max-w-[1180px] items-center justify-between gap-4 px-4 py-3">
        <div class="flex items-center gap-3">
          <span
            class="flex h-9 w-9 items-center justify-center rounded-lg bg-brand text-sm font-semibold text-brand-ink"
            aria-hidden="true"
            >S</span
          >
          <div>
            <p class="text-xs font-medium tracking-wide text-muted">SeSeGraph</p>
            <p class="text-sm font-semibold">图集画廊</p>
          </div>
        </div>

        <div class="flex items-center gap-2">
          <router-link
            :to="{ name: 'workbench' }"
            class="hidden h-9 items-center rounded-md border border-line px-3 text-sm font-medium transition hover:bg-canvas sm:inline-flex"
          >
            发帖工作台
          </router-link>
          <button
            type="button"
            :disabled="signingOut"
            class="h-9 rounded-md border border-line px-3 text-sm font-medium transition hover:border-muted/40 hover:bg-canvas disabled:opacity-60"
            @click="signOut"
          >
            {{ signingOut ? '正在退出…' : '退出登录' }}
          </button>
        </div>
      </div>
    </header>

    <main class="mx-auto w-full max-w-[1180px] px-4 py-7">
      <div class="flex flex-wrap items-end justify-between gap-4">
        <div>
          <h1 class="text-xl font-semibold tracking-tight">图集</h1>
          <p class="mt-1 text-sm text-muted tnum">
            共 {{ total }} 个图集<template v-if="hasFilter"> · 已筛选</template>
          </p>
        </div>
        <router-link
          :to="{ name: 'workbench' }"
          class="inline-flex h-10 items-center rounded-md bg-brand px-4 text-sm font-medium text-brand-ink transition hover:bg-brand-strong"
        >
          新建图集
        </router-link>
      </div>

      <section class="mt-5 rounded-lg border border-line bg-surface px-4 py-3">
        <div class="flex flex-wrap items-center gap-3">
          <label class="relative min-w-0 flex-1 sm:max-w-[360px]">
            <span class="sr-only">搜索图集</span>
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
              placeholder="搜索标题、说明或标签"
              class="h-10 w-full rounded-md border border-line bg-canvas pl-9 pr-3 text-sm outline-none transition focus:border-brand focus:bg-surface"
            />
          </label>

          <button
            type="button"
            class="flex h-10 shrink-0 items-center gap-2 rounded-md border border-line px-3 text-sm font-medium transition hover:bg-canvas"
            :aria-pressed="blurEnabled"
            @click="toggleBlur"
          >
            <span
              class="relative inline-flex h-4 w-7 shrink-0 items-center rounded-full transition"
              :class="blurEnabled ? 'bg-brand' : 'bg-line'"
              aria-hidden="true"
            >
              <span
                class="absolute h-3 w-3 rounded-full bg-white transition"
                :class="blurEnabled ? 'left-3.5' : 'left-0.5'"
              ></span>
            </span>
            防社死模糊
          </button>

          <button
            v-if="hasFilter"
            type="button"
            class="h-10 shrink-0 rounded-md px-2 text-sm text-muted transition hover:text-ink"
            @click="clearFilters"
          >
            清除筛选
          </button>
        </div>

        <div v-if="tags.length > 0" class="mt-3 flex flex-wrap items-center gap-1.5 border-t border-line pt-3">
          <span class="mr-1 text-xs text-muted">标签</span>
          <button
            v-for="tag in tags"
            :key="tag.name"
            type="button"
            class="h-8 rounded-md px-2.5 text-xs font-medium transition"
            :class="
              selectedTags.includes(tag.name)
                ? 'bg-brand text-brand-ink'
                : 'border border-line text-muted hover:border-muted/40 hover:text-ink'
            "
            :aria-pressed="selectedTags.includes(tag.name)"
            @click="toggleTag(tag.name)"
          >
            {{ tag.name }}<span class="ml-1 opacity-70 tnum">{{ tag.count }}</span>
          </button>
        </div>
      </section>

      <section class="mt-5">
        <div v-if="loading" class="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
          <div v-for="index in 8" :key="index" class="overflow-hidden rounded-lg border border-line bg-surface">
            <div class="aspect-[4/3] w-full animate-pulse bg-canvas"></div>
            <div class="space-y-2 px-3 py-3">
              <div class="h-4 w-3/4 animate-pulse rounded bg-canvas"></div>
              <div class="h-3 w-1/2 animate-pulse rounded bg-canvas"></div>
            </div>
          </div>
        </div>

        <div v-else-if="error !== ''" class="rounded-lg border border-line bg-surface px-6 py-12 text-center">
          <p class="text-sm text-danger">{{ error }}</p>
          <button
            type="button"
            class="mt-3 h-9 rounded-md border border-line px-3 text-sm font-medium transition hover:bg-canvas"
            @click="loadList()"
          >
            重新加载
          </button>
        </div>

        <div
          v-else-if="items.length === 0"
          class="rounded-lg border border-dashed border-line bg-surface px-6 py-14 text-center"
        >
          <div class="mx-auto flex h-12 w-12 items-center justify-center rounded-lg bg-canvas" aria-hidden="true">
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
            class="mt-4 h-9 rounded-md border border-line px-3 text-sm font-medium transition hover:bg-canvas"
            @click="clearFilters"
          >
            清除筛选
          </button>
        </div>

        <template v-else>
          <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
            <PostCard
              v-for="item in items"
              :key="item.id"
              :item="item"
              :blurred="blurEnabled"
              @open="openDetail"
            />
          </div>

          <nav v-if="totalPages > 1" class="mt-6 flex items-center justify-center gap-1.5" aria-label="分页">
            <button
              type="button"
              class="h-9 rounded-md border border-line px-3 text-sm transition hover:bg-canvas disabled:opacity-50"
              :disabled="page <= 1"
              @click="loadList(page - 1)"
            >
              上一页
            </button>
            <button
              v-for="target in pages"
              :key="target"
              type="button"
              class="h-9 w-9 rounded-md text-sm transition tnum"
              :class="target === page ? 'bg-brand text-brand-ink' : 'border border-line hover:bg-canvas'"
              :aria-current="target === page ? 'page' : undefined"
              @click="loadList(target)"
            >
              {{ target }}
            </button>
            <button
              type="button"
              class="h-9 rounded-md border border-line px-3 text-sm transition hover:bg-canvas disabled:opacity-50"
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
    />
  </div>
</template>
