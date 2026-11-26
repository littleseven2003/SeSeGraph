<script setup lang="ts">
import type { TaskTone } from '../../utils/tasks.ts'
import { stagePercent, type StageKey, type StageView } from '../../utils/stages.ts'

// 四阶段进度条：桌面四等分，390px 折成 2×2；工作台与任务页共用同一份结构与色板。
// 列对布局在窄屏也是两列：每列两段，合成 2×2，不再退化成单列四行。
const props = defineProps<{
  stages: StageView[]
  /** 列对布局把四段拆成两列各两段，供任务页 3:3:1 主体使用；缺省仍是四等分一行。 */
  layout?: 'row' | 'pairs'
}>()

// 每段用自己的字标色，失败改红、暂停降透明度；未开始时用中性底色。
const STAGE_FILL: Record<StageKey, string> = {
  compress: 'bg-stage-compress',
  upload: 'bg-stage-upload',
  sync: 'bg-stage-sync',
  publish: 'bg-stage-publish',
}

// showValue 判断右侧状态文字是否值得显示：未开始的阶段只留中性轨道，读屏信息在容器 aria-label 里。
function showValue(item: StageView): boolean {
  return item.value !== '待开始' && item.value !== '等待' && item.value !== '未开始'
}

function fillClass(item: StageView): string {
  if (item.tone === 'danger') return 'bg-danger'
  if (item.tone === 'muted') return 'bg-line'
  return STAGE_FILL[item.key]
}

function fillStyle(item: StageView): Record<string, string> {
  return {
    width: `${String(stagePercent(item.ratio))}%`,
    opacity: item.tone === 'warn' ? '0.55' : '1',
  }
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
  <div
    v-if="props.layout === 'pairs'"
    class="grid grid-cols-2 gap-x-6 gap-y-4 sm:gap-x-8"
    :aria-label="`四阶段进度：${props.stages.map((item) => item.index + '. ' + item.label + ' ' + item.value).join('，')}`"
  >
    <ol class="space-y-4">
      <li v-for="item in [props.stages[0], props.stages[2]]" :key="item.key" class="min-w-0">
        <div class="flex items-baseline justify-between gap-2">
          <span class="truncate text-xs">
            <span class="tnum text-muted/75">{{ item.index }}.</span>
            <span class="ml-0.5 font-medium">{{ item.label }}</span>
          </span>
          <span v-if="showValue(item)" class="tnum shrink-0 text-xs" :class="VALUE_TEXT[item.tone]">{{ item.value }}</span>
        </div>
        <div class="mt-2 h-1.5 w-full overflow-hidden rounded-full bg-canvas" role="progressbar" :aria-label="`${item.label}进度`" :aria-valuenow="stagePercent(item.ratio)" aria-valuemin="0" aria-valuemax="100">
          <div class="h-full rounded-full transition-[width] duration-300" :class="fillClass(item)" :style="fillStyle(item)"></div>
        </div>
      </li>
    </ol>
    <ol class="space-y-4">
      <li v-for="item in [props.stages[1], props.stages[3]]" :key="item.key" class="min-w-0">
        <div class="flex items-baseline justify-between gap-2">
          <span class="truncate text-xs">
            <span class="tnum text-muted/75">{{ item.index }}.</span>
            <span class="ml-0.5 font-medium">{{ item.label }}</span>
          </span>
          <span v-if="showValue(item)" class="tnum shrink-0 text-xs" :class="VALUE_TEXT[item.tone]">{{ item.value }}</span>
        </div>
        <div class="mt-2 h-1.5 w-full overflow-hidden rounded-full bg-canvas" role="progressbar" :aria-label="`${item.label}进度`" :aria-valuenow="stagePercent(item.ratio)" aria-valuemin="0" aria-valuemax="100">
          <div class="h-full rounded-full transition-[width] duration-300" :class="fillClass(item)" :style="fillStyle(item)"></div>
        </div>
      </li>
    </ol>
  </div>

  <ol
    v-else
    class="grid grid-cols-2 gap-3.5 sm:grid-cols-4 sm:gap-4"
    :aria-label="`四阶段进度：${stages.map((item) => item.index + '. ' + item.label + ' ' + item.value).join('，')}`"
  >
    <li v-for="item in stages" :key="item.key" class="min-w-0">
      <div class="flex items-baseline justify-between gap-2">
        <span class="truncate text-xs">
          <span class="tnum text-muted/75">{{ item.index }}.</span>
          <span class="ml-0.5 font-medium">{{ item.label }}</span>
        </span>
        <span v-if="showValue(item)" class="tnum shrink-0 text-xs" :class="VALUE_TEXT[item.tone]">{{ item.value }}</span>
      </div>
      <div
        class="mt-1.5 h-1.5 w-full overflow-hidden rounded-full bg-canvas"
        role="progressbar"
        :aria-label="`${item.label}进度`"
        :aria-valuenow="stagePercent(item.ratio)"
        aria-valuemin="0"
        aria-valuemax="100"
      >
        <div
          class="h-full rounded-full transition-[width] duration-300"
          :class="fillClass(item)"
          :style="fillStyle(item)"
        ></div>
      </div>
    </li>
  </ol>
</template>
