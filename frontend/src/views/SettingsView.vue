<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { ApiError } from '../api/client.ts'
import {
  fetchBackupStatus,
  fetchSettings,
  fetchTraffic,
  saveSettings,
  testConnection,
  triggerBackup,
  SettingsError,
  type BackupStatus,
  type ConnectionResult,
  type SettingsView,
  type TrafficStats,
} from '../api/settings.ts'
import {
  channelInvalid,
  changedPatch,
  connectionLabel,
  connectionLatency,
  connectionSummary,
  connectionTone,
  endpointText,
  ENV_MANAGED_HINT,
  FIELD_PROXY_MODE,
  FIELD_PROXY_TELEGRAM,
  FIELD_PROXY_TELEGRAPH,
  FIELD_PROXY_URL,
  FIELD_TELEGRAM_CHANNEL,
  FIELD_TELEGRAM_TOKEN,
  FIELD_TELEGRAPH_AUTHOR,
  hasPatch,
  PROXY_MODES,
  proxyCustomNeedsURL,
  proxyModeLabel,
  proxyURLHint,
  proxyURLInvalid,
  proxyURLText,
  backupLatestText,
  backupScheduleText,
  saveFailureText,
  tokenPlaceholder,
  formatTrafficBytes,
  trafficState,
  trafficStateText,
  trafficSummary,
} from '../utils/settings.ts'

const view = ref<SettingsView | null>(null)
const loading = ref(true)
const loadError = ref('')
const saving = ref(false)
const feedback = ref('')
const saveError = ref('')
const fieldErrors = ref<string[]>([])
const traffic = ref<TrafficStats | null>(null)
const trafficError = ref('')
const backup = ref<BackupStatus | null>(null)
const backupError = ref('')
const backupNote = ref('')
const backing = ref(false)
const checking = ref(false)
const checkError = ref('')
const result = ref<ConnectionResult | null>(null)
// activeSection 跟随滚动更新，左侧目录用它显示当前所在分组。
const activeSection = ref('upstream')
let sectionObserver: IntersectionObserver | undefined

// 分组导航：桌面左侧目录与手机顶部横向目录共用同一份锚点。
const SETTING_NAV = [
  { id: 'upstream', label: '上游与代理' },
  { id: 'defaults', label: '发帖默认值' },
  { id: 'check', label: '连通性自检' },
  { id: 'ops', label: '流量与备份' },
]

// fieldErrorText 给字段就近的报错文案，避免只在底部提示。
function fieldErrorText(field: string): string {
  if (!fieldErrors.value.includes(field)) return ''
  if (field === FIELD_TELEGRAM_CHANNEL) return '需为 @用户名 或数字编号'
  if (field === FIELD_TELEGRAPH_AUTHOR) return '作者名不超过 100 字'
  if (field === FIELD_PROXY_URL) return '需为 http://、https:// 或 socks5:// 地址，自定义模式必须填写'
  return '该项填写有误'
}

const form = reactive({
  token: '',
  channelId: '',
  authorName: '',
  proxyMode: 'direct',
  proxyUrl: '',
  proxyTelegram: false,
  proxyTelegraph: false,
  metaFilenames: '',
  metaParseMode: 'first-line',
})

function applyView(next: SettingsView): void {
  view.value = next
  form.channelId = next.telegram.channel_id
  form.authorName = next.telegraph.author_name
  form.token = ''
  form.proxyMode = next.proxy.mode
  form.proxyUrl = ''
  form.proxyTelegram = next.proxy.telegram_enabled
  form.proxyTelegraph = next.proxy.telegraph_enabled
  form.metaFilenames = next.meta.filenames.join(', ')
  form.metaParseMode = next.meta.parse_mode
}

function managed(field: string): boolean {
  return view.value !== null && view.value.env_managed.includes(field)
}

async function loadTraffic(): Promise<void> {
  trafficError.value = ''
  try {
    traffic.value = await fetchTraffic()
  } catch (cause) {
    trafficError.value = cause instanceof ApiError && cause.status === 401
      ? '登录已失效，请重新登录后再查看'
      : '读取流量统计失败'
  }
}

async function loadBackup(): Promise<void> {
  backupError.value = ''
  try {
    backup.value = await fetchBackupStatus()
  } catch (cause) {
    backupError.value = cause instanceof ApiError && cause.status === 401
      ? '登录已失效，请重新登录后再查看'
      : '读取备份状态失败'
  }
}

async function runBackup(): Promise<void> {
  if (backing.value) return
  backing.value = true
  backupError.value = ''
  backupNote.value = ''
  try {
    const result = await triggerBackup()
    backupNote.value = result.warning !== undefined
      ? result.warning
      : '备份完成：' + result.name + '（' + formatTrafficBytes(result.size) + '）'
    await loadBackup()
  } catch (cause) {
    backupError.value = cause instanceof SettingsError ? cause.message : '备份失败，请稍后重试'
  } finally {
    backing.value = false
  }
}

async function load(): Promise<void> {
  loading.value = true
  loadError.value = ''
  try {
    applyView(await fetchSettings())
    await loadTraffic()
    await loadBackup()
  } catch (cause) {
    loadError.value = cause instanceof ApiError && cause.status === 401
      ? '登录已失效，请重新登录后再打开设置'
      : '读取设置失败，请稍后重试'
  } finally {
    loading.value = false
  }
}

function failText(cause: unknown): { message: string; fields: string[] } {
  if (cause instanceof SettingsError) {
    return { message: saveFailureText(cause.status, cause.message, cause.fields), fields: cause.fields }
  }
  if (cause instanceof ApiError) {
    return { message: saveFailureText(cause.status, cause.message, []), fields: [] }
  }
  return { message: '保存失败，请稍后重试', fields: [] }
}

async function save(): Promise<void> {
  if (view.value === null || saving.value) return
  saveError.value = ''
  fieldErrors.value = []
  feedback.value = ''

  const patch = changedPatch(view.value, form)
  if (!hasPatch(patch)) {
    feedback.value = '没有需要保存的改动'
    return
  }
  if (patch.telegram?.channel_id !== undefined && channelInvalid(patch.telegram.channel_id)) {
    saveError.value = '频道标识需为 @用户名 或数字编号'
    fieldErrors.value = [FIELD_TELEGRAM_CHANNEL]
    return
  }
  if (patch.telegraph?.author_name !== undefined && Array.from(patch.telegraph.author_name).length > 100) {
    saveError.value = '作者名不超过 100 字'
    fieldErrors.value = [FIELD_TELEGRAPH_AUTHOR]
    return
  }
  if (patch.proxy?.url !== undefined && proxyURLInvalid(patch.proxy.url)) {
    saveError.value = '代理地址需为 http://、https:// 或 socks5:// 格式'
    fieldErrors.value = [FIELD_PROXY_URL]
    return
  }
  if (proxyCustomNeedsURL(form.proxyMode, view.value.proxy.url_set, form.proxyUrl)) {
    saveError.value = '自定义代理需要填写代理地址'
    fieldErrors.value = [FIELD_PROXY_URL]
    return
  }

  saving.value = true
  try {
    applyView(await saveSettings(patch))
    feedback.value = '设置已保存并立即生效'
  } catch (cause) {
    const failure = failText(cause)
    saveError.value = failure.message
    fieldErrors.value = failure.fields
  } finally {
    saving.value = false
  }
}

async function clearToken(): Promise<void> {
  if (view.value === null || saving.value) return
  saving.value = true
  saveError.value = ''
  feedback.value = ''
  try {
    applyView(await saveSettings({ telegram: { token: '' } }))
    feedback.value = '已清除保存的 Bot Token'
  } catch (cause) {
    saveError.value = failText(cause).message
  } finally {
    saving.value = false
  }
}

async function clearProxy(): Promise<void> {
  if (view.value === null || saving.value) return
  saving.value = true
  saveError.value = ''
  feedback.value = ''
  try {
    applyView(await saveSettings({ proxy: { url: '' } }))
    feedback.value = '已清除保存的代理地址'
  } catch (cause) {
    saveError.value = failText(cause).message
  } finally {
    saving.value = false
  }
}

async function check(): Promise<void> {
  if (checking.value) return
  checking.value = true
  checkError.value = ''
  try {
    result.value = await testConnection()
  } catch (cause) {
    checkError.value = cause instanceof ApiError && cause.status === 401
      ? '登录已失效，请重新登录后再检测'
      : '连通性检测失败，请稍后重试'
  } finally {
    checking.value = false
  }
}

onMounted(() => {
  void load()
})

// 设置读取完成、分组渲染出来之后才能观察锚点，否则一开始找不到节点。
watch(view, async (next) => {
  if (next === null) return
  await nextTick()
  observeSections()
})

// observeSections 让左侧目录跟随滚动定位；设置未加载时直接跳过。
function observeSections(): void {
  sectionObserver?.disconnect()
  const targets = SETTING_NAV.map((item) => document.getElementById(item.id)).filter(
    (node): node is HTMLElement => node !== null,
  )
  if (typeof IntersectionObserver === 'undefined' || targets.length === 0) return
  sectionObserver = new IntersectionObserver(
    (entries) => {
      const visible = entries
        .filter((entry) => entry.isIntersecting)
        .sort((left, right) => right.intersectionRatio - left.intersectionRatio)[0]
      if (visible !== undefined) activeSection.value = visible.target.id
    },
    { rootMargin: '-96px 0px -55% 0px', threshold: [0.1, 0.4] },
  )
  targets.forEach((target) => sectionObserver?.observe(target))
}

// selectSection 让点按立即更新色点，不依赖滚动观察的下一帧。
function selectSection(id: string): void {
  activeSection.value = id
}

onBeforeUnmount(() => {
  sectionObserver?.disconnect()
  sectionObserver = undefined
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
            <p class="truncate text-sm font-semibold tracking-tight">设置</p>
            <p class="mt-0.5 truncate text-xs text-muted">上游 · 发帖 · 自检 · 流量</p>
          </div>
        </div>
        <router-link
          :to="{ name: 'home' }"
          class="inline-flex h-11 shrink-0 items-center rounded-md border border-line px-3 text-sm font-medium transition hover:border-ink/25 hover:bg-canvas"
        >
          返回图集
        </router-link>
      </div>

      <nav class="flex gap-2 overflow-x-auto border-t border-line px-5 py-2 lg:hidden" aria-label="设置分组">
        <a
          v-for="item in SETTING_NAV"
          :key="item.id"
          :href="'#' + item.id"
          class="inline-flex h-11 shrink-0 items-center rounded border px-3 text-xs font-medium transition hover:border-ink/25 hover:text-ink"
          :class="activeSection === item.id ? 'border-accent bg-accent-soft text-accent' : 'border-line text-muted'"
          @click="selectSection(item.id)"
        >
          {{ item.label }}
        </a>
      </nav>
    </header>

    <main class="mx-auto w-full max-w-[1240px] px-5 py-7">
      <div class="flex items-start gap-8">
      <nav class="sticky top-24 hidden w-[168px] shrink-0 lg:block" aria-label="设置分组">
        <ul class="space-y-0.5">
          <li v-for="item in SETTING_NAV" :key="item.id">
            <a
              :href="'#' + item.id"
              class="flex items-center gap-2 rounded-md px-3 py-2 text-sm transition hover:bg-canvas hover:text-ink"
              @click="selectSection(item.id)"
              :class="activeSection === item.id ? 'text-ink' : 'text-muted'"
            >
              <span
                class="h-1.5 w-1.5 shrink-0 rounded-full transition"
                :class="activeSection === item.id ? 'bg-brand' : 'bg-transparent'"
                aria-hidden="true"
              ></span>
              {{ item.label }}
            </a>
          </li>
        </ul>

        <div class="mt-4 border-t border-line pt-4">
          <p class="text-xs leading-relaxed" aria-live="polite">
            <span v-if="saveError !== ''" class="text-danger">{{ saveError }}</span>
            <span v-else-if="feedback !== ''" class="text-accent">{{ feedback }}</span>
            <span v-else class="text-muted">已发布帖会保留原页面路径，修改作者名只影响后续发布。</span>
          </p>
          <button
            type="button"
            :disabled="saving"
            class="mt-3 h-11 w-full rounded-md bg-brand px-4 text-sm font-medium text-brand-ink transition hover:bg-brand-strong disabled:opacity-60"
            @click="save"
          >
            {{ saving ? '保存中…' : '保存设置' }}
          </button>
        </div>
      </nav>

      <div class="min-w-0 max-w-[880px] flex-1">
        <p v-if="loading" class="py-16 text-center text-sm text-muted">正在读取设置…</p>

        <div v-else-if="loadError !== ''" class="rounded-md border border-line bg-surface px-4 py-10 text-center">
          <p class="text-sm text-danger">{{ loadError }}</p>
          <button
            type="button"
            class="mt-3 h-10 rounded-md border border-line px-3 text-sm font-medium transition hover:bg-canvas"
            @click="load"
          >
            重试
          </button>
        </div>

        <template v-else-if="view !== null">
          <div class="space-y-8">
            <section id="upstream" class="scroll-mt-36">
              <div class="flex items-center gap-2">
                <span class="tnum rounded border border-line bg-surface px-1.5 py-0.5 text-xs font-medium leading-none text-muted">01</span>
                <h2 class="text-base font-semibold tracking-tight">上游与代理</h2>
              </div>
              <p class="mt-1 text-xs text-muted">图片与发布的上游凭据、代理走向，保存后立即重建传输层。</p>

              <div class="mt-3 space-y-3">
                <div class="rounded-md border border-line bg-surface">
                  <div class="border-b border-line px-5 py-4">
                    <h3 class="text-sm font-semibold">Telegram 图床</h3>
                    <p class="mt-1 text-xs text-muted">图片经该 Bot 存入私有频道，保存后上传队列与图片网关立即使用新配置。</p>
                  </div>
                  <div class="space-y-4 px-5 py-4">
                    <label class="block">
                      <span class="text-xs font-medium text-muted">Bot Token</span>
                      <div class="mt-1 flex items-center gap-2">
                        <input
                          v-model="form.token"
                          type="password"
                          autocomplete="off"
                          :disabled="saving || managed(FIELD_TELEGRAM_TOKEN)"
                          :placeholder="tokenPlaceholder(view.telegram.token_set)"
                          class="h-10 min-w-0 flex-1 rounded-md border border-line bg-canvas px-3 text-sm outline-none transition focus:border-ink/40 focus:bg-surface disabled:opacity-60"
                        />
                        <button
                          v-if="view.telegram.token_set && !managed(FIELD_TELEGRAM_TOKEN)"
                          type="button"
                          :disabled="saving"
                          class="h-10 shrink-0 rounded-md border border-line px-3 text-xs font-medium text-muted transition hover:bg-canvas disabled:opacity-60"
                          @click="clearToken"
                        >
                          清除
                        </button>
                      </div>
                      <p class="mt-1 text-xs text-muted">
                        <template v-if="managed(FIELD_TELEGRAM_TOKEN)">{{ ENV_MANAGED_HINT }}</template>
                        <template v-else-if="view.telegram.token_set">令牌保存在服务端，不会回显</template>
                        <template v-else>尚未设置，填入后即可上传图片</template>
                      </p>
                    </label>

                    <label class="block">
                      <span class="text-xs font-medium text-muted">频道标识</span>
                      <input
                        v-model="form.channelId"
                        type="text"
                        autocomplete="off"
                        :disabled="saving || managed(FIELD_TELEGRAM_CHANNEL)"
                        placeholder="@channel 或 -1001234567890"
                        class="mt-1 h-10 w-full rounded-md border border-line bg-canvas px-3 text-sm outline-none transition focus:border-ink/40 focus:bg-surface disabled:opacity-60"
                        :class="fieldErrors.includes(FIELD_TELEGRAM_CHANNEL) ? 'border-danger' : ''"
                      />
                      <p v-if="fieldErrorText(FIELD_TELEGRAM_CHANNEL) !== ''" class="mt-1 text-xs text-danger">
                        {{ fieldErrorText(FIELD_TELEGRAM_CHANNEL) }}
                      </p>
                      <p v-else-if="managed(FIELD_TELEGRAM_CHANNEL)" class="mt-1 text-xs text-muted">
                        {{ ENV_MANAGED_HINT }}
                      </p>
                    </label>

                    <div class="rounded-md border border-line bg-canvas px-3 py-2.5">
                      <p class="text-xs text-muted">上游地址</p>
                      <p class="mt-0.5 text-sm">{{ endpointText(view.telegram.api_base_source) }}</p>
                    </div>
                  </div>
                </div>

                <div class="rounded-md border border-line bg-surface">
                  <div class="border-b border-line px-5 py-4">
                    <h3 class="text-sm font-semibold">Telegraph 发布</h3>
                    <p class="mt-1 text-xs text-muted">发布页面的作者名由这里提供，令牌通过「申请账户」或环境变量写入。</p>
                  </div>
                  <div class="space-y-4 px-5 py-4">
                    <label class="block">
                      <span class="text-xs font-medium text-muted">作者名</span>
                      <input
                        v-model="form.authorName"
                        type="text"
                        autocomplete="off"
                        :disabled="saving || managed(FIELD_TELEGRAPH_AUTHOR)"
                        placeholder="留空则使用 Telegraph 默认作者"
                        class="mt-1 h-10 w-full rounded-md border border-line bg-canvas px-3 text-sm outline-none transition focus:border-ink/40 focus:bg-surface disabled:opacity-60"
                        :class="fieldErrors.includes(FIELD_TELEGRAPH_AUTHOR) ? 'border-danger' : ''"
                      />
                      <p v-if="fieldErrorText(FIELD_TELEGRAPH_AUTHOR) !== ''" class="mt-1 text-xs text-danger">
                        {{ fieldErrorText(FIELD_TELEGRAPH_AUTHOR) }}
                      </p>
                      <p v-else-if="managed(FIELD_TELEGRAPH_AUTHOR)" class="mt-1 text-xs text-muted">
                        {{ ENV_MANAGED_HINT }}
                      </p>
                    </label>

                    <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
                      <div class="rounded-md border border-line bg-canvas px-3 py-2.5">
                        <p class="text-xs text-muted">访问令牌</p>
                        <p class="mt-0.5 text-sm">{{ view.telegraph.token_set ? '已配置' : '未配置' }}</p>
                      </div>
                      <div class="rounded-md border border-line bg-canvas px-3 py-2.5">
                        <p class="text-xs text-muted">上游地址</p>
                        <p class="mt-0.5 text-sm">{{ endpointText(view.telegraph.api_base_source) }}</p>
                      </div>
                    </div>
                  </div>
                </div>

                <div class="rounded-md border border-line bg-surface">
                  <div class="border-b border-line px-5 py-4">
                    <h3 class="text-sm font-semibold">网络代理</h3>
                    <p class="mt-1 text-xs text-muted">
                      分别控制两项上游是否经代理访问，保存后立即重建传输层。自定义地址可带账密，不会回显。
                    </p>
                  </div>
                  <div class="space-y-4 px-5 py-4">
                    <fieldset>
                      <legend class="text-xs font-medium text-muted">代理模式</legend>
                      <div class="mt-1 grid grid-cols-1 gap-2 sm:grid-cols-3">
                        <label
                          v-for="option in PROXY_MODES"
                          :key="option.value"
                          class="flex min-h-11 cursor-pointer items-center gap-2.5 rounded-md border border-line bg-canvas px-3 py-2.5 text-sm transition hover:border-ink/25 focus-within:border-ink"
                          :class="form.proxyMode === option.value ? 'border-accent bg-accent-soft' : ''"
                        >
                          <input
                            v-model="form.proxyMode"
                            type="radio"
                            name="proxy-mode"
                            :value="option.value"
                            :disabled="saving || managed(FIELD_PROXY_MODE)"
                            class="h-6 w-6 shrink-0 accent-accent"
                          />
                          <span>{{ option.label }}</span>
                        </label>
                      </div>
                      <p class="mt-1 text-xs text-muted">
                        <template v-if="managed(FIELD_PROXY_MODE)">{{ ENV_MANAGED_HINT }}</template>
                        <template v-else>当前生效：{{ proxyModeLabel(view.proxy.mode) }}</template>
                      </p>
                    </fieldset>

                    <label class="block">
                      <span class="text-xs font-medium text-muted">自定义代理地址</span>
                      <div class="mt-1 flex items-center gap-2">
                        <input
                          v-model="form.proxyUrl"
                          type="password"
                          autocomplete="off"
                          :disabled="saving || managed(FIELD_PROXY_URL)"
                          :placeholder="proxyURLHint(view.proxy.url_set, view.proxy.mode)"
                          class="h-10 min-w-0 flex-1 rounded-md border border-line bg-canvas px-3 text-sm outline-none transition focus:border-ink/40 focus:bg-surface disabled:opacity-60"
                          :class="fieldErrors.includes(FIELD_PROXY_URL) ? 'border-danger' : ''"
                        />
                        <button
                          v-if="view.proxy.url_set && view.proxy.mode !== 'custom' && !managed(FIELD_PROXY_URL)"
                          type="button"
                          :disabled="saving"
                          class="h-10 shrink-0 rounded-md border border-line px-3 text-xs font-medium text-muted transition hover:bg-canvas disabled:opacity-60"
                          @click="clearProxy"
                        >
                          清除
                        </button>
                      </div>
                      <p v-if="fieldErrorText(FIELD_PROXY_URL) !== ''" class="mt-1 text-xs text-danger">
                        {{ fieldErrorText(FIELD_PROXY_URL) }}
                      </p>
                      <p v-else-if="managed(FIELD_PROXY_URL)" class="mt-1 text-xs text-muted">{{ ENV_MANAGED_HINT }}</p>
                      <p v-else class="mt-1 text-xs text-muted">
                        {{ proxyURLText(view.proxy.url_set, view.proxy.url_source) }}；切回直连后才能清除已保存的地址。
                      </p>
                    </label>

                    <div class="grid grid-cols-1 gap-2 sm:grid-cols-2">
                      <label
                        class="flex min-h-11 cursor-pointer items-center gap-2.5 rounded-md border border-line bg-canvas px-3 py-2.5 text-sm transition hover:border-ink/25 focus-within:border-ink"
                      >
                        <input
                          v-model="form.proxyTelegram"
                          type="checkbox"
                          :disabled="saving || form.proxyMode === 'direct' || managed(FIELD_PROXY_TELEGRAM)"
                          class="h-6 w-6 shrink-0 accent-accent"
                        />
                        <span>Telegram Bot API 走代理</span>
                      </label>
                      <label
                        class="flex min-h-11 cursor-pointer items-center gap-2.5 rounded-md border border-line bg-canvas px-3 py-2.5 text-sm transition hover:border-ink/25 focus-within:border-ink"
                      >
                        <input
                          v-model="form.proxyTelegraph"
                          type="checkbox"
                          :disabled="saving || form.proxyMode === 'direct' || managed(FIELD_PROXY_TELEGRAPH)"
                          class="h-6 w-6 shrink-0 accent-accent"
                        />
                        <span>Telegraph API 走代理</span>
                      </label>
                    </div>
                    <p class="text-xs text-muted">
                      直连模式忽略上面的开关；跟随系统模式读取进程环境变量 HTTP_PROXY / HTTPS_PROXY / ALL_PROXY。
                    </p>
                  </div>
                </div>
              </div>
            </section>

            <section id="defaults" class="scroll-mt-36">
              <div class="flex items-center gap-2">
                <span class="tnum rounded border border-line bg-surface px-1.5 py-0.5 text-xs font-medium leading-none text-muted">02</span>
                <h2 class="text-base font-semibold tracking-tight">发帖默认值</h2>
              </div>
              <p class="mt-1 text-xs text-muted">说明文件命中后自动预填标题与说明，工作台的解析方式默认按这里。</p>

              <div class="mt-3 rounded-md border border-line bg-surface">
                <div class="space-y-4 px-5 py-4">
                  <div>
                    <label class="block text-xs font-medium text-muted" for="meta-filenames">说明文件名</label>
                    <input
                      id="meta-filenames"
                      v-model="form.metaFilenames"
                      type="text"
                      class="mt-1 h-10 w-full rounded-md border border-line bg-canvas px-3 text-sm outline-none transition focus:border-ink/40 focus:bg-surface"
                      placeholder="info.txt, meta.txt, readme.txt"
                    />
                    <p class="mt-1 text-xs text-muted">多个文件名用逗号分隔，最多 10 个，匹配时不区分大小写。</p>
                  </div>
                  <div>
                    <label class="block text-xs font-medium text-muted" for="meta-parse-mode">解析方式</label>
                    <select
                      id="meta-parse-mode"
                      v-model="form.metaParseMode"
                      class="mt-1 h-10 w-full rounded-md border border-line bg-canvas px-3 text-sm outline-none transition focus:border-ink/40 focus:bg-surface"
                    >
                      <option value="first-line">首行标题 + 其余正文</option>
                      <option value="key-value">键值匹配</option>
                    </select>
                  </div>
                  <div>
                    <span class="block text-xs font-medium text-muted">默认画质</span>
                    <p class="mt-1 text-sm">
                      {{ view.upload.default_mode === 'document' ? 'document（原图）' : 'photo（省流量）' }}
                      <span v-if="view.upload.mode_source === 'env'" class="ml-1 text-xs text-muted">{{ ENV_MANAGED_HINT }}</span>
                    </p>
                    <p class="mt-1 text-xs text-muted">新任务默认使用该画质，单帖仍可在工作台覆盖；续传沿用原任务的画质。</p>
                  </div>
                </div>
              </div>
            </section>

            <section id="check" class="scroll-mt-36">
              <div class="flex items-center gap-2">
                <span class="tnum rounded border border-line bg-surface px-1.5 py-0.5 text-xs font-medium leading-none text-muted">03</span>
                <h2 class="text-base font-semibold tracking-tight">连通性自检</h2>
              </div>
              <p class="mt-1 text-xs text-muted">并行检测 Bot 与频道权限、Telegraph 账户，只读请求，不发送任何内容。</p>

              <div class="mt-3 rounded-md border border-line bg-surface">
                <div class="flex flex-wrap items-center justify-between gap-3 border-b border-line px-5 py-3.5">
                  <p class="text-xs text-muted">保存设置后再检测，读到的是最新上游配置。</p>
                  <button
                    type="button"
                    :disabled="checking"
                    class="h-11 shrink-0 rounded-md border border-line px-3 text-sm font-medium transition hover:border-ink/25 hover:bg-canvas disabled:opacity-60"
                    @click="check"
                  >
                    {{ checking ? '检测中…' : '开始检测' }}
                  </button>
                </div>
                <div class="px-5 py-4">
                  <p v-if="checkError !== ''" class="text-xs text-danger">{{ checkError }}</p>
                  <p v-else-if="result === null" class="text-xs text-muted">尚未检测。</p>
                  <template v-else>
                    <div class="space-y-2">
                      <div
                        v-for="item in [
                          { name: 'Telegram', target: result.telegram },
                          { name: 'Telegraph', target: result.telegraph },
                        ]"
                        :key="item.name"
                        class="flex flex-wrap items-center justify-between gap-2 rounded-md border border-line bg-canvas px-3 py-2.5"
                      >
                        <div class="min-w-0">
                          <p class="text-sm font-medium">{{ item.name }}</p>
                          <p class="mt-0.5 text-xs text-muted">{{ item.target.detail }}</p>
                        </div>
                        <div class="flex items-center gap-3 text-xs">
                          <span class="font-medium" :class="connectionTone(item.target.status)">
                            {{ connectionLabel(item.target.status) }}
                          </span>
                          <span class="text-muted tnum">{{ connectionLatency(item.target) }}</span>
                        </div>
                      </div>
                    </div>
                    <p class="mt-2 text-xs text-muted">{{ connectionSummary(result) }}</p>
                  </template>
                </div>
              </div>
            </section>

            <section id="ops" class="scroll-mt-36">
              <div class="flex items-center gap-2">
                <span class="tnum rounded border border-line bg-surface px-1.5 py-0.5 text-xs font-medium leading-none text-muted">04</span>
                <h2 class="text-base font-semibold tracking-tight">流量与备份</h2>
              </div>
              <p class="mt-1 text-xs text-muted">按自然月统计图片出站流量，生成一致性快照并可下载或发到频道。</p>

              <div class="mt-3 space-y-3">
                <div class="rounded-md border border-line bg-surface">
                  <div class="flex flex-wrap items-center justify-between gap-3 border-b border-line px-5 py-3.5">
                    <h3 class="text-sm font-semibold">流量看护</h3>
                    <button
                      type="button"
                      class="h-11 shrink-0 rounded-md border border-line px-3 text-sm font-medium transition hover:border-ink/25 hover:bg-canvas"
                      @click="loadTraffic"
                    >
                      刷新
                    </button>
                  </div>
                  <div class="px-5 py-4">
                    <p v-if="trafficError !== ''" class="text-xs text-danger">{{ trafficError }}</p>
                    <p v-else-if="traffic === null" class="text-xs text-muted">尚未读取。</p>
                    <template v-else>
                      <div class="grid grid-cols-1 gap-3 sm:grid-cols-3">
                        <div class="rounded-md border border-line bg-canvas px-3 py-2.5">
                          <p class="text-xs text-muted">本月出站</p>
                          <p class="mt-0.5 text-sm font-medium tnum">{{ formatTrafficBytes(traffic.bytes_out) }}</p>
                        </div>
                        <div class="rounded-md border border-line bg-canvas px-3 py-2.5">
                          <p class="text-xs text-muted">统计月份</p>
                          <p class="mt-0.5 text-sm font-medium tnum">{{ traffic.month }}</p>
                        </div>
                        <div class="rounded-md border border-line bg-canvas px-3 py-2.5">
                          <p class="text-xs text-muted">状态</p>
                          <p
                            class="mt-0.5 text-sm font-medium"
                            :class="trafficState(traffic) === 'ok' ? 'text-accent' : 'text-danger'"
                          >
                            {{ trafficStateText(traffic) }}
                          </p>
                        </div>
                      </div>
                      <p class="mt-2 text-xs text-muted">{{ trafficSummary(traffic) }}</p>
                    </template>
                  </div>
                </div>

                <div class="rounded-md border border-line bg-surface">
                  <div class="flex flex-wrap items-center justify-between gap-3 border-b border-line px-5 py-3.5">
                    <h3 class="text-sm font-semibold">自动备份</h3>
                    <div class="flex shrink-0 items-center gap-2">
                      <button
                        type="button"
                        :disabled="backing"
                        class="h-11 rounded-md border border-line px-3 text-sm font-medium transition hover:border-ink/25 hover:bg-canvas disabled:opacity-60"
                        @click="runBackup"
                      >
                        {{ backing ? '备份中…' : '立即备份' }}
                      </button>
                      <a
                        v-if="backup !== null && backup.latest !== null"
                        href="/api/backup/download"
                        class="inline-flex h-11 items-center rounded-md border border-line px-3 text-sm font-medium transition hover:border-ink/25 hover:bg-canvas"
                      >
                        下载最近备份
                      </a>
                    </div>
                  </div>
                  <div class="px-5 py-4">
                    <p v-if="backupError !== ''" class="text-xs text-danger">{{ backupError }}</p>
                    <p v-else-if="backupNote !== ''" class="text-xs text-accent">{{ backupNote }}</p>
                    <template v-if="backup !== null">
                      <p class="text-xs text-muted">{{ backupScheduleText(backup) }}</p>
                      <p class="mt-1 text-sm">{{ backupLatestText(backup) }}</p>
                    </template>
                    <p v-else class="text-xs text-muted">尚未读取备份状态。</p>
                  </div>
                </div>
              </div>
            </section>
          </div>

        </template>
        </div>
      </div>

      <div
        class="sticky bottom-0 z-20 -mx-5 mt-6 border-t border-line bg-surface px-5 py-3 lg:hidden"
      >
        <div class="flex items-center justify-between gap-3">
          <div class="min-w-0 flex-1 text-xs" aria-live="polite">
            <p v-if="saveError !== ''" class="text-danger">{{ saveError }}</p>
            <p v-else-if="feedback !== ''" class="text-accent">{{ feedback }}</p>
            <p v-else class="text-muted">已发布帖会保留原页面路径，修改作者名只影响后续发布。</p>
          </div>
          <button
            type="button"
            :disabled="saving"
            class="h-11 shrink-0 rounded-md bg-brand px-4 text-sm font-medium text-brand-ink transition hover:bg-brand-strong disabled:opacity-60"
            @click="save"
          >
            {{ saving ? '保存中…' : '保存设置' }}
          </button>
        </div>
      </div>
    </main>
  </div>
</template>
