<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { parseMetaText, splitTags, type ParseMode, type PostMeta } from '../../utils/meta.ts'

// 元信息表单：总标题、总说明、标签与作者，支持从包内文本预填。
const draft = defineModel<PostMeta>({ required: true })
const props = defineProps<{
  metaText: string
  metaSource: string
  /** 设置页保存的默认解析方式 */
  parseDefault?: ParseMode
  disabled?: boolean
}>()
const emit = defineEmits<{ (event: 'error', message: string): void }>()

const parseMode = ref<ParseMode>(props.parseDefault ?? 'first-line')
// 设置异步读取完成后同步默认解析方式，用户手动切换过则不再覆盖。
watch(
  () => props.parseDefault,
  (value) => {
    if (value === 'first-line' || value === 'key-value') parseMode.value = value
  },
)
const tagInput = ref('')

const hasMetaText = computed(() => props.metaText.trim() !== '')

function addTag(raw: string): void {
  const merged = splitTags([...draft.value.tags, raw].join(','))
  if (merged.length > 20) {
    emit('error', '标签数量不超过 20 个')
    return
  }
  if (merged.some((tag) => tag.length > 30)) {
    emit('error', '单个标签不超过 30 字')
    return
  }
  draft.value = { ...draft.value, tags: merged }
  tagInput.value = ''
}

function onTagKeydown(event: KeyboardEvent): void {
  if (event.key !== 'Enter' && event.key !== ',' && event.key !== '，') return
  event.preventDefault()
  if (tagInput.value.trim() !== '') addTag(tagInput.value)
}

function removeTag(tag: string): void {
  draft.value = { ...draft.value, tags: draft.value.tags.filter((item) => item !== tag) }
}

// 从包内文本预填，模式可切换；只填充有内容的字段，避免覆盖手写内容。
function applyFromPackage(): void {
  if (!hasMetaText.value) return
  const parsed = parseMetaText(props.metaText, parseMode.value)
  draft.value = {
    title: parsed.title || draft.value.title,
    description: parsed.description || draft.value.description,
    tags: parsed.tags.length > 0 ? parsed.tags : draft.value.tags,
    author: parsed.author || draft.value.author,
  }
}
</script>

<template>
  <section class="rounded-lg border border-line bg-surface p-4">
    <div class="flex flex-wrap items-start justify-between gap-3">
      <div>
        <h2 class="text-base font-semibold">元信息</h2>
        <p class="mt-0.5 text-xs text-muted">标题必填，说明、标签与作者会写入图集记录。</p>
      </div>

      <div class="flex flex-wrap items-center gap-2">
        <div class="flex rounded-md border border-line p-0.5" role="group" aria-label="包内信息解析模式">
          <button
            v-for="option in [
              { value: 'first-line', label: '首行标题' },
              { value: 'key-value', label: '键值匹配' },
            ]"
            :key="option.value"
            type="button"
            class="h-7 rounded px-2 text-xs font-medium transition"
            :class="parseMode === option.value ? 'bg-accent text-white' : 'text-muted hover:bg-canvas'"
            @click="parseMode = option.value as ParseMode"
          >
            {{ option.label }}
          </button>
        </div>
        <button
          type="button"
          class="h-8 rounded-md border border-line px-2.5 text-xs font-medium transition hover:border-muted/40 hover:bg-canvas disabled:opacity-50"
          :disabled="props.disabled || !hasMetaText"
          :title="hasMetaText ? `从 ${props.metaSource || '包内文本'} 读取` : '压缩包内没有可用的说明文件'"
          @click="applyFromPackage"
        >
          从包内读取
        </button>
      </div>
    </div>

    <p v-if="hasMetaText" class="mt-2 text-xs text-muted">
      已找到包内说明文件 {{ props.metaSource || '说明文件' }}，可切换模式后点「从包内读取」预填。
    </p>

    <div class="mt-3 grid gap-3 lg:grid-cols-2">
      <label class="block">
        <span class="text-xs font-medium text-muted">总标题</span>
        <input
          :value="draft.title"
          type="text"
          maxlength="200"
          placeholder="例如：夏日海边写真"
          class="mt-1 h-10 w-full rounded-md border border-line bg-surface px-3 text-sm outline-none transition focus:border-brand"
          :disabled="props.disabled"
          @input="draft = { ...draft, title: ($event.target as HTMLInputElement).value }"
        />
      </label>

      <label class="block">
        <span class="text-xs font-medium text-muted">作者名（留空使用全局设置）</span>
        <input
          :value="draft.author"
          type="text"
          maxlength="100"
          placeholder="例如：SeSe"
          class="mt-1 h-10 w-full rounded-md border border-line bg-surface px-3 text-sm outline-none transition focus:border-brand"
          :disabled="props.disabled"
          @input="draft = { ...draft, author: ($event.target as HTMLInputElement).value }"
        />
      </label>

      <label class="block lg:col-span-2">
        <span class="text-xs font-medium text-muted">总说明</span>
        <textarea
          :value="draft.description"
          rows="3"
          maxlength="5000"
          placeholder="这组图片的说明，可留空"
          class="mt-1 w-full resize-y rounded-md border border-line bg-surface px-3 py-2 text-sm outline-none transition focus:border-brand"
          :disabled="props.disabled"
          @input="draft = { ...draft, description: ($event.target as HTMLTextAreaElement).value }"
        />
      </label>

      <div class="lg:col-span-2">
        <span class="text-xs font-medium text-muted">标签</span>
        <div
          class="mt-1 flex min-h-10 flex-wrap items-center gap-1.5 rounded-md border border-line bg-surface px-2 py-1.5"
        >
          <span
            v-for="tag in draft.tags"
            :key="tag"
            class="inline-flex items-center gap-1 rounded bg-canvas px-2 py-0.5 text-xs font-medium"
          >
            {{ tag }}
            <button
              type="button"
              class="text-muted transition hover:text-danger disabled:opacity-40"
              :disabled="props.disabled"
              :aria-label="`移除标签 ${tag}`"
              @click="removeTag(tag)"
            >
              ×
            </button>
          </span>
          <input
            v-model="tagInput"
            type="text"
            class="h-7 min-w-[120px] flex-1 border-0 bg-transparent px-1 text-sm outline-none"
            placeholder="输入标签后回车添加"
            :disabled="props.disabled"
            @keydown="onTagKeydown"
            @blur="tagInput.trim() !== '' && addTag(tagInput)"
          />
        </div>
        <p class="mt-1 text-xs text-muted">最多 20 个标签，单个不超过 30 字，重复的会自动去掉。</p>
      </div>
    </div>
  </section>
</template>
