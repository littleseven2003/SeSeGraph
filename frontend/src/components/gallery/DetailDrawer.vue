<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { togglePost, updatePost, type PostDetail } from '../../api/gallery.ts'
import { ApiError } from '../../api/client.ts'
import {
  DEFAULT_SHARE_TEMPLATE,
  SHARE_PLACEHOLDERS,
  focusStep,
  formatFileSize,
  formatPublishedAt,
  imageShareUrl,
  postShareUrl,
  metadataFromDetail,
  readShareTemplate,
  renderShareText,
  saveShareTemplate,
  statusLabel,
  toggleActionLabel,
  toggleConfirmText,
  toggleHint,
  updateFailureText,
  validateMetadata,
  type MetadataDraft,
} from '../../utils/gallery.ts'

const props = defineProps<{
  detail: PostDetail | null
  loading: boolean
  error: string
}>()

const emit = defineEmits<{ close: []; retry: []; saved: []; toggled: [] }>()

const closeButton = ref<HTMLButtonElement | null>(null)
const panel = ref<HTMLElement | null>(null)
const feedback = ref('')
const shareTemplate = ref(DEFAULT_SHARE_TEMPLATE)
const templateDraft = ref(DEFAULT_SHARE_TEMPLATE)
const templateNotice = ref('')
const editing = ref(false)
const saving = ref(false)
const editError = ref('')
const draft = ref<MetadataDraft>({ title: '', description: '', authorName: '' })
const toggling = ref(false)
const confirmToggle = ref(false)
const toggleError = ref('')
let previousFocus: HTMLElement | null = null
// 记录当前已加载的帖子编号，刷新同一帖时不清掉操作反馈。
let activeId: number | null = null

const status = computed(() =>
  props.detail === null
    ? { label: '', tone: 'idle' as const }
    : statusLabel(props.detail.status, props.detail.publish_state, props.detail.disabled),
)
const published = computed(() => (props.detail === null ? '' : formatPublishedAt(props.detail.published_at)))
const created = computed(() => (props.detail === null ? '' : formatPublishedAt(props.detail.created_at)))
// 分享链接一律使用服务端公开地址，管理页面的域名不能代表图片出口。
const shareUrl = computed(() => (props.detail === null ? '' : postShareUrl(props.detail)))
const publicBase = computed(() => (props.detail === null ? '' : props.detail.public_base_url))
const shareReady = computed(() => shareUrl.value !== '')
const previewValues = computed(() => ({
  title: props.detail?.title ?? '标题',
  description: props.detail?.description ?? '说明',
  // 预览用示例链接，避免模板里的 {{url}} 原样显示成未替换的占位符。
  url: shareUrl.value !== '' ? shareUrl.value : 'https://telegra.ph/Example-1',
  count: props.detail?.images.length ?? 0,
  tags: props.detail?.tags ?? [],
}))

function imageLink(image: { short_code: string; status: string }): string {
  if (props.detail === null) return ''
  return imageShareUrl(props.detail, image, publicBase.value)
}

const toneClass = computed(() => {
  switch (status.value.tone) {
    case 'ok':
      return 'bg-accent/10 text-accent'
    case 'progress':
      return 'bg-brand/10 text-brand'
    case 'error':
      return 'bg-danger-soft text-danger'
    default:
      return 'bg-canvas text-muted'
  }
})

// copy 优先用剪贴板接口，被拒绝时回落到隐藏文本域。
async function copy(text: string, label: string): Promise<void> {
  if (text === '') {
    feedback.value = '暂无可复制的链接'
    return
  }
  let copied = false
  try {
    if (navigator.clipboard !== undefined) {
      await navigator.clipboard.writeText(text)
      copied = true
    }
  } catch {
    copied = false
  }
  if (!copied) {
    try {
      const area = document.createElement('textarea')
      area.value = text
      area.setAttribute('readonly', '')
      area.style.position = 'fixed'
      area.style.opacity = '0'
      document.body.appendChild(area)
      area.select()
      copied = document.execCommand('copy')
      document.body.removeChild(area)
    } catch {
      copied = false
    }
  }
  feedback.value = copied ? label + '已复制' : '复制失败，请手动选择'
}

function askToggle(): void {
  if (props.detail === null || toggling.value) return
  toggleError.value = ''
  confirmToggle.value = true
}

async function doToggle(): Promise<void> {
  if (props.detail === null || toggling.value) return
  toggling.value = true
  toggleError.value = ''
  try {
    const result = await togglePost(props.detail.id)
    confirmToggle.value = false
    feedback.value = result.disabled ? '已撤回该帖' : '已恢复该帖访问'
    emit('toggled')
  } catch (cause) {
    toggleError.value = cause instanceof ApiError ? updateFailureText(cause.status, cause.message) : '操作失败，请稍后重试'
  } finally {
    toggling.value = false
  }
}

function startEdit(): void {
  if (props.detail === null) return
  draft.value = metadataFromDetail(props.detail)
  editError.value = ''
  editing.value = true
}

function cancelEdit(): void {
  editing.value = false
  editError.value = ''
}

async function saveEdit(): Promise<void> {
  if (props.detail === null || saving.value) return
  const message = validateMetadata(draft.value)
  if (message !== '') {
    editError.value = message
    return
  }
  saving.value = true
  editError.value = ''
  try {
    await updatePost(props.detail.id, {
      title: draft.value.title.trim(),
      description: draft.value.description,
      author_name: draft.value.authorName.trim(),
    })
    editing.value = false
    feedback.value = '已保存'
    emit('saved')
  } catch (cause) {
    if (cause instanceof ApiError) {
      editError.value = updateFailureText(cause.status, cause.message)
    } else {
      editError.value = '保存失败，请稍后重试'
    }
  } finally {
    saving.value = false
  }
}

function copyShareText(): void {
  if (props.detail === null) return
  const text = renderShareText(shareTemplate.value, {
    title: props.detail.title,
    description: props.detail.description,
    url: shareUrl.value,
    count: props.detail.images.length,
    tags: props.detail.tags,
  })
  void copy(text, '分享文案')
}

// focusable 列出抽屉内可聚焦元素，用于把 Tab 循环限制在抽屉里。
function focusable(): HTMLElement[] {
  if (panel.value === null) return []
  const selector =
    'a[href], button:not([disabled]), textarea, input, select, summary, [tabindex]:not([tabindex="-1"])'
  return Array.from(panel.value.querySelectorAll<HTMLElement>(selector)).filter(
    (element) => element.offsetParent !== null,
  )
}

function onKeydown(event: KeyboardEvent): void {
  if (event.key === 'Escape') {
    emit('close')
    return
  }
  if (event.key !== 'Tab' || panel.value === null) return
  const items = focusable()
  if (items.length === 0) return
  const active = document.activeElement as HTMLElement | null
  const index = active === null ? -1 : items.indexOf(active)
  const inside = active !== null && panel.value.contains(active)
  if (!inside) {
    event.preventDefault()
    items[event.shiftKey ? items.length - 1 : 0]?.focus()
    return
  }
  const step = focusStep(index, items.length, event.shiftKey)
  const next = items[step]
  if (next === undefined) return
  // 只在首尾越界时接管，抽屉内部的正常切换交给浏览器。
  if ((!event.shiftKey && step === 0) || (event.shiftKey && step === items.length - 1)) {
    event.preventDefault()
    next.focus()
  }
}

function insertPlaceholder(token: string): void {
  templateDraft.value = templateDraft.value + token
}

function applyTemplate(): void {
  shareTemplate.value = saveShareTemplate(window.localStorage, templateDraft.value)
  templateDraft.value = shareTemplate.value
  templateNotice.value = '模板已保存'
}

function resetTemplate(): void {
  shareTemplate.value = saveShareTemplate(window.localStorage, '')
  templateDraft.value = shareTemplate.value
  templateNotice.value = '已恢复默认模板'
}

onMounted(() => {
  previousFocus = document.activeElement as HTMLElement | null
  shareTemplate.value = readShareTemplate(window.localStorage)
  templateDraft.value = shareTemplate.value
  window.addEventListener('keydown', onKeydown)
  document.body.style.overflow = 'hidden'
  closeButton.value?.focus()
})

onBeforeUnmount(() => {
  window.removeEventListener('keydown', onKeydown)
  document.body.style.overflow = ''
  // 关闭后把焦点还给打开抽屉的元素，键盘用户不会丢失位置。
  if (previousFocus !== null && previousFocus.isConnected) previousFocus.focus()
})

watch(
  () => props.detail?.id,
  (next) => {
    if (next === undefined || next === activeId) return
    activeId = next
    confirmToggle.value = false
    feedback.value = ''
    editing.value = false
    editError.value = ''
    toggleError.value = ''
  },
)
</script>

<template>
  <div class="fixed inset-0 z-40 flex justify-end bg-ink/45" @click.self="emit('close')">
    <aside
      ref="panel"
      role="dialog"
      aria-modal="true"
      aria-label="图集详情"
      class="flex h-full w-full max-w-full flex-col border-l border-line bg-surface sm:max-w-[520px]"
    >
      <header class="flex items-start justify-between gap-3 border-b border-line px-5 py-4">
        <div class="min-w-0">
          <p class="text-xs font-medium tracking-wide text-muted">图集详情</p>
          <h2 class="mt-1 truncate text-base font-semibold">{{ detail?.title ?? '正在读取…' }}</h2>
        </div>
        <div class="flex shrink-0 items-center gap-2">
          <button
            v-if="detail !== null && !editing"
            type="button"
            class="h-9 rounded-md border border-line px-3 text-xs font-medium transition hover:bg-canvas disabled:opacity-60"
            :disabled="toggling"
            @click="startEdit"
          >
            编辑
          </button>
          <button
            v-if="detail !== null && !editing"
            type="button"
            class="h-9 rounded-md border px-3 text-xs font-medium transition disabled:opacity-60"
            :class="detail.disabled ? 'border-accent/40 text-accent hover:bg-accent/10' : 'border-line text-danger hover:bg-danger-soft'"
            :disabled="toggling"
            @click="askToggle"
          >
            {{ toggling ? '处理中…' : toggleActionLabel(detail.disabled) }}
          </button>
          <button
            ref="closeButton"
            type="button"
            class="flex h-9 w-9 items-center justify-center rounded-md border border-line text-muted transition hover:bg-canvas"
            aria-label="关闭详情"
            @click="emit('close')"
          >
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" class="h-4 w-4">
            <path d="M6 6l12 12M18 6L6 18" stroke-linecap="round" />
          </svg>
          </button>
        </div>
      </header>

      <div class="flex-1 overflow-y-auto px-5 py-4">
        <p v-if="loading" class="py-10 text-center text-sm text-muted">正在读取详情…</p>

        <div v-else-if="error !== ''" class="rounded-lg border border-line bg-canvas px-4 py-8 text-center">
          <p class="text-sm text-danger">{{ error }}</p>
          <button
            type="button"
            class="mt-3 h-9 rounded-md border border-line px-3 text-sm font-medium transition hover:bg-surface"
            @click="emit('retry')"
          >
            重试
          </button>
        </div>

        <form v-else-if="detail !== null && editing" class="space-y-3" @submit.prevent="saveEdit">
          <label class="block">
            <span class="text-xs font-medium text-muted">标题</span>
            <input
              v-model="draft.title"
              type="text"
              class="mt-1 h-9 w-full rounded-md border border-line bg-canvas px-2 text-sm outline-none transition focus:border-brand focus:bg-surface"
            />
          </label>
          <label class="block">
            <span class="text-xs font-medium text-muted">说明</span>
            <textarea
              v-model="draft.description"
              rows="4"
              class="mt-1 w-full rounded-md border border-line bg-canvas px-2 py-1.5 text-sm leading-relaxed outline-none transition focus:border-brand focus:bg-surface"
            ></textarea>
          </label>
          <label class="block">
            <span class="text-xs font-medium text-muted">作者</span>
            <input
              v-model="draft.authorName"
              type="text"
              class="mt-1 h-9 w-full rounded-md border border-line bg-canvas px-2 text-sm outline-none transition focus:border-brand focus:bg-surface"
            />
          </label>

          <p class="text-xs text-muted">
            已发布帖会先更新所有 Telegraph 页面，全部成功后才保存本地记录；分页数量会变化时会被拒绝。
          </p>
          <p v-if="editError !== ''" class="text-xs text-danger" aria-live="polite">{{ editError }}</p>

          <div class="flex items-center gap-2">
            <button
              type="submit"
              class="h-9 rounded-md bg-brand px-3 text-xs font-medium text-brand-ink transition hover:bg-brand-strong disabled:opacity-60"
              :disabled="saving"
            >
              {{ saving ? '正在保存…' : '保存' }}
            </button>
            <button
              type="button"
              class="h-9 rounded-md border border-line px-3 text-xs font-medium transition hover:bg-canvas"
              :disabled="saving"
              @click="cancelEdit"
            >
              取消
            </button>
          </div>
        </form>

        <template v-else-if="detail !== null">
          <div v-if="confirmToggle" class="mb-3 rounded-md border border-line bg-canvas px-3 py-2">
            <p class="text-xs text-ink">{{ toggleConfirmText(detail.disabled) }}</p>
            <div class="mt-2 flex items-center gap-2">
              <button
                type="button"
                class="h-8 rounded-md bg-brand px-2.5 text-xs font-medium text-brand-ink transition hover:bg-brand-strong disabled:opacity-60"
                :disabled="toggling"
                @click="doToggle"
              >
                确认
              </button>
              <button
                type="button"
                class="h-8 rounded-md border border-line px-2.5 text-xs font-medium transition hover:bg-surface"
                :disabled="toggling"
                @click="confirmToggle = false"
              >
                取消
              </button>
              <span v-if="toggleError !== ''" class="text-xs text-danger">{{ toggleError }}</span>
            </div>
          </div>

          <p
            v-if="detail.disabled"
            class="mb-3 rounded-md border border-danger/25 bg-danger-soft px-3 py-2 text-xs text-danger"
          >
            {{ toggleHint(true) }}
          </p>

          <div class="flex flex-wrap items-center gap-2">
            <span class="rounded px-1.5 py-0.5 text-xs font-medium" :class="toneClass">{{ status.label }}</span>
            <span class="text-xs text-muted tnum">{{ detail.image_count }} 张图</span>
            <span class="text-xs text-muted">{{ detail.upload_mode === 'document' ? '原图模式' : '画质模式' }}</span>
          </div>

          <dl class="mt-4 grid grid-cols-2 gap-x-4 gap-y-2 text-xs">
            <div>
              <dt class="text-muted">发布时间</dt>
              <dd class="mt-0.5 font-medium tnum">{{ published !== '' ? published : '未发布' }}</dd>
            </div>
            <div>
              <dt class="text-muted">创建时间</dt>
              <dd class="mt-0.5 font-medium tnum">{{ created }}</dd>
            </div>
            <div v-if="detail.author_name !== ''">
              <dt class="text-muted">作者</dt>
              <dd class="mt-0.5 font-medium">{{ detail.author_name }}</dd>
            </div>
          </dl>

          <p v-if="detail.description !== ''" class="mt-4 whitespace-pre-line text-sm text-ink/85">
            {{ detail.description }}
          </p>

          <div v-if="detail.tags.length > 0" class="mt-3 flex flex-wrap gap-1">
            <span
              v-for="tag in detail.tags"
              :key="tag"
              class="rounded bg-canvas px-1.5 py-0.5 text-xs text-muted"
              >{{ tag }}</span
            >
          </div>

          <section v-if="detail.telegraph_urls.length > 0" class="mt-5">
            <h3 class="text-xs font-medium text-muted">分页链接</h3>
            <ol class="mt-2 space-y-1">
              <li v-for="(url, index) in detail.telegraph_urls" :key="url" class="flex items-center gap-2">
                <span class="w-10 shrink-0 text-xs text-muted tnum">第 {{ index + 1 }} 页</span>
                <a
                  :href="url"
                  target="_blank"
                  rel="noreferrer noopener"
                  class="min-w-0 flex-1 truncate text-xs text-brand hover:underline"
                  >{{ url }}</a
                >
                <button
                  type="button"
                  class="h-8 shrink-0 rounded border border-line px-2 text-xs text-muted transition hover:bg-canvas"
                  @click="copy(url, '链接')"
                >
                  复制
                </button>
              </li>
            </ol>
          </section>

          <section class="mt-5">
            <h3 class="text-xs font-medium text-muted">图片清单</h3>
            <ul class="mt-2 divide-y divide-line rounded-lg border border-line">
              <li
                v-for="image in detail.images"
                :key="image.short_code"
                class="flex items-center justify-between gap-3 px-3 py-2"
              >
                <div class="min-w-0">
                  <p class="text-xs font-medium tnum">第 {{ image.sort_order }} 张</p>
                  <p class="mt-0.5 truncate text-xs text-muted">
                    {{ image.mime_type }} · {{ formatFileSize(image.file_size) }}
                  </p>
                </div>
                <template v-if="imageLink(image) !== ''">
                  <a
                    :href="imageLink(image)"
                    target="_blank"
                    rel="noreferrer noopener"
                    class="shrink-0 text-xs text-brand hover:underline"
                    >查看外链</a
                  >
                  <button
                    type="button"
                    class="h-8 shrink-0 rounded border border-line px-2 text-xs text-muted transition hover:bg-canvas"
                    @click="copy(imageLink(image), '单图外链')"
                  >
                    复制
                  </button>
                </template>
                <span v-else class="shrink-0 text-xs text-muted">暂不可用</span>
              </li>
            </ul>
            <p v-if="detail.images.length === 0" class="mt-2 text-xs text-muted">还没有已上传的图片。</p>
          </section>
        </template>
      </div>

      <footer class="border-t border-line px-5 py-3">
        <div class="flex flex-wrap items-center gap-2">
          <button
            type="button"
            class="h-9 flex-1 rounded-md bg-brand px-3 text-xs font-medium text-brand-ink transition hover:bg-brand-strong disabled:opacity-60"
            :disabled="detail === null || !shareReady"
            @click="copy(shareUrl, '链接')"
          >
            复制纯链接
          </button>
          <button
            type="button"
            class="h-9 flex-1 rounded-md border border-line px-3 text-xs font-medium transition hover:bg-canvas disabled:opacity-60"
            :disabled="detail === null || !shareReady"
            @click="copyShareText"
          >
            复制分享文案
          </button>
        </div>
        <p class="mt-2 min-h-[18px] text-xs text-muted" aria-live="polite">
          {{ feedback !== '' ? feedback : shareReady || detail === null ? '' : '发布完成并生成互链后可分享整帖' }}
        </p>

        <details class="mt-1 text-xs">
          <summary class="cursor-pointer select-none text-muted hover:text-ink">分享文案模板</summary>
          <div class="mt-2 space-y-2">
            <label class="sr-only" for="share-template">分享文案模板</label>
            <textarea
              id="share-template"
              v-model="templateDraft"
              rows="3"
              class="w-full rounded-md border border-line bg-canvas px-2 py-1.5 text-xs leading-relaxed outline-none transition focus:border-brand focus:bg-surface"
            ></textarea>
            <div class="flex flex-wrap gap-1">
              <button
                v-for="token in SHARE_PLACEHOLDERS"
                :key="token"
                type="button"
                class="rounded border border-line px-1.5 py-0.5 font-mono text-xs text-muted transition hover:bg-canvas hover:text-ink"
                @click="insertPlaceholder(token)"
              >
                {{ token }}
              </button>
            </div>
            <div class="flex flex-wrap items-center gap-2">
              <button
                type="button"
                class="h-8 rounded-md bg-brand px-2.5 text-xs font-medium text-brand-ink transition hover:bg-brand-strong"
                @click="applyTemplate"
              >
                保存模板
              </button>
              <button
                type="button"
                class="h-8 rounded-md border border-line px-2.5 text-xs font-medium transition hover:bg-canvas"
                @click="resetTemplate"
              >
                恢复默认
              </button>
              <span class="text-muted" aria-live="polite">{{ templateNotice }}</span>
            </div>
            <p class="whitespace-pre-line rounded bg-canvas px-2 py-1.5 text-xs leading-relaxed text-muted">
              {{ renderShareText(templateDraft, previewValues) }}
            </p>
          </div>
        </details>
      </footer>
    </aside>
  </div>
</template>
