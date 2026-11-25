<script setup lang="ts">
import type { TaskTone } from '../../utils/tasks.ts'
import type { StageView } from '../../utils/stages.ts'

// 四阶段进度条：桌面四等分，390px 折成 2×2；工作台与任务页共用同一份结构与色板。
defineProps<{
  stages: StageView[]
}>()

const FILL: Record<TaskTone, string> = {
  run: 'bg-brand',
  ok: 'bg-accent',
  warn: 'bg-warn',
  danger: 'bg-danger',
  muted: 'bg-line',
}

const VALUE_TEXT: Record<TaskTone, string> = {
  run: 'text-brand-strong',
  ok: 'text-accent',
  warn: 'text-warn',
  danger: 'text-danger',
  muted: 'text-muted',
}
</script>

<template>
  <ol
    class="grid grid-cols-2 gap-2.5 sm:grid-cols-4 sm:gap-3"
    :aria-label="`四阶段进度：${stages.map((item) => item.index + '. ' + item.label + ' ' + item.value).join('，')}`"
  >
    <li v-for="item in stages" :key="item.key" class="min-w-0">
      <div class="flex items-baseline justify-between gap-2">
        <span class="truncate text-xs">
          <span class="tnum text-muted/75">{{ item.index }}.</span>
          <span class="ml-0.5 font-medium">{{ item.label }}</span>
        </span>
        <span class="tnum shrink-0 text-xs" :class="VALUE_TEXT[item.tone]">{{ item.value }}</span>
      </div>
      <div
        class="mt-1.5 h-1.5 w-full overflow-hidden rounded-full bg-canvas"
        role="progressbar"
        :aria-label="`${item.label}进度`"
        :aria-valuenow="Math.round(item.ratio * 100)"
        aria-valuemin="0"
        aria-valuemax="100"
      >
        <div
          class="h-full rounded-full transition-[width] duration-300"
          :class="FILL[item.tone]"
          :style="{ width: `${Math.round(item.ratio * 100)}%` }"
        ></div>
      </div>
    </li>
  </ol>
</template>
