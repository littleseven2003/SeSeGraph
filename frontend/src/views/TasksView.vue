<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { ApiError } from '../api/client.ts'
import { fetchProgress, fetchTasks, pauseTask, resumeTask, retryPost, stopTask, type PoolStatus, type ServerProgress, type TaskFilter, type TaskRow } from '../api/posts.ts'
import { formatPublishedAt } from '../utils/gallery.ts'
import AppHeader from '../components/AppHeader.vue'
import BackToTop from '../components/BackToTop.vue'
import TaskStageBar from '../components/tasks/TaskStageBar.vue'
import {
  TASK_FILTERS,
  badgeOf,
  canContinueUpload,
  canPause,
  canResume,
  canStop,
  type TaskTone,
} from '../utils/tasks.ts'
import { stageInputOf, stageOverall, stagePercent, stageSlots, stageSummary, taskStages } from '../utils/stages.ts'

const tasks = ref<TaskRow[]>([])
const total = ref(0)
const page = ref(1)
// Bot 池状态：启用自动切换且有冻结条目时在列表上方提示。
const pool = ref<PoolStatus>({ enabled: false, frozen: [] })
const pageSize = 20
const filter = ref<TaskFilter>('')
const loading = ref(false)
const error = ref('')
const notice = ref('')
const busyId = ref(0)
// expanded 记录已展开的任务编号：默认只显示一行，展开后才出现四阶段进度与操作。
const expanded = ref<Set<number>>(new Set())
let timer: ReturnType<typeof setInterval> | undefined
// 请求序号用于丢弃迟到的响应：切筛选或翻页后，旧轮询不会覆盖新结果。
let requestSeq = 0
// 每页只保留一个在途请求；隐藏时停轮询，恢复可见立即补一次。
const POLL_MS = 2000
// requestLoad 统一调度所有入口：定时、切筛选、翻页、恢复可见共用一个在途请求。
let inFlight: Promise<void> | null = null
let queued: { silent: boolean } | null = null

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

// stagesOf 用统一口径把任务行转成四阶段：#1 压缩 → #2 上传 → #3 同步 → #4 发布。
function stagesOf(task: TaskRow) {
  return taskStages(stageInputOf(task))
}

function summaryOf(task: TaskRow): string {
  return stageSummary(stagesOf(task))
}

// overallOf 按适用阶段算总进度：旧任务缺压缩记录时不计该段，仅存图任务不计发布段。
function overallOf(task: TaskRow): number {
  return stageOverall(stagesOf(task), {
    publishIntent: task.publish_intent,
    publishState: task.publish_state,
    hasCompressRecord: task.compressed_count !== null,
  })
}

// SLOT_FILL 用字标四色区分阶段：压缩珊瑚、上传琥珀、同步松石、发布品红。
const SLOT_FILL: Record<string, string> = {
  compress: 'bg-stage-compress',
  upload: 'bg-stage-upload',
  sync: 'bg-stage-sync',
  publish: 'bg-stage-publish',
}

// slotsOf 把任务摊成固定四格：不适用的阶段保留槽位并标斜纹，列表纵向仍然对齐。
function slotsOf(task: TaskRow) {
  return stageSlots(stagesOf(task), {
    publishIntent: task.publish_intent,
    publishState: task.publish_state,
    hasCompressRecord: task.compressed_count !== null,
  })
}

// slotText 给读屏一句话说明四段各自状态，不另造数值。
function slotText(task: TaskRow): string {
  return slotsOf(task)
    .map((slot) => (slot.inactive ? `${slot.label}${slot.reason}` : `${slot.label}${stagePercent(slot.ratio)}%`))
    .join('，')
}

// progressById 缓存展开后才拉取的逐图进度，避免轮询时反复请求。
const progressById = ref(new Map<number, ServerProgress>())
const progressError = ref('')
// failedStamp 记录逐图原因对应的任务更新时间，轮询发现变化时重新拉取。
const failedStamp = ref(new Map<number, string>())

// failureOf 汇总一条任务的可读失败原因：先看任务级，再看发布级。
function failureOf(task: TaskRow): string {
  const reason = (task.error ?? '').trim()
  if (reason !== '') return reason
  return (task.publish_error ?? '').trim()
}

// failedImages 列出展开后看到的失败图片序号与原因。
function failedImages(task: TaskRow): { sort_order: number; error: string }[] {
  const state = progressById.value.get(task.id)
  if (state === undefined) return []
  return state.images
    .filter((image) => image.status === 'failed')
    .map((image) => ({ sort_order: image.sort_order, error: (image.error ?? '').trim() || '原因未知' }))
}

// retryTask 让失败任务重新排队：图片都存好时只重跑收尾，不再调用图床。
async function retryTask(task: TaskRow): Promise<void> {
  if (busyId.value !== 0) return
  busyId.value = task.id
  error.value = ''
  notice.value = ''
  try {
    const result = await retryPost(task.id)
    notice.value = result.retried === 0 ? '已重新排队，只重跑收尾' : `已重新排队 ${result.retried} 张`
    // 重试后逐图明细会变，丢掉缓存让展开时重新拉取。
    const next = new Map(progressById.value)
    next.delete(task.id)
    progressById.value = next
    await requestLoad(false)
    if (expanded.value.has(task.id)) {
      progressError.value = ''
      await loadTaskProgress(task.id)
    }
  } catch (cause) {
    error.value = cause instanceof ApiError ? cause.message : '重新排队失败，请稍后重试'
  } finally {
    busyId.value = 0
  }
}

async function loadTaskProgress(id: number, force = false): Promise<void> {
  if (!force && progressById.value.has(id)) return
  try {
    const state = await fetchProgress(id)
    const next = new Map(progressById.value)
    next.set(id, state)
    progressById.value = next
    const task = tasks.value.find((item) => item.id === id)
    if (task !== undefined) failedStamp.value = new Map(failedStamp.value).set(id, task.updated_at)
  } catch {
    progressError.value = '读取逐图原因失败，稍后重试'
  }
}

function toggleExpanded(id: number, status: string): void {
  if (expanded.value.has(id)) {
    expanded.value.delete(id)
  } else {
    expanded.value.add(id)
    if (status === 'failed') void loadTaskProgress(id)
  }
}

function isExpanded(id: number): boolean {
  return expanded.value.has(id)
}

async function load(silent = false): Promise<void> {
  const seq = (requestSeq += 1)
  // 记下发起时的条件：慢请求回来时若用户已经切筛选或翻页，这份结果整份丢弃。
  const requestedPage = page.value
  const requestedFilter = filter.value
  if (!silent) loading.value = true
  error.value = ''
  try {
    const result = await fetchTasks(requestedPage, pageSize, requestedFilter)
    // 只有最后一次请求可以写入，避免旧筛选或旧页码的结果盖掉新状态。
    if (seq !== requestSeq) return
    if (requestedFilter !== filter.value || requestedPage !== page.value) return
    // 命中数在翻页期间变少时，服务端已钳到最后一页；这里再兜一层，避免空白页。
    if (result.items.length === 0 && result.total > 0 && page.value > 1) {
      page.value = Math.max(1, Math.ceil(result.total / pageSize))
      void requestLoad(false)
      return
    }
    tasks.value = result.items
    pool.value = result.pool ?? { enabled: false, frozen: [] }
    total.value = result.total
    if (result.page !== page.value) page.value = result.page
    // 展开中的失败任务在轮询里对比更新时间，变了就重读逐图原因。
    for (const task of result.items) {
      if (!expanded.value.has(task.id) || task.status !== 'failed') continue
      const stamp = failedStamp.value.get(task.id)
      if (stamp === undefined) void loadTaskProgress(task.id)
      else if (stamp !== task.updated_at) void loadTaskProgress(task.id, true)
    }
  } catch (cause) {
    if (seq !== requestSeq) return
    error.value = cause instanceof ApiError ? cause.message : '读取任务失败，请稍后重试'
  } finally {
    // 前台请求无论是否被后来者顶掉，都要把自己的加载态收掉。
    if (!silent) loading.value = false
  }
}

// disposed 在离页后置位：此后不再接受新的刷新，迟到的补跑也一起作废。
let disposed = false

// requestLoad 串行化所有刷新入口：在途时只记一次补跑，避免重叠请求。
function requestLoad(silent = false): Promise<void> {
  if (disposed) return Promise.resolve()
  if (inFlight !== null) {
    queued = { silent: queued !== null && queued.silent === false ? false : silent }
    return inFlight
  }
  inFlight = (async () => {
    try {
      await load(silent)
    } finally {
      inFlight = null
      const next = queued
      queued = null
      // 隐藏或已离页时不补跑，回到可见时由 visibilitychange 重新发起。
      if (next !== null && !disposed && document.visibilityState === 'visible') void requestLoad(next.silent)
    }
  })()
  return inFlight
}

// poll 只在页面可见且没有在途请求时刷新，避免叠加请求。
async function poll(): Promise<void> {
  if (document.visibilityState === 'hidden') return
  await requestLoad(true)
}

function startPolling(): void {
  stopPolling()
  timer = setInterval(() => void poll(), POLL_MS)
}

function stopPolling(): void {
  if (timer === undefined) return
  clearInterval(timer)
  timer = undefined
}

// dropQueued 丢掉已排队的补跑：页面隐藏或卸载后不该再发请求。
function dropQueued(): void {
  queued = null
}

// onVisibility 切回页面时立即补拉一次，离开时停掉轮询。
function onVisibility(): void {
  if (document.visibilityState === 'visible') {
    void requestLoad(true)
    startPolling()
    return
  }
  stopPolling()
  dropQueued()
}

function setFilter(next: TaskFilter): void {
  if (filter.value === next) return
  filter.value = next
  // 换筛选回到第一页，避免停在旧页码上看到空白。
  page.value = 1
  notice.value = ''
  void requestLoad(false)
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
    // 控制后的刷新与定时、切筛选、翻页共用同一调度，不与在途请求叠加。
    await requestLoad(false)
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
  void requestLoad(false)
}

onMounted(() => {
  void requestLoad(false)
  startPolling()
  document.addEventListener('visibilitychange', onVisibility)
})

onBeforeUnmount(() => {
  disposed = true
  stopPolling()
  dropQueued()
  document.removeEventListener('visibilitychange', onVisibility)
})
</script>

<template>
  <div class="min-h-full">
    <AppHeader />

    <main class="mx-auto w-full max-w-[960px] px-5 py-7">
      <div class="flex flex-wrap items-center justify-between gap-2">
        <div class="flex flex-wrap items-center gap-x-3 gap-y-2">
          <h1 class="text-2xl font-semibold tracking-tight sm:text-3xl">任务</h1>
          <p class="tnum rounded-md border border-line bg-surface px-2.5 py-0.5 text-xs text-muted">
            共 {{ total }} 个<template v-if="filtered"> · {{ filterLabel }}</template>
          </p>
        </div>
        <router-link
          :to="{ name: 'workbench' }"
          class="inline-flex h-11 items-center rounded-xl bg-brand px-4 text-sm font-medium text-brand-ink transition hover:bg-brand-strong"
        >
          新建图集
        </router-link>
      </div>

      <div class="mt-3 max-w-full overflow-x-auto overscroll-x-contain">
        <div class="inline-flex w-max items-center gap-1 rounded-xl border border-line/70 bg-canvas p-1" role="group" aria-label="按状态筛选任务">
          <button
            v-for="item in TASK_FILTERS"
            :key="item.value"
            type="button"
            class="inline-flex h-11 shrink-0 items-center -my-1"
            :aria-pressed="filter === item.value"
            @click="setFilter(item.value)"
          >
            <!-- 外层留 44px 热区，内层视觉块保持 36px 与 8px 圆角。 -->
            <span
              class="inline-flex h-9 items-center rounded-lg px-3 text-xs font-medium transition"
              :class="filter === item.value ? 'bg-surface text-ink shadow-[0_1px_0_0_rgba(27,26,23,0.08)]' : 'text-muted hover:text-ink'"
            >
              {{ item.label }}
            </span>
          </button>
        </div>
      </div>

      <p
        v-if="pool.enabled && pool.frozen.length > 0"
        class="mt-3 rounded-xl bg-warn-soft px-3 py-2 text-xs leading-relaxed text-ink"
      >
        Bot 池已有 {{ pool.frozen.length }} 个 Bot 被标为不可写（{{ pool.frozen[0].alias || '未命名' }}{{ pool.frozen[0].note !== '' ? '：' + pool.frozen[0].note : '' }}），新上传会自动换用池内其他 Bot。
      </p>
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
          {{ filtered ? '换个筛选看看。' : '在工作台选好图片后提交，任务会出现在这里。' }}
        </p>
      </div>

      <ul v-else class="mt-4 space-y-3">
        <li
          v-for="task in tasks"
          :key="task.id"
          class="overflow-hidden rounded-2xl bg-surface shadow-[0_1px_0_0_rgba(27,26,23,0.05)]"
        >
          <button
            type="button"
            class="flex w-full flex-col px-4 py-1 text-left transition hover:bg-canvas/60 sm:px-5"
            :aria-expanded="isExpanded(task.id)"
            :aria-controls="`task-stages-${task.id}`"
            @click="toggleExpanded(task.id, task.status)"
          >
            <span class="flex min-h-11 w-full items-center gap-2">
              <!-- 标题与时间同组占剩余宽度：短标题时间紧跟，长标题截断给时间让位。 -->
              <span class="flex min-w-0 flex-1 items-center gap-2">
                <span class="min-w-0 truncate text-sm font-semibold" :title="task.title">{{ task.title }}</span>
                <span class="tnum shrink-0 text-xs text-muted">{{ updatedText(task.updated_at) }}</span>
              </span>
              <span
                class="hidden h-2 w-60 shrink-0 items-center gap-[3px] sm:flex"
                role="progressbar"
                :aria-label="`${task.title} 总进度`"
                :aria-valuenow="overallOf(task)"
                :aria-valuetext="slotText(task)"
                aria-valuemin="0"
                aria-valuemax="100"
              >
                <span
                  v-for="slot in slotsOf(task)"
                  :key="slot.key"
                  class="h-full flex-1 overflow-hidden rounded-[2px] bg-canvas first:rounded-l-full last:rounded-r-full"
                  :title="slot.inactive ? `${slot.label}${slot.reason}` : `${slot.label}${stagePercent(slot.ratio)}%`"
                  aria-hidden="true"
                >
                  <span v-if="slot.inactive" class="stage-slot-idle block h-full w-full"></span>
                  <span
                    v-else
                    class="block h-full transition-[width] duration-300"
                    :class="SLOT_FILL[slot.key]"
                    :style="{ width: `${String(stagePercent(slot.ratio))}%` }"
                  ></span>
                </span>
              </span>
              <span
                class="shrink-0 whitespace-nowrap rounded-md px-2.5 py-1 text-xs font-medium sm:min-w-[4.5rem] sm:text-center"
                :class="BADGE_CLASS[badgeOf(task).tone]"
              >
                {{ badgeOf(task).label }}
              </span>
              <span class="shrink-0 text-muted transition-transform" :class="isExpanded(task.id) ? 'rotate-180' : ''" aria-hidden="true">
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" class="h-4 w-4">
                  <path d="M6 9l6 6 6-6" stroke-linecap="round" stroke-linejoin="round" />
                </svg>
              </span>
            </span>
            <!-- 窄屏总轨换到下一行铺满，时间仍紧跟标题。 -->
            <span class="flex w-full items-center pb-1.5 sm:hidden">
              <span
                class="flex h-2 w-full items-center gap-[3px]"
                role="progressbar"
                :aria-label="`${task.title} 总进度`"
                :aria-valuenow="overallOf(task)"
                :aria-valuetext="slotText(task)"
                aria-valuemin="0"
                aria-valuemax="100"
              >
                <span
                  v-for="slot in slotsOf(task)"
                  :key="'narrow-' + slot.key"
                  class="h-full flex-1 overflow-hidden rounded-[2px] bg-canvas first:rounded-l-full last:rounded-r-full"
                  :title="slot.inactive ? `${slot.label}${slot.reason}` : `${slot.label}${stagePercent(slot.ratio)}%`"
                  aria-hidden="true"
                >
                  <span v-if="slot.inactive" class="stage-slot-idle block h-full w-full"></span>
                  <span
                    v-else
                    class="block h-full transition-[width] duration-300"
                    :class="SLOT_FILL[slot.key]"
                    :style="{ width: `${String(stagePercent(slot.ratio))}%` }"
                  ></span>
                </span>
              </span>
            </span>
          </button>

          <div v-if="isExpanded(task.id)" :id="`task-stages-${task.id}`" class="border-t border-line/70 px-4 py-3 sm:px-5">
            <div class="grid gap-4 lg:grid-cols-[minmax(0,1fr)_148px] lg:items-start">
              <div class="min-w-0">
                <TaskStageBar :stages="stagesOf(task)" layout="pairs" />
              </div>
              <div class="flex flex-col gap-1.5 lg:justify-start">
                <router-link
                  v-if="task.status === 'completed'"
                  :to="{ name: 'post', params: { id: String(task.id) }, hash: '#progress' }"
                  class="inline-flex h-11 items-center justify-center rounded-lg border border-line bg-surface px-4 text-xs font-medium text-muted transition hover:bg-canvas hover:text-ink sm:h-9"
                >
                  查看图集
                </router-link>
                <button
                  v-if="task.status === 'failed' && task.control_state === 'running'"
                  type="button"
                  class="inline-flex h-11 items-center justify-center rounded-lg border border-danger/35 bg-surface px-4 text-xs font-medium text-danger transition hover:bg-danger-soft disabled:opacity-60 sm:h-9"
                  :disabled="busyId !== 0"
                  @click="retryTask(task)"
                >
                  重试
                </button>
                <button
                  v-if="canResume(task)"
                  type="button"
                  class="inline-flex h-11 items-center justify-center rounded-lg border border-brand/30 bg-brand-soft px-4 text-xs font-medium text-brand-strong transition hover:border-brand/50 disabled:opacity-60 sm:h-9"
                  :disabled="busyId !== 0"
                  @click="control(task, 'resume')"
                >
                  继续
                </button>
                <router-link
                  v-else-if="canContinueUpload(task)"
                  :to="{ name: 'workbench', query: { resume: task.id } }"
                  class="inline-flex h-11 items-center justify-center rounded-lg border border-line bg-surface px-4 text-xs font-medium text-muted transition hover:bg-canvas hover:text-ink sm:h-9"
                >
                  继续上传
                </router-link>
                <button
                  v-if="canPause(task)"
                  type="button"
                  class="inline-flex h-11 items-center justify-center rounded-lg border border-line bg-surface px-4 text-xs font-medium text-muted transition hover:bg-canvas hover:text-ink disabled:opacity-60 sm:h-9"
                  :disabled="busyId !== 0"
                  @click="control(task, 'pause')"
                >
                  暂停
                </button>
                <button
                  v-if="canStop(task)"
                  type="button"
                  class="inline-flex h-11 items-center justify-center rounded-lg border border-danger/35 bg-surface px-4 text-xs font-medium text-danger transition hover:bg-danger-soft disabled:opacity-60 sm:h-9"
                  :disabled="busyId !== 0"
                  @click="control(task, 'stop')"
                >
                  停止
                </button>
                <p v-if="task.control_state === 'paused'" class="text-xs text-brand-strong">已暂停，继续只补缺项</p>
                <p v-else-if="task.control_state === 'stopped'" class="text-xs text-danger">已停止，不再上传或发布</p>
              </div>
            </div>

            <!-- 顶行已经给过更新时间，这里只留阶段概况，不重复同一时间。 -->
            <p class="mt-2 text-xs text-muted">{{ summaryOf(task) }}</p>
            <!-- 失败时把可读原因写在这里，不再只显示笼统的「存图失败」。 -->
            <p v-if="failureOf(task) !== ''" class="mt-1.5 rounded-md bg-danger-soft px-2 py-1.5 text-xs leading-relaxed text-danger">
              {{ failureOf(task) }}
            </p>
            <div v-if="failedImages(task).length > 0" class="mt-2">
              <p class="text-xs font-medium text-danger">失败明细</p>
              <ul class="mt-1 space-y-0.5">
                <li v-for="item in failedImages(task)" :key="item.sort_order" class="tnum text-xs text-muted">
                  第 {{ item.sort_order }} 张 · {{ item.error }}
                </li>
              </ul>
            </div>
            <p v-if="progressError !== '' && isExpanded(task.id)" class="mt-1 text-xs text-muted">{{ progressError }}</p>

          </div>
        </li>
      </ul>

      <div v-if="pages > 1" class="mt-4 flex items-center justify-between text-xs text-muted">
        <button
          type="button"
          class="inline-flex h-11 items-center justify-center gap-1 rounded-lg border border-line bg-surface px-3 text-xs font-medium transition hover:bg-canvas hover:text-ink disabled:opacity-40 sm:h-8 sm:px-2.5"
          :disabled="page <= 1"
          aria-label="上一页"
          @click="go(-1)"
        >
          <svg class="h-3.5 w-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M15 18l-6-6 6-6" /></svg>
          <span class="hidden sm:inline">上一页</span>
        </button>
        <span class="tnum">{{ page }} / {{ pages }}</span>
        <button
          type="button"
          class="inline-flex h-11 items-center justify-center gap-1 rounded-lg border border-line bg-surface px-3 text-xs font-medium transition hover:bg-canvas hover:text-ink disabled:opacity-40 sm:h-8 sm:px-2.5"
          :disabled="page >= pages"
          aria-label="下一页"
          @click="go(1)"
        >
          <span class="hidden sm:inline">下一页</span>
          <svg class="h-3.5 w-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M9 6l6 6-6 6" /></svg>
        </button>
      </div>
    </main>

    <BackToTop />
  </div>
</template>
