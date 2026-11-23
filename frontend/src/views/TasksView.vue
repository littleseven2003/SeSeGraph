<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { ApiError } from '../api/client.ts'
import { fetchTasks, pauseTask, resumeTask, stopTask, type TaskRow } from '../api/posts.ts'
import { formatPublishedAt, statusLabel } from '../utils/gallery.ts'
import { publishStateLabel } from '../utils/progress.ts'

const tasks = ref<TaskRow[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = 20
const loading = ref(false)
const error = ref('')
const notice = ref('')
const busyId = ref(0)
let timer: ReturnType<typeof setInterval> | undefined

const pages = computed(() => Math.max(1, Math.ceil(total.value / pageSize)))

async function load(): Promise<void> {
  if (loading.value) return
  loading.value = true
  error.value = ''
  try {
    const result = await fetchTasks(page.value, pageSize)
    tasks.value = result.items
    total.value = result.total
    if (result.page !== page.value) page.value = result.page
  } catch (cause) {
    error.value = cause instanceof ApiError ? cause.message : '读取任务失败，请稍后重试'
  } finally {
    loading.value = false
  }
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

function stageUpload(task: TaskRow): string {
  if (task.stored_count >= task.image_count && task.image_count > 0) return '已完成'
  return `${task.stored_count}/${task.image_count} 张`
}

function canPause(task: TaskRow): boolean {
  if (task.control_state !== 'running') return false
  return ['uploading', 'queued', 'publishing'].includes(task.status) ||
    ['pending', 'publishing'].includes(task.publish_state)
}

function canResume(task: TaskRow): boolean {
  return task.control_state === 'paused'
}

function canStop(task: TaskRow): boolean {
  return task.control_state !== 'stopped'
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
    <header class="border-b border-line/60">
      <div class="mx-auto flex w-full max-w-[1240px] items-center justify-between gap-4 px-5 py-3">
        <div class="flex min-w-0 items-center gap-2.5">
          <router-link :to="{ name: 'home' }" class="text-sm text-muted transition hover:text-ink">画廊</router-link>
          <span class="h-4 w-px bg-line" aria-hidden="true"></span>
          <h1 class="text-sm font-semibold">任务</h1>
        </div>
        <router-link :to="{ name: 'workbench' }" class="inline-flex h-11 items-center rounded-full bg-ink px-4 text-sm font-medium text-white transition hover:bg-ink/90">
          新建图集
        </router-link>
      </div>
    </header>

    <main class="mx-auto w-full max-w-[1240px] px-5 py-6">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <p class="text-xs text-muted">每 10 秒刷新一次；暂停后继续只补未完成的项，停止为终态。</p>
        <button
          type="button"
          class="h-10 rounded-full border border-line px-3.5 text-sm font-medium text-muted transition hover:bg-canvas hover:text-ink disabled:opacity-60"
          :disabled="loading"
          @click="load"
        >
          {{ loading ? '刷新中…' : '刷新' }}
        </button>
      </div>

      <p v-if="error !== ''" class="mt-3 text-xs text-danger">{{ error }}</p>
      <p v-else-if="notice !== ''" class="mt-3 text-xs text-accent">{{ notice }}</p>

      <div v-if="tasks.length === 0 && !loading" class="mt-10 rounded-2xl border border-line bg-surface px-6 py-10 text-center text-sm text-muted">
        没有进行中的任务
      </div>

      <ul v-else class="mt-4 space-y-2.5">
        <li
          v-for="task in tasks"
          :key="task.id"
          class="rounded-2xl border border-line bg-surface px-4 py-3.5 shadow-[0_1px_0_0_rgba(27,26,23,0.05)]"
        >
          <div class="flex flex-wrap items-start justify-between gap-3">
            <div class="min-w-0">
              <p class="truncate text-sm font-semibold" :title="task.title">
                <span class="tnum mr-1.5 text-muted">#{{ task.id }}</span>{{ task.title }}
              </p>
              <p class="mt-0.5 text-xs text-muted">更新于 {{ updatedText(task.updated_at) }}</p>
            </div>
            <div class="flex shrink-0 items-center gap-2">
              <button
                v-if="canPause(task)"
                type="button"
                class="h-9 rounded-full border border-line px-3 text-xs font-medium text-muted transition hover:border-ink/25 hover:text-ink disabled:opacity-60"
                :disabled="busyId !== 0"
                @click="control(task, 'pause')"
              >
                暂停
              </button>
              <button
                v-if="canResume(task)"
                type="button"
                class="h-9 rounded-full border border-brand/30 bg-brand-soft px-3 text-xs font-medium text-brand-strong transition hover:border-brand/50 disabled:opacity-60"
                :disabled="busyId !== 0"
                @click="control(task, 'resume')"
              >
                继续
              </button>
              <button
                v-if="canStop(task)"
                type="button"
                class="h-9 rounded-full border border-danger/35 bg-white px-3 text-xs font-medium text-danger transition hover:bg-danger-soft disabled:opacity-60"
                :disabled="busyId !== 0"
                @click="control(task, 'stop')"
              >
                停止
              </button>
            </div>
          </div>

          <dl class="mt-2.5 grid grid-cols-1 gap-2 text-xs sm:grid-cols-3">
            <div class="rounded-xl bg-canvas px-3 py-2">
              <dt class="text-muted">传至服务器</dt>
              <dd class="mt-0.5 font-medium">{{ stageUpload(task) }}</dd>
            </div>
            <div class="rounded-xl bg-canvas px-3 py-2">
              <dt class="text-muted">存入频道</dt>
              <dd class="mt-0.5 font-medium">{{ statusLabel(task.status, task.publish_state).label }}</dd>
            </div>
            <div class="rounded-xl bg-canvas px-3 py-2">
              <dt class="text-muted">发布 Telegraph</dt>
              <dd class="mt-0.5 font-medium">{{ publishStateLabel(task.publish_state) }}</dd>
            </div>
          </dl>
          <p v-if="task.control_state === 'paused'" class="mt-2 text-xs text-brand-strong">已暂停，继续只补缺项</p>
          <p v-else-if="task.control_state === 'stopped'" class="mt-2 text-xs text-danger">已停止，不再上传或发布</p>
        </li>
      </ul>

      <div v-if="pages > 1" class="mt-4 flex items-center justify-between text-xs text-muted">
        <button type="button" class="h-10 rounded-full border border-line px-3.5 disabled:opacity-40" :disabled="page <= 1" @click="go(-1)">
          上一页
        </button>
        <span class="tnum">{{ page }} / {{ pages }}</span>
        <button type="button" class="h-10 rounded-full border border-line px-3.5 disabled:opacity-40" :disabled="page >= pages" @click="go(1)">
          下一页
        </button>
      </div>
    </main>
  </div>
</template>
