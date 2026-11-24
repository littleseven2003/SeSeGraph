<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { ApiError } from '../api/client.ts'
import { fetchTasks, pauseTask, resumeTask, stopTask, type TaskFilter, type TaskRow } from '../api/posts.ts'
import { formatPublishedAt } from '../utils/gallery.ts'
import {
  TASK_FILTERS,
  badgeOf,
  canContinueUpload,
  canPause,
  canResume,
  canStop,
  channelStage,
  publishStage,
  uploadStage,
  type TaskTone,
} from '../utils/tasks.ts'

const tasks = ref<TaskRow[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = 20
const filter = ref<TaskFilter>('')
const loading = ref(false)
const error = ref('')
const notice = ref('')
const busyId = ref(0)
let timer: ReturnType<typeof setInterval> | undefined
// 请求序号用于丢弃迟到的响应：切筛选或翻页后，旧轮询不会覆盖新结果。
let requestSeq = 0

const pages = computed(() => Math.max(1, Math.ceil(total.value / pageSize)))
const filtered = computed(() => filter.value !== '')
const filterLabel = computed(() => TASK_FILTERS.find((item) => item.value === filter.value)?.label ?? '全部')

const BADGE_CLASS: Record<TaskTone, string> = {
  run: 'bg-brand-soft text-brand-strong',
  warn: 'bg-warn-soft text-warn',
  danger: 'bg-danger-soft text-danger',
  ok: 'bg-accent-soft text-accent',
  muted: 'bg-canvas text-muted',
}

const STAGE_VALUE_CLASS: Record<TaskTone, string> = {
  run: 'text-brand-strong',
  warn: 'text-warn',
  danger: 'text-danger',
  ok: 'text-accent',
  muted: 'text-muted',
}

async function load(): Promise<void> {
  const seq = (requestSeq += 1)
  loading.value = true
  error.value = ''
  try {
    const result = await fetchTasks(page.value, pageSize, filter.value)
    // 只有最后一次请求可以写入，避免旧筛选或旧页码的结果盖掉新状态。
    if (seq !== requestSeq) return
    tasks.value = result.items
    total.value = result.total
    if (result.page !== page.value) page.value = result.page
  } catch (cause) {
    if (seq !== requestSeq) return
    error.value = cause instanceof ApiError ? cause.message : '读取任务失败，请稍后重试'
  } finally {
    if (seq === requestSeq) loading.value = false
  }
}

function setFilter(next: TaskFilter): void {
  if (filter.value === next) return
  filter.value = next
  // 换筛选回到第一页，避免停在旧页码上看到空白。
  page.value = 1
  notice.value = ''
  void load()
}

async function control(task: TaskRow, action: 'pause' | 'resume' | 'stop'): Promise<void> {
  if (busyId.value !== 0) return
  busyId.value = task.id
  error.value = ''
  notice.value = ''
  try {
    if (action === 'pause') await pauseTask(task.id)
    else if (action === 'resume') await resumeTask(task.id)
    else await stopTask(task.id)
    notice.value = action === 'pause' ? '任务已暂停' : action === 'resume' ? '任务已继续' : '任务已停止'
    await load()
  } catch (cause) {
    error.value = cause instanceof ApiError ? cause.message : '任务操作失败，请稍后重试'
  } finally {
    busyId.value = 0
  }
}

function updatedText(value: string): string {
  return formatPublishedAt(value) || value
}

function go(delta: number): void {
  const next = page.value + delta
  if (next < 1 || next > pages.value) return
  page.value = next
  void load()
}

onMounted(() => {
  void load()
  timer = setInterval(() => void load(), 10_000)
})

onBeforeUnmount(() => {
  if (timer !== undefined) clearInterval(timer)
})
</script>

<template>
  <div class="min-h-full">
    <header class="sticky top-0 z-20 border-b border-line bg-surface">
      <div class="mx-auto flex w-full max-w-[1240px] items-center justify-between gap-4 px-5 py-3">
        <div class="flex min-w-0 items-center gap-1.5">
          <router-link
            :to="{ name: 'home' }"
            class="-ml-2 flex h-11 w-11 shrink-0 items-center justify-center rounded-md text-muted transition hover:bg-canvas hover:text-ink"
            aria-label="返回图集"
          >
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" class="h-4 w-4">
              <path d="M15 5l-7 7 7 7" stroke-linecap="round" stroke-linejoin="round" />
            </svg>
          </router-link>
          <div class="min-w-0">
            <p class="truncate text-sm font-semibold tracking-tight">任务</p>
            <p class="mt-0.5 truncate text-xs text-muted">上传 · 存频道 · 发布</p>
          </div>
        </div>
        <router-link
          :to="{ name: 'workbench' }"
          class="inline-flex h-11 shrink-0 items-center rounded-full bg-brand px-4 text-sm font-medium text-brand-ink transition hover:bg-brand-strong"
        >
          新建图集
        </router-link>
      </div>
    </header>

    <main class="mx-auto w-full max-w-[960px] px-5 py-7">
      <div class="flex flex-wrap items-center gap-x-3 gap-y-2">
        <h1 class="text-2xl font-semibold tracking-tight sm:text-3xl">任务</h1>
        <p class="tnum rounded-full border border-line bg-surface px-2.5 py-0.5 text-xs text-muted">
          共 {{ total }} 个<template v-if="filtered"> · {{ filterLabel }}</template>
        </p>
        <div class="flex w-full items-center justify-between gap-2 sm:ml-auto sm:w-auto">
          <p class="text-xs text-muted">每 10 秒刷新一次</p>
          <button
            type="button"
            class="inline-flex h-11 shrink-0 items-center rounded-full border border-line bg-surface px-3.5 text-sm font-medium text-muted transition hover:bg-canvas hover:text-ink disabled:opacity-60"
            :disabled="loading"
            @click="load"
          >
            {{ loading ? '刷新中…' : '刷新' }}
          </button>
        </div>
      </div>

      <div class="mt-3 max-w-full overflow-x-auto overscroll-x-contain">
        <div class="flex min-w-max items-center gap-1 rounded-full bg-canvas p-1" role="group" aria-label="按状态筛选任务">
          <button
            v-for="item in TASK_FILTERS"
            :key="item.value"
            type="button"
            class="inline-flex h-11 shrink-0 items-center rounded-full px-3.5 text-xs font-medium transition"
            :class="filter === item.value ? 'bg-surface text-ink shadow-[0_1px_0_0_rgba(27,26,23,0.08)]' : 'text-muted hover:text-ink'"
            :aria-pressed="filter === item.value"
            @click="setFilter(item.value)"
          >
            {{ item.label }}
          </button>
        </div>
      </div>

      <p v-if="error !== ''" class="mt-3 text-xs text-danger">{{ error }}</p>
      <p v-else-if="notice !== ''" class="mt-3 text-xs text-accent">{{ notice }}</p>

      <div v-if="tasks.length === 0 && !loading" class="mt-8 rounded-2xl bg-surface px-6 py-12 text-center">
        <span class="mx-auto flex h-12 w-12 items-center justify-center rounded-xl bg-canvas text-muted" aria-hidden="true">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" class="h-5 w-5">
            <path d="M4 7h16M4 12h16M4 17h10" stroke-linecap="round" />
          </svg>
        </span>
        <p class="mt-3 text-sm font-semibold">{{ filtered ? '当前筛选没有任务' : '暂无任务' }}</p>
        <p class="mt-1 text-xs text-muted">
          {{ filtered ? '换个筛选看看，或回到全部任务。' : '在工作台选好图片后提交，任务会出现在这里。' }}
        </p>
        <div class="mt-4 flex flex-wrap items-center justify-center gap-2">
          <button
            v-if="filtered"
            type="button"
            class="inline-flex h-11 items-center rounded-full border border-line bg-surface px-4 text-sm font-medium text-muted transition hover:bg-canvas hover:text-ink"
            @click="setFilter('')"
          >
            查看全部
          </button>
          <router-link
            :to="{ name: 'workbench' }"
            class="inline-flex h-11 items-center rounded-full bg-brand px-4 text-sm font-medium text-brand-ink transition hover:bg-brand-strong"
          >
            新建图集
          </router-link>
        </div>
      </div>

      <ul v-else class="mt-4 space-y-3">
        <li
          v-for="task in tasks"
          :key="task.id"
          class="rounded-2xl bg-surface p-4 shadow-[0_1px_0_0_rgba(27,26,23,0.05)] sm:px-5"
        >
          <div class="flex flex-wrap items-start justify-between gap-2">
            <div class="min-w-0 flex-1">
              <p class="truncate text-sm font-semibold" :title="task.title">
                <span class="tnum mr-1.5 text-muted">#{{ task.id }}</span>{{ task.title }}
              </p>
              <p class="mt-0.5 text-xs text-muted">更新于 {{ updatedText(task.updated_at) }}</p>
            </div>
            <span
              class="shrink-0 whitespace-nowrap rounded-full px-2.5 py-1 text-xs font-medium"
              :class="BADGE_CLASS[badgeOf(task).tone]"
            >
              {{ badgeOf(task).label }}
            </span>
          </div>

          <dl class="mt-3 grid grid-cols-1 gap-2 text-xs sm:grid-cols-3">
            <div class="rounded-xl bg-canvas px-3 py-2.5">
              <dt class="text-muted"><span class="tnum mr-1 text-muted/80">01</span>传至服务器</dt>
              <dd class="tnum mt-0.5 font-medium" :class="STAGE_VALUE_CLASS[uploadStage(task).tone]">{{ uploadStage(task).value }}</dd>
            </div>
            <div class="rounded-xl bg-canvas px-3 py-2.5">
              <dt class="text-muted"><span class="tnum mr-1 text-muted/80">02</span>存入频道</dt>
              <dd class="tnum mt-0.5 font-medium" :class="STAGE_VALUE_CLASS[channelStage(task).tone]">{{ channelStage(task).value }}</dd>
            </div>
            <div class="rounded-xl bg-canvas px-3 py-2.5">
              <dt class="text-muted"><span class="tnum mr-1 text-muted/80">03</span>发布 Telegraph</dt>
              <dd class="mt-0.5 font-medium" :class="STAGE_VALUE_CLASS[publishStage(task).tone]">{{ publishStage(task).value }}</dd>
            </div>
          </dl>

          <div class="mt-3 flex flex-wrap items-center gap-2">
            <button
              v-if="canResume(task)"
              type="button"
              class="inline-flex h-11 items-center rounded-full border border-brand/30 bg-brand-soft px-4 text-xs font-medium text-brand-strong transition hover:border-brand/50 disabled:opacity-60"
              :disabled="busyId !== 0"
              @click="control(task, 'resume')"
            >
              继续
            </button>
            <router-link
              v-else-if="canContinueUpload(task)"
              :to="{ name: 'workbench', query: { resume: task.id } }"
              class="inline-flex h-11 items-center rounded-full border border-line bg-surface px-4 text-xs font-medium text-muted transition hover:bg-canvas hover:text-ink"
            >
              继续上传
            </router-link>
            <button
              v-if="canPause(task)"
              type="button"
              class="inline-flex h-11 items-center rounded-full border border-line bg-surface px-4 text-xs font-medium text-muted transition hover:bg-canvas hover:text-ink disabled:opacity-60"
              :disabled="busyId !== 0"
              @click="control(task, 'pause')"
            >
              暂停
            </button>
            <button
              v-if="canStop(task)"
              type="button"
              class="inline-flex h-11 items-center rounded-full border border-danger/35 bg-surface px-4 text-xs font-medium text-danger transition hover:bg-danger-soft disabled:opacity-60"
              :disabled="busyId !== 0"
              @click="control(task, 'stop')"
            >
              停止
            </button>
            <p v-if="task.control_state === 'paused'" class="text-xs text-brand-strong">已暂停，继续只补缺项</p>
            <p v-else-if="task.control_state === 'stopped'" class="text-xs text-danger">已停止，不再上传或发布</p>
          </div>
        </li>
      </ul>

      <div v-if="pages > 1" class="mt-4 flex items-center justify-between text-xs text-muted">
        <button
          type="button"
          class="inline-flex h-11 items-center rounded-full border border-line bg-surface px-4 text-xs font-medium transition hover:bg-canvas hover:text-ink disabled:opacity-40"
          :disabled="page <= 1"
          @click="go(-1)"
        >
          上一页
        </button>
        <span class="tnum">{{ page }} / {{ pages }}</span>
        <button
          type="button"
          class="inline-flex h-11 items-center rounded-full border border-line bg-surface px-4 text-xs font-medium transition hover:bg-canvas hover:text-ink disabled:opacity-40"
          :disabled="page >= pages"
          @click="go(1)"
        >
          下一页
        </button>
      </div>
    </main>
  </div>
</template>
