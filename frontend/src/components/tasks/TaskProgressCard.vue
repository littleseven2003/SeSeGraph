<script setup lang="ts">
import { computed } from 'vue'
import TaskStageBar from './TaskStageBar.vue'
import type { StageView } from '../../utils/stages.ts'
import type { ServerProgress } from '../../api/posts.ts'
import { canRequestPublish, needsRetry, publishActive, publishStateLabel, publishSummary } from '../../utils/progress.ts'

// 任务进度卡：四阶段进度条 + 控制入口，工作台与任务页共用同一套阶段口径。
const props = defineProps<{
  stages: StageView[]
  /** 没有服务端进度时只展示阶段条（例如刚压缩完、还没建任务） */
  progress?: ServerProgress | null
  retrying?: boolean
  publishing?: boolean
  controlBusy?: boolean
}>()
const emit = defineEmits<{
  (event: 'retry'): void
  (event: 'publish'): void
  (event: 'pause'): void
  (event: 'resume'): void
  (event: 'stop'): void
}>()

// 只有还没结束的上传任务才提供暂停与停止入口。
const controllable = computed(() => {
  const progress = props.progress
  if (!progress) return false
  return (
    progress.status === 'uploading' ||
    progress.status === 'queued' ||
    progress.status === 'publishing' ||
    (progress.status === 'completed' &&
      (progress.publish_state === 'pending' || progress.publish_state === 'publishing'))
  )
})
const controlState = computed(() => props.progress?.control_state ?? 'running')
const canPublish = computed(() => (props.progress ? canRequestPublish(props.progress) : false))
const canRetry = computed(() => (props.progress ? needsRetry(props.progress) : false))
const publishHint = computed(() => (props.progress ? publishSummary(props.progress) : ''))
const publishLabel = computed(() => (props.progress ? publishStateLabel(props.progress.publish_state) : ''))
</script>

<template>
  <section class="rounded-2xl bg-surface p-4 shadow-[0_1px_0_0_rgba(27,26,23,0.05)]">
    <div class="flex items-baseline justify-between gap-2">
      <h2 class="text-sm font-semibold tracking-tight">任务进度</h2>
      <p class="tnum text-xs text-muted">
        <template v-if="props.progress">任务 #{{ props.progress.id }}</template>
        <template v-else>四阶段</template>
      </p>
    </div>

    <div class="mt-3">
      <TaskStageBar :stages="props.stages" />
    </div>

    <div v-if="controllable || canRetry" class="mt-3 flex flex-wrap items-center gap-2">
      <button
        v-if="canRetry"
        type="button"
        class="inline-flex h-11 items-center rounded-full bg-brand px-4 text-sm font-medium text-brand-ink transition hover:bg-brand-strong disabled:opacity-60"
        :disabled="props.retrying"
        @click="emit('retry')"
      >
        {{ props.retrying ? '正在重试…' : `重试 ${props.progress?.failed_count ?? 0} 张` }}
      </button>
      <button
        v-if="controllable && controlState === 'running'"
        type="button"
        class="inline-flex h-11 items-center rounded-full border border-line bg-surface px-3.5 text-xs font-medium text-muted transition hover:border-ink/25 hover:text-ink disabled:opacity-60"
        :disabled="props.controlBusy"
        @click="emit('pause')"
      >
        {{ props.controlBusy ? '处理中…' : '暂停任务' }}
      </button>
      <button
        v-else-if="controllable && controlState === 'paused'"
        type="button"
        class="inline-flex h-11 items-center rounded-full border border-brand/30 bg-brand-soft px-3.5 text-xs font-medium text-brand-strong transition hover:border-brand/50 disabled:opacity-60"
        :disabled="props.controlBusy"
        @click="emit('resume')"
      >
        {{ props.controlBusy ? '处理中…' : '继续任务' }}
      </button>
      <button
        v-if="controllable"
        type="button"
        class="inline-flex h-11 items-center rounded-full border border-danger/35 bg-surface px-3.5 text-xs font-medium text-danger transition hover:bg-danger-soft disabled:opacity-60"
        :disabled="props.controlBusy"
        @click="emit('stop')"
      >
        停止任务
      </button>
      <span v-if="controlState === 'paused'" class="text-xs text-muted">已暂停，继续只补未完成的项</span>
      <span v-else-if="controlState === 'stopped'" class="text-xs text-muted">任务已停止</span>
    </div>

    <div v-if="props.progress?.status === 'completed'" class="mt-3 rounded-xl bg-canvas px-3.5 py-2.5">
      <div class="flex flex-wrap items-center justify-between gap-2">
        <div class="min-w-0">
          <p class="text-sm font-medium">Telegraph 发布</p>
          <p class="mt-0.5 text-xs text-muted">{{ publishHint }} · 状态：{{ publishLabel }}</p>
        </div>
        <button
          v-if="canPublish"
          type="button"
          class="inline-flex h-11 items-center rounded-full bg-brand px-4 text-sm font-medium text-brand-ink transition hover:bg-brand-strong disabled:opacity-60"
          :disabled="props.progress ? publishActive(props.progress) || props.publishing : false"
          @click="emit('publish')"
        >
          {{ props.publishing ? '正在提交…' : props.progress?.publish_state === 'failed' ? '重新发布' : '发布到 Telegraph' }}
        </button>
      </div>
      <ul v-if="(props.progress?.telegraph_urls.length ?? 0) > 0" class="mt-2 space-y-1">
        <li v-for="url in props.progress?.telegraph_urls ?? []" :key="url" class="text-xs">
          <a :href="url" target="_blank" rel="noreferrer noopener" class="break-all text-brand-strong underline underline-offset-2">{{ url }}</a>
        </li>
      </ul>
    </div>
  </section>
</template>
