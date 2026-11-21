<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import type { PostDetail } from '../../api/gallery.ts'
import {
  DEFAULT_SHARE_TEMPLATE,
  formatFileSize,
  formatPublishedAt,
  imageShareUrl,
  postShareUrl,
  renderShareText,
  statusLabel,
} from '../../utils/gallery.ts'

const props = defineProps<{
  detail: PostDetail | null
  loading: boolean
  error: string
}>()

const emit = defineEmits<{ close: []; retry: [] }>()

const closeButton = ref<HTMLButtonElement | null>(null)
const feedback = ref('')

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

function copyShareText(): void {
  if (props.detail === null) return
  const text = renderShareText(DEFAULT_SHARE_TEMPLATE, {
    title: props.detail.title,
    description: props.detail.description,
    url: shareUrl.value,
    count: props.detail.images.length,
    tags: props.detail.tags,
  })
  void copy(text, '分享文案')
}

function onKeydown(event: KeyboardEvent): void {
  if (event.key === 'Escape') emit('close')
}

onMounted(() => {
  window.addEventListener('keydown', onKeydown)
  document.body.style.overflow = 'hidden'
  closeButton.value?.focus()
})

onBeforeUnmount(() => {
  window.removeEventListener('keydown', onKeydown)
  document.body.style.overflow = ''
})

watch(
  () => props.detail?.id,
  () => {
    feedback.value = ''
  },
)
</script>

<template>
  <div class="fixed inset-0 z-40 flex justify-end bg-ink/45" @click.self="emit('close')">
    <aside
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
        <button
          ref="closeButton"
          type="button"
          class="flex h-9 w-9 shrink-0 items-center justify-center rounded-md border border-line text-muted transition hover:bg-canvas"
          aria-label="关闭详情"
          @click="emit('close')"
        >
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" class="h-4 w-4">
            <path d="M6 6l12 12M18 6L6 18" stroke-linecap="round" />
          </svg>
        </button>
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

        <template v-else-if="detail !== null">
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
      </footer>
    </aside>
  </div>
</template>
