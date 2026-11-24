<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { setPostDisabled, updatePost, type GalleryImage, type PostDetail } from '../../api/gallery.ts'
import { changeCover, fetchMediaBlob } from '../../api/posts.ts'
import { ApiError } from '../../api/client.ts'
import { copyText } from '../../utils/clipboard.ts'
import { detailUploadModeLabel } from '../../utils/media.ts'
import { makeCoverFromMedia } from '../../utils/cover.ts'
import { splitTags } from '../../utils/meta.ts'
import { bubblePosition, type BubblePosition } from '../../utils/bubble.ts'
import {
  ThumbQueue,
  canLoadThumb,
  needsExplicitPreview,
  pickEagerOrders,
  renderThumb,
  withTimeout,
} from '../../utils/thumbs.ts'
import {
  DEFAULT_SHARE_TEMPLATE,
  SHARE_PLACEHOLDERS,
  focusStep,
  formatFileSize,
  formatPublishedAt,
  formatPublishedDate,
  formatViewsExact,
  showsViews,
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
  viewsRefreshing?: boolean
  viewsNotice?: string
  viewsNoticeTone?: 'ok' | 'muted'
  deleting?: boolean
  deleteError?: string
}>()

const emit = defineEmits<{ close: []; retry: []; saved: []; toggled: []; refreshViews: []; delete: [] }>()

const closeButton = ref<HTMLButtonElement | null>(null)
const panel = ref<HTMLElement | null>(null)
const scroller = ref<HTMLElement | null>(null)
const feedback = ref('')
// copiedAction 记录刚刚复制成功的动作，按钮文字短暂反馈，避免只靠底部提示。
const copiedAction = ref('')
let copiedTimer: number | undefined
const shareTemplate = ref(DEFAULT_SHARE_TEMPLATE)
const templateDraft = ref(DEFAULT_SHARE_TEMPLATE)
const templateNotice = ref('')
const editing = ref(false)
const saving = ref(false)
const editError = ref('')
const draft = ref<MetadataDraft>({ title: '', description: '', authorName: '', tagsText: '' })
const toggling = ref(false)
const confirmToggle = ref(false)
const toggleError = ref('')
const confirmDelete = ref(false)
// 视频条目按需展开播放器，避免打开抽屉就加载整段视频。
const playingCode = ref('')
// 更换封面时先生成新的缩略图；已发布帖由后端先改远端再回写本地。
const coverBusy = ref(false)
const coverError = ref('')
// 缩略图按序号缓存 object URL；放大气泡复用同一份，不重复回源。
const thumbUrls = ref<Map<number, string>>(new Map())
const previewOrder = ref(0)
const previewPos = ref<BubblePosition | null>(null)
const previewLoading = ref(false)
let thumbs: ThumbQueue | null = null
let thumbPostId = 0
let observer: IntersectionObserver | null = null
let previewAnchor: HTMLElement | null = null
// 气泡尺寸固定，方便定位时先按预估高度钳制，加载完成后再校正。
const PREVIEW_WIDTH = 240
const PREVIEW_HEIGHT = 264

function togglePlay(code: string): void {
  playingCode.value = playingCode.value === code ? '' : code
}
let previousFocus: HTMLElement | null = null
// 记录当前已加载的帖子编号，刷新同一帖时不清掉操作反馈。
let activeId: number | null = null

const status = computed(() =>
  props.detail === null
    ? { label: '', tone: 'idle' as const }
    : statusLabel(props.detail.status, props.detail.publish_state, props.detail.disabled),
)
const published = computed(() => (props.detail === null ? '' : formatPublishedAt(props.detail.published_at)))
// 浏览量只在已发布、未撤回且同步过时展示；未同步的已发布帖显示「尚未统计」。
const showViews = computed(() => (props.detail === null ? false : showsViews(props.detail)))
const viewsText = computed(() =>
  props.detail === null ? '' : formatViewsExact(props.detail.views_count) + ' 次浏览',
)
const viewsTitle = computed(() =>
  props.detail === null || props.detail.views_updated_at === null
    ? '尚未同步浏览量'
    : '更新于 ' + formatPublishedAt(props.detail.views_updated_at),
)
const canRefreshViews = computed(
  () => props.detail !== null && props.detail.publish_state === 'published' && !props.detail.disabled,
)
const created = computed(() => (props.detail === null ? '' : formatPublishedAt(props.detail.created_at)))
// 分享链接一律使用服务端公开地址，管理页面的域名不能代表图片出口。
const shareUrl = computed(() => (props.detail === null ? '' : postShareUrl(props.detail)))
const publicBase = computed(() => (props.detail === null ? '' : props.detail.public_base_url))
const pendingDelete = computed(() => props.detail?.pending_delete === true)
const shareReady = computed(() => shareUrl.value !== '' && !pendingDelete.value)
// shareHint 说明当前为什么不能分享：撤回与未发布分别给出对应提示。
const shareHint = computed(() =>
  pendingDelete.value
    ? '图集正在删除，分享已停用；可在下方重试删除'
    : props.detail !== null && props.detail.disabled
      ? '已撤回访问，恢复后可分享整帖'
      : '发布完成并生成链接后可分享整帖',
)
const previewValues = computed(() => ({
  title: props.detail?.title ?? '标题',
  description: props.detail?.description ?? '说明',
  // 预览用示例链接，避免模板里的 {{url}} 原样显示成未替换的占位符。
  url: shareUrl.value !== '' ? shareUrl.value : 'https://telegra.ph/Example-1',
  count: props.detail?.images.length ?? 0,
  tags: props.detail?.tags ?? [],
  // 预览在缺少发布日期时用示例日期，确保模板里的时间段可见。
  published_at: formatPublishedDate(props.detail?.published_at) || '2026-11-22',
}))

// previewUrl 取当前预览项的小图；用 computed 而不是模板里直接读 Map，避免响应式差异。
const previewUrl = computed(() => {
  const urls = thumbUrls.value
  const order = previewOrder.value
  if (order === 0 || thumbs?.has(order) !== true) return ''
  return urls.get(order) ?? ''
})

// 气泡底部说明当前预览项与体积，取不到时留空。
const previewSizeText = computed(() => {
  const detail = props.detail
  if (detail === null || previewOrder.value === 0) return ''
  const image = detail.images.find((item) => item.sort_order === previewOrder.value)
  return image === undefined ? '' : formatFileSize(image.file_size)
})

function imageLink(image: { short_code: string; status: string }): string {
  if (props.detail === null) return ''
  return imageShareUrl(props.detail, image, publicBase.value)
}

const toneClass = computed(() => {
  switch (status.value.tone) {
    case 'ok':
      return 'bg-accent-soft text-accent'
    case 'progress':
      return 'bg-brand-soft text-brand-strong'
    case 'error':
      return 'bg-danger-soft text-danger'
    default:
      return 'bg-canvas text-muted'
  }
})

// copy 复制文本并把结果写在复制按钮下方，成功时按钮文字短暂变为已复制。
async function copy(text: string, label: string, action = ''): Promise<void> {
  if (text === '') {
    feedback.value = '暂无可复制的链接'
    return
  }
  const ok = await copyText(text)
  feedback.value = ok ? label + '已复制' : '复制失败，请手动选择'
  if (!ok || action === '') return
  copiedAction.value = action
  if (copiedTimer !== undefined) window.clearTimeout(copiedTimer)
  copiedTimer = window.setTimeout(() => {
    copiedAction.value = ''
  }, 1800)
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
    const result = await setPostDisabled(props.detail.id, !props.detail.disabled)
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
      tags: splitTags(draft.value.tagsText ?? ''),
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
    published_at: formatPublishedDate(props.detail.published_at),
  })
  void copy(text, '分享文案', 'share')
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
  window.addEventListener('resize', onViewportResize)
  scroller.value?.addEventListener('scroll', onScrollerScroll, { passive: true })
  document.body.style.overflow = 'hidden'
  closeButton.value?.focus()
  void syncEagerThumbs()
})

// teardownThumbs 释放上一帖的缩略图、观察器与气泡，避免切帖后仍持有旧对象。
function teardownThumbs(): void {
  observer?.disconnect()
  observer = null
  thumbs?.dispose()
  thumbs = null
  thumbPostId = 0
  thumbUrls.value = new Map()
  closePreview()
}

// ensureThumbs 为当前帖子建一条串行队列：同一时刻最多回源一张管理员媒体。
function ensureThumbs(): ThumbQueue | null {
  const detail = props.detail
  if (detail === null) return null
  if (thumbs !== null && thumbPostId === detail.id) return thumbs
  teardownThumbs()
  const postId = detail.id
  thumbPostId = postId
  thumbs = new ThumbQueue(
    async (order, signal) => {
      const media = await fetchMediaBlob(postId, order, signal)
      if (signal.aborted) throw new Error('已取消')
      return await withTimeout(renderThumb(media), 8000)
    },
    {
      onStore: (order) => {
        if (props.detail?.id !== postId || thumbs === null) return
        const url = thumbs.get(order)
        if (url === '') return
        // 增量写入：详情刷新或列表重建都不影响已取到的小图。
        const next = new Map(thumbUrls.value)
        next.set(order, url)
        thumbUrls.value = next
      },
    },
  )
  return thumbs
}

// requestThumb 按需取一张缩略图：视频与大图只在明确预览时回源。
function requestThumb(order: number, explicit: boolean): void {
  const detail = props.detail
  if (detail === null || order <= 0) return
  const image = detail.images.find((item) => item.sort_order === order)
  if (image === undefined || !canLoadThumb(image)) return
  if (!explicit && needsExplicitPreview(image)) return
  const queue = ensureThumbs()
  if (queue === null || queue.has(order)) return
  queue.request(order)
}

// thumbUrl 返回行内缩略图地址：当前封面直接复用详情里的小图，其余走按需缓存。
function thumbUrl(image: GalleryImage): string {
  // 先读一次缓存，保证首帧就把渲染绑定到缩略图更新上。
  const urls = thumbUrls.value
  if (props.detail !== null && image.sort_order === props.detail.cover_sort_order) {
    const cover = props.detail.cover_url
    if (cover !== '') return cover
  }
  // 已被淘汰并回收的地址不能再渲染，否则会短暂出现裂图。
  if (thumbs?.has(image.sort_order) !== true) return ''
  return urls.get(image.sort_order) ?? ''
}

// observeRows 只观察可视区附近的缩略图位，离开视口不触发任何请求。
function observeRows(): void {
  observer?.disconnect()
  observer = null
  if (typeof IntersectionObserver !== 'function') return
  observer = new IntersectionObserver(
    (entries) => {
      for (const entry of entries) {
        if (!entry.isIntersecting) continue
        const order = Number((entry.target as HTMLElement).dataset.thumbOrder ?? '0')
        requestThumb(order, false)
      }
    },
    { root: null, rootMargin: '120px' },
  )
  scroller.value?.querySelectorAll<HTMLElement>('[data-thumb-order]').forEach((node) => {
    observer?.observe(node)
  })
}

// syncEagerThumbs 把可视区里可自动加载的项一次性排进队列。
async function syncEagerThumbs(): Promise<void> {
  await nextTick()
  const detail = props.detail
  if (detail === null) return
  const nodes = Array.from(scroller.value?.querySelectorAll<HTMLElement>('[data-thumb-order]') ?? [])
  const visible = nodes
    .filter((node) => node.getBoundingClientRect().top < window.innerHeight + 120)
    .map((node) => Number(node.dataset.thumbOrder ?? '0'))
  for (const order of pickEagerOrders(visible, detail.images)) requestThumb(order, false)
  observeRows()
}

// openPreview 在桌面上打开左侧放大气泡；窄屏不弹，避免挤压或超出窗口。
function openPreview(order: number, event: Event): void {
  const target = event.currentTarget as HTMLElement | null
  if (target === null) return
  thumbs?.unpin(previewOrder.value)
  previewAnchor = target
  previewOrder.value = order
  thumbs?.pin(order)
  previewLoading.value = !thumbUrls.value.has(order)
  requestThumb(order, true)
  positionPreview()
}

// positionPreview 按缩略图位置把气泡放在左侧，纵向钳在视口安全区内。
function positionPreview(): void {
  if (previewAnchor === null || previewOrder.value === 0) return
  const rect = previewAnchor.getBoundingClientRect()
  previewPos.value = bubblePosition(
    { left: rect.left, top: rect.top, height: rect.height },
    {
      viewportWidth: window.innerWidth,
      viewportHeight: window.innerHeight,
      bubbleWidth: PREVIEW_WIDTH,
      bubbleHeight: PREVIEW_HEIGHT,
    },
  )
  if (previewPos.value === null) previewOrder.value = 0
}

watch(previewUrl, (url) => {
  if (url !== '') previewLoading.value = false
})

function closePreview(): void {
  thumbs?.unpin(previewOrder.value)
  previewOrder.value = 0
  previewPos.value = null
  previewLoading.value = false
  previewAnchor = null
}

// togglePreview 供触控与键盘使用：点一下打开，再点关闭。
function togglePreview(order: number, event: Event): void {
  if (previewOrder.value === order) {
    closePreview()
    return
  }
  openPreview(order, event)
}

// onScrollerScroll 滚动时直接收起气泡，比跟随重算更稳，也避免滚动中反复重排。
function onScrollerScroll(): void {
  if (previewOrder.value !== 0) closePreview()
}

function onViewportResize(): void {
  if (previewOrder.value !== 0) positionPreview()
}

// setCover 把某一张设为封面：先取管理员媒体生成新缩略图，再交给现有换封面接口。
async function setCover(image: GalleryImage): Promise<void> {
  const detail = props.detail
  if (detail === null || coverBusy.value) return
  if (!canLoadThumb(image) || image.sort_order === detail.cover_sort_order) return
  const postId = detail.id
  coverBusy.value = true
  coverError.value = ''
  try {
    const media = await fetchMediaBlob(postId, image.sort_order)
    // 切帖后旧响应不许改新帖。
    if (props.detail?.id !== postId) return
    const thumbnail = (await makeCoverFromMedia(media)).dataUrl
    await changeCover(postId, image.sort_order, thumbnail)
    if (props.detail?.id !== postId) return
    emit('saved')
  } catch (cause) {
    if (props.detail?.id !== postId) return
    coverError.value = cause instanceof ApiError ? cause.message : '更换封面失败，请稍后重试'
  } finally {
    if (props.detail?.id === postId) coverBusy.value = false
  }
}

onBeforeUnmount(() => {
  window.removeEventListener('keydown', onKeydown)
  window.removeEventListener('resize', onViewportResize)
  scroller.value?.removeEventListener('scroll', onScrollerScroll)
  teardownThumbs()
  if (copiedTimer !== undefined) window.clearTimeout(copiedTimer)
  document.body.style.overflow = ''
  // 关闭后把焦点还给打开抽屉的元素，键盘用户不会丢失位置。
  if (previousFocus !== null && previousFocus.isConnected) previousFocus.focus()
})

// 详情刷新（例如换封面后重载）会重建列表节点，需要重新观察并补齐缺项。
watch(
  () => props.detail,
  (next) => {
    if (next === null) return
    void syncEagerThumbs()
  },
)

watch(
  () => props.detail?.id,
  (next) => {
    if (next === undefined || next === activeId) return
    activeId = next
    confirmToggle.value = false
    feedback.value = ''
    copiedAction.value = ''
    editing.value = false
    editError.value = ''
    toggleError.value = ''
    teardownThumbs()
    void syncEagerThumbs()
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
          <h2 class="mt-1 line-clamp-2 text-base font-semibold leading-snug">
            {{ detail?.title ?? '正在读取…' }}
          </h2>
        </div>
        <div class="flex shrink-0 items-center gap-2">
          <button
            v-if="detail !== null && !editing"
            type="button"
            class="h-11 rounded-full border border-line px-3.5 text-xs font-medium text-muted transition hover:bg-canvas hover:text-ink disabled:opacity-60"
            :disabled="toggling"
            @click="startEdit"
          >
            编辑
          </button>
          <button
            v-if="detail !== null && !editing"
            type="button"
            class="h-11 rounded-full border px-3.5 text-xs font-medium transition disabled:opacity-60"
            :class="detail.disabled ? 'border-accent/40 text-accent hover:bg-accent/10' : 'border-line text-danger hover:bg-danger-soft'"
            :disabled="toggling"
            @click="askToggle"
          >
            {{ toggling ? '处理中…' : toggleActionLabel(detail.disabled) }}
          </button>
          <button
            ref="closeButton"
            type="button"
            class="flex h-11 w-11 shrink-0 items-center justify-center rounded-full border border-line text-muted transition hover:bg-canvas hover:text-ink"
            aria-label="关闭详情"
            @click="emit('close')"
          >
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" class="h-4 w-4">
            <path d="M6 6l12 12M18 6L6 18" stroke-linecap="round" />
          </svg>
          </button>
        </div>
      </header>

      <div ref="scroller" class="flex-1 overflow-y-auto px-5 py-4">
        <p v-if="loading" class="py-10 text-center text-sm text-muted">正在读取详情…</p>

        <div v-else-if="error !== ''" class="rounded-lg border border-line bg-canvas px-4 py-8 text-center">
          <p class="text-sm text-danger">{{ error }}</p>
          <button
            type="button"
            class="mt-3 h-11 rounded-full border border-line px-3.5 text-sm font-medium text-muted transition hover:bg-canvas hover:text-ink"
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
              class="mt-1 h-11 w-full rounded-xl border border-line bg-canvas px-3 text-sm outline-none transition focus:border-brand focus:bg-surface"
            />
          </label>
          <label class="block">
            <span class="text-xs font-medium text-muted">说明</span>
            <textarea
              v-model="draft.description"
              rows="4"
              class="mt-1 w-full rounded-xl border border-line bg-canvas px-3 py-2 text-sm leading-relaxed outline-none transition focus:border-brand focus:bg-surface"
            ></textarea>
          </label>
          <label class="block">
            <span class="text-xs font-medium text-muted">标签</span>
            <input
              v-model="draft.tagsText"
              type="text"
              placeholder="用逗号或空格分隔，留空即清空"
              class="mt-1 h-11 w-full rounded-xl border border-line bg-canvas px-3 text-sm outline-none transition focus:border-brand focus:bg-surface"
            />
          </label>
          <label class="block">
            <span class="text-xs font-medium text-muted">作者</span>
            <input
              v-model="draft.authorName"
              type="text"
              class="mt-1 h-11 w-full rounded-xl border border-line bg-canvas px-3 text-sm outline-none transition focus:border-brand focus:bg-surface"
            />
          </label>

          <p class="text-xs text-muted">
            已发布帖会先更新所有 Telegraph 页面，全部成功后才保存本地记录；分页数量会变化时会被拒绝。
          </p>
          <p v-if="editError !== ''" class="text-xs text-danger" aria-live="polite">{{ editError }}</p>

          <div class="flex items-center gap-2">
            <button
              type="submit"
              class="h-11 rounded-full bg-brand px-4 text-xs font-medium text-brand-ink transition hover:bg-brand-strong disabled:opacity-60"
              :disabled="saving"
            >
              {{ saving ? '正在保存…' : '保存' }}
            </button>
            <button
              type="button"
              class="h-11 rounded-full border border-line px-3.5 text-xs font-medium text-muted transition hover:bg-canvas hover:text-ink"
              :disabled="saving"
              @click="cancelEdit"
            >
              取消
            </button>
          </div>
        </form>

        <template v-else-if="detail !== null">
          <div v-if="confirmToggle" class="mb-3 rounded-xl bg-canvas px-3.5 py-3">
            <p class="text-xs text-ink">{{ toggleConfirmText(detail.disabled) }}</p>
            <div class="mt-2 flex items-center gap-2">
              <button
                type="button"
                class="h-9 rounded-full bg-brand px-3 text-xs font-medium text-brand-ink transition hover:bg-brand-strong disabled:opacity-60"
                :disabled="toggling"
                @click="doToggle"
              >
                确认
              </button>
              <button
                type="button"
                class="h-9 rounded-full border border-line px-3 text-xs font-medium text-muted transition hover:bg-canvas hover:text-ink"
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
            class="mb-3 rounded-xl bg-danger-soft px-3.5 py-2.5 text-xs text-danger"
          >
            {{ toggleHint(true) }}
          </p>

          <div class="flex flex-wrap items-center gap-2">
            <span class="whitespace-nowrap rounded px-1.5 py-0.5 text-xs font-medium" :class="toneClass">{{ status.label }}</span>
            <span class="text-xs text-muted tnum">{{ detail.image_count }} 张图</span>
            <span class="text-xs text-muted">{{ detailUploadModeLabel(detail.upload_mode) }}</span>
            <span
              v-if="showViews"
              class="inline-flex items-center gap-1 text-xs text-muted tnum"
              :title="viewsTitle"
            >
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" class="h-3.5 w-3.5" aria-hidden="true">
                <path d="M2.5 12S6 5.5 12 5.5 21.5 12 21.5 12 18 18.5 12 18.5 2.5 12 2.5 12Z" stroke-linecap="round" stroke-linejoin="round" />
                <circle cx="12" cy="12" r="3" />
              </svg>
              {{ viewsText }}
            </span>
            <span v-else-if="canRefreshViews" class="text-xs text-muted">尚未统计</span>
            <button
              v-if="canRefreshViews"
              type="button"
              class="inline-flex h-8 items-center gap-1 rounded-full border border-line bg-surface px-2.5 text-xs text-muted transition hover:border-ink/25 hover:text-ink disabled:opacity-60"
              :disabled="viewsRefreshing"
              :title="viewsTitle"
              aria-label="刷新浏览量"
              @click="emit('refreshViews')"
            >
              <svg
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                stroke-width="1.75"
                class="h-3.5 w-3.5"
                :class="viewsRefreshing ? 'motion-safe:animate-spin' : ''"
                aria-hidden="true"
              >
                <path d="M20 11a8 8 0 1 0-2.34 5.66" stroke-linecap="round" />
                <path d="M20 5v6h-6" stroke-linecap="round" stroke-linejoin="round" />
              </svg>
              刷新浏览量
            </button>
            <span
              v-if="viewsNotice"
              class="text-xs tnum"
              :class="viewsNoticeTone === 'muted' ? 'text-muted' : 'text-accent'"
              aria-live="polite"
              >{{ viewsNotice }}</span
            >
          </div>

          <p v-if="detail.description !== ''" class="mt-3 whitespace-pre-line text-sm leading-relaxed text-ink/85">
            {{ detail.description }}
          </p>

          <div v-if="detail.tags.length > 0" class="mt-2.5 flex flex-wrap gap-1">
            <span
              v-for="tag in detail.tags"
              :key="tag"
              class="rounded bg-canvas px-1.5 py-0.5 text-xs text-muted"
              >{{ tag }}</span
            >
          </div>

          <section class="mt-4 rounded-2xl bg-canvas px-3.5 py-3.5">
            <div class="flex flex-wrap items-center gap-2">
              <button
                type="button"
                class="inline-flex h-11 min-w-[8.5rem] flex-1 items-center justify-center rounded-full border px-3.5 text-sm font-medium transition disabled:cursor-not-allowed"
                :class="
                  shareReady
                    ? 'border-brand bg-brand text-brand-ink hover:bg-brand-strong'
                    : 'border-line bg-surface text-muted'
                "
                :disabled="!shareReady"
                @click="copy(shareUrl, '链接', 'link')"
              >
                {{ copiedAction === 'link' ? '已复制链接' : '复制链接' }}
              </button>
              <button
                type="button"
                class="inline-flex h-11 min-w-[8.5rem] flex-1 items-center justify-center rounded-full border px-3.5 text-sm font-medium transition disabled:cursor-not-allowed"
                :class="
                  shareReady
                    ? 'border-line bg-surface text-ink hover:border-ink/25'
                    : 'border-line bg-canvas text-muted'
                "
                :disabled="!shareReady"
                @click="copyShareText"
              >
                {{ copiedAction === 'share' ? '已复制文案' : '复制分享文案' }}
              </button>
            </div>
            <p class="mt-2 min-h-[16px] text-xs text-muted" aria-live="polite">
              {{ feedback !== '' ? feedback : shareReady ? '' : shareHint }}
            </p>
          </section>

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

          <section v-if="detail.telegraph_urls.length > 0" class="mt-5">
            <h3 class="text-xs font-medium text-muted">分页链接</h3>
            <ol class="mt-2 space-y-1">
              <li v-for="(url, index) in detail.telegraph_urls" :key="url" class="flex items-center gap-2">
                <span class="w-10 shrink-0 text-xs text-muted tnum">第 {{ index + 1 }} 页</span>
                <a
                  :href="url"
                  target="_blank"
                  rel="noreferrer noopener"
                  class="inline-flex min-h-6 min-w-0 flex-1 items-center truncate text-xs text-brand-strong hover:underline"
                  >{{ url }}</a
                >
                <button
                  type="button"
                  class="h-9 shrink-0 rounded border border-line px-2.5 text-xs text-muted transition hover:bg-canvas"
                  @click="copy(url, '链接')"
                >
                  复制
                </button>
              </li>
            </ol>
          </section>

          <section class="mt-5">
            <div class="flex min-h-6 flex-wrap items-center justify-between gap-2">
              <h3 class="text-xs font-medium text-muted">媒体清单</h3>
              <span v-if="coverBusy" class="text-xs text-muted">正在设置封面…</span>
              <span v-else-if="coverError !== ''" class="text-xs text-danger">{{ coverError }}</span>
            </div>
            <ul class="mt-2 divide-y divide-line/70 overflow-hidden rounded-xl bg-surface">
              <li v-for="image in detail.images" :key="image.short_code" class="px-3 py-2">
                <div
                  class="grid grid-cols-[2.75rem_minmax(0,1fr)] items-start gap-x-3 gap-y-2 sm:grid-cols-[2.75rem_minmax(0,1fr)_5.25rem_3.75rem_3.25rem] sm:items-center sm:gap-x-2.5"
                >
                  <button
                    type="button"
                    :data-thumb-order="image.sort_order"
                    class="flex h-11 w-11 shrink-0 items-center justify-center overflow-hidden rounded-lg border border-line bg-canvas transition hover:border-brand/40 focus-visible:outline focus-visible:outline-2 focus-visible:outline-brand"
                    :aria-label="`放大预览第 ${image.sort_order} 项`"
                    :aria-expanded="previewOrder === image.sort_order"
                    @mouseenter="openPreview(image.sort_order, $event)"
                    @mouseleave="closePreview"
                    @focus="openPreview(image.sort_order, $event)"
                    @blur="closePreview"
                    @click="togglePreview(image.sort_order, $event)"
                  >
                    <img
                      v-if="thumbUrl(image) !== ''"
                      :src="thumbUrl(image)"
                      alt=""
                      class="h-full w-full object-cover"
                      loading="lazy"
                      decoding="async"
                    />
                    <svg
                      v-else-if="image.mime_type === 'video/mp4'"
                      viewBox="0 0 24 24"
                      fill="none"
                      stroke="currentColor"
                      stroke-width="1.6"
                      class="h-4 w-4 text-muted"
                      aria-hidden="true"
                    >
                      <path d="M8 5.5v13l10-6.5-10-6.5Z" stroke-linejoin="round" />
                    </svg>
                    <svg
                      v-else
                      viewBox="0 0 24 24"
                      fill="none"
                      stroke="currentColor"
                      stroke-width="1.5"
                      class="h-4 w-4 text-muted"
                      aria-hidden="true"
                    >
                      <rect x="3.5" y="5.5" width="17" height="13" rx="2" />
                      <path d="m7 15 3.5-3.5L13 14l2-2 2 3" stroke-linecap="round" stroke-linejoin="round" />
                    </svg>
                  </button>

                  <div class="min-w-0">
                    <p class="flex items-center gap-1.5 text-xs font-medium tnum">
                      第 {{ image.sort_order }} 项
                      <span
                        v-if="image.mime_type === 'video/mp4'"
                        class="rounded bg-canvas px-1.5 py-0.5 text-[11px] font-medium text-ink"
                        >MP4</span
                      >
                    </p>
                    <p class="mt-0.5 truncate text-xs text-muted">
                      {{ image.mime_type }} · {{ formatFileSize(image.file_size) }}
                    </p>
                    <p v-if="!canLoadThumb(image)" class="mt-0.5 truncate text-xs text-muted">未存入频道，暂无缩略图</p>
                    <button
                      v-if="image.mime_type === 'video/mp4'"
                      type="button"
                      class="mt-1 inline-flex h-7 items-center rounded-full border border-line px-2.5 text-[11px] text-muted transition hover:bg-canvas"
                      @click="togglePlay(image.short_code)"
                    >
                      {{ playingCode === image.short_code ? '收起' : '播放' }}
                    </button>
                  </div>

                  <div class="col-start-2 flex flex-wrap items-center gap-2 sm:contents">
                    <span
                      v-if="image.sort_order === detail.cover_sort_order"
                      class="inline-flex h-8 min-w-[5.25rem] items-center justify-center whitespace-nowrap rounded-full bg-brand-soft px-2.5 text-xs font-medium text-brand-strong sm:col-start-3 sm:w-full"
                      >当前封面</span
                    >
                    <button
                      v-else
                      type="button"
                      class="inline-flex h-8 min-w-[5.25rem] items-center justify-center whitespace-nowrap rounded-full border border-line bg-surface px-2.5 text-xs font-medium text-muted transition hover:border-brand/40 hover:text-brand-strong disabled:opacity-60 sm:col-start-3 sm:w-full"
                      :disabled="coverBusy || !canLoadThumb(image) || detail.pending_delete"
                      :aria-label="`将第 ${image.sort_order} 项设为封面`"
                      @click="setCover(image)"
                    >
                      {{ coverBusy ? '设置中…' : '设为封面' }}
                    </button>
                    <a
                      v-if="imageLink(image) !== ''"
                      :href="imageLink(image)"
                      target="_blank"
                      rel="noreferrer noopener"
                      class="inline-flex min-h-8 items-center whitespace-nowrap text-xs text-brand-strong hover:underline sm:col-start-4 sm:justify-self-end"
                      >查看外链</a
                    >
                    <span v-else class="hidden sm:col-start-4 sm:block" aria-hidden="true"></span>
                    <button
                      type="button"
                      class="inline-flex h-8 items-center rounded-full border border-line px-2.5 text-xs text-muted transition hover:bg-canvas disabled:opacity-50 sm:col-start-5 sm:justify-self-end"
                      :disabled="imageLink(image) === ''"
                      @click="copy(imageLink(image), '单图外链')"
                    >
                      复制
                    </button>
                  </div>
                </div>
                <video
                  v-if="playingCode === image.short_code && imageLink(image) !== ''"
                  :src="imageLink(image)"
                  controls
                  playsinline
                  preload="metadata"
                  class="mt-2 w-full rounded-lg bg-ink"
                />
              </li>
            </ul>
            <p v-if="detail.images.length === 0" class="mt-2 text-xs text-muted">还没有已上传的图片。</p>
          </section>

          <div class="mt-5 border-t border-line pt-3">
            <div
              v-if="confirmDelete"
              class="rounded-xl border border-danger/25 bg-danger-soft/70 px-3.5 py-3"
            >
              <p class="text-xs leading-relaxed text-danger">
                <template v-if="pendingDelete">上次删除未完成，已保留待删除记录并停用短码；可再次确认重试，远端页面会重新清空。</template>
                <template v-else>
                  删除后本站链接立即停用，已发布的 Telegraph 页面会清空为「此图集已撤回」，本地记录与封面一并移除，且无法恢复。
                  Telegraph 原地址仍可能被访问到；Telegram 私有频道里的原图与历史备份继续保留。
                </template>
              </p>
              <div class="mt-2 flex flex-wrap items-center gap-2">
                <button
                  type="button"
                  class="h-9 rounded-full bg-danger px-3 text-xs font-medium text-white transition hover:bg-danger/90 disabled:opacity-60"
                  :disabled="deleting"
                  @click="emit('delete')"
                >
                  {{ deleting ? '正在删除…' : pendingDelete ? '重试删除' : '确认删除' }}
                </button>
                <button
                  type="button"
                  class="h-9 rounded-full border border-line px-3 text-xs font-medium text-muted transition hover:bg-canvas hover:text-ink"
                  :disabled="deleting"
                  @click="confirmDelete = false"
                >
                  取消
                </button>
              </div>
              <p v-if="deleteError !== undefined && deleteError !== ''" class="mt-2 text-xs text-danger" aria-live="polite">
                {{ deleteError }}
              </p>
            </div>
            <button
              v-else
              type="button"
              class="h-9 rounded-full border border-danger/35 px-3 text-xs font-medium text-danger transition hover:bg-danger-soft disabled:opacity-60"
              :disabled="deleting"
              @click="confirmDelete = true"
            >
              删除图集
            </button>
          </div>

          <details class="mt-5 border-t border-line pt-3 text-xs">
            <summary class="inline-flex min-h-6 cursor-pointer select-none items-center py-1 text-muted hover:text-ink">
              分享文案模板
            </summary>
            <div class="mt-2 space-y-2">
              <textarea
                id="share-template"
                v-model="templateDraft"
                aria-label="分享文案模板"
                rows="3"
                class="w-full rounded-xl border border-line bg-canvas px-2.5 py-2 text-xs leading-relaxed outline-none transition focus:border-brand focus:bg-surface"
              ></textarea>
              <div class="flex flex-wrap gap-1">
                <button
                  v-for="token in SHARE_PLACEHOLDERS"
                  :key="token"
                  type="button"
                  class="inline-flex min-h-6 items-center rounded border border-line px-2 py-1 font-mono text-xs text-muted transition hover:bg-canvas hover:text-ink"
                  @click="insertPlaceholder(token)"
                >
                  {{ token }}
                </button>
              </div>
              <div class="flex flex-wrap items-center gap-2">
                <button
                  type="button"
                  class="h-9 rounded-full bg-brand px-3 text-xs font-medium text-brand-ink transition hover:bg-brand-strong"
                  @click="applyTemplate"
                >
                  保存模板
                </button>
                <button
                  type="button"
                  class="h-9 rounded-full border border-line px-3 text-xs font-medium text-muted transition hover:bg-canvas hover:text-ink"
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
        </template>
      </div>

      <Teleport to="body">
        <div
          v-if="previewOrder !== 0 && previewPos !== null"
          class="pointer-events-none fixed z-50"
          :style="{ right: previewPos.right + 'px', top: previewPos.top + 'px' }"
        >
          <div class="rounded-2xl border border-line bg-surface p-2 shadow-lg">
            <div class="flex h-[224px] w-[224px] items-center justify-center overflow-hidden rounded-xl bg-canvas">
              <img v-if="previewUrl !== ''" :src="previewUrl" alt="" class="h-full w-full object-contain" />
              <span v-else class="text-xs text-muted">{{ previewLoading ? '正在读取…' : '暂无缩略图' }}</span>
            </div>
            <p class="mt-1.5 px-1 text-[11px] text-muted tnum">第 {{ previewOrder }} 项 · {{ previewSizeText }}</p>
          </div>
        </div>
      </Teleport>
    </aside>
  </div>
</template>
