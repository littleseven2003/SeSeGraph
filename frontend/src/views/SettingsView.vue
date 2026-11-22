<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ApiError } from '../api/client.ts'
import {
  fetchSettings,
  saveSettings,
  testConnection,
  SettingsError,
  type ConnectionResult,
  type SettingsView,
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
  saveFailureText,
  tokenPlaceholder,
} from '../utils/settings.ts'

const view = ref<SettingsView | null>(null)
const loading = ref(true)
const loadError = ref('')
const saving = ref(false)
const feedback = ref('')
const saveError = ref('')
const fieldErrors = ref<string[]>([])
const checking = ref(false)
const checkError = ref('')
const result = ref<ConnectionResult | null>(null)

const form = reactive({
  token: '',
  channelId: '',
  authorName: '',
  proxyMode: 'direct',
  proxyUrl: '',
  proxyTelegram: false,
  proxyTelegraph: false,
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
}

function managed(field: string): boolean {
  return view.value !== null && view.value.env_managed.includes(field)
}

async function load(): Promise<void> {
  loading.value = true
  loadError.value = ''
  try {
    applyView(await fetchSettings())
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
</script>

<template>
  <div class="min-h-full">
    <header class="border-b border-line bg-surface">
      <div class="mx-auto flex w-full max-w-[920px] items-center justify-between gap-4 px-4 py-3">
        <div class="flex items-center gap-3">
          <router-link
            :to="{ name: 'home' }"
            class="flex h-9 w-9 items-center justify-center rounded-md border border-line text-muted transition hover:bg-canvas hover:text-ink"
            aria-label="返回图集"
          >
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" class="h-4 w-4">
              <path d="M15 5l-7 7 7 7" stroke-linecap="round" stroke-linejoin="round" />
            </svg>
          </router-link>
          <div>
            <p class="text-xs font-medium tracking-wide text-muted">SeSeGraph</p>
            <p class="text-sm font-semibold">设置</p>
          </div>
        </div>
        <router-link
          :to="{ name: 'home' }"
          class="inline-flex h-9 items-center rounded-md border border-line px-3 text-sm font-medium transition hover:bg-canvas"
        >
          返回图集
        </router-link>
      </div>
    </header>

    <main class="mx-auto w-full max-w-[920px] px-4 py-7">
      <p v-if="loading" class="py-16 text-center text-sm text-muted">正在读取设置…</p>

      <div v-else-if="loadError !== ''" class="rounded-lg border border-line bg-surface px-4 py-10 text-center">
        <p class="text-sm text-danger">{{ loadError }}</p>
        <button
          type="button"
          class="mt-3 h-9 rounded-md border border-line px-3 text-sm font-medium transition hover:bg-canvas"
          @click="load"
        >
          重试
        </button>
      </div>

      <template v-else-if="view !== null">
        <section class="rounded-lg border border-line bg-surface">
          <div class="border-b border-line px-5 py-4">
            <h2 class="text-sm font-semibold">Telegram 图床</h2>
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
                  class="h-10 min-w-0 flex-1 rounded-md border border-line bg-canvas px-3 text-sm outline-none transition focus:border-brand focus:bg-surface disabled:opacity-60"
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
                class="mt-1 h-10 w-full rounded-md border border-line bg-canvas px-3 text-sm outline-none transition focus:border-brand focus:bg-surface disabled:opacity-60"
                :class="fieldErrors.includes(FIELD_TELEGRAM_CHANNEL) ? 'border-danger' : ''"
              />
              <p v-if="managed(FIELD_TELEGRAM_CHANNEL)" class="mt-1 text-xs text-muted">{{ ENV_MANAGED_HINT }}</p>
            </label>

            <div class="rounded-md border border-line bg-canvas px-3 py-2">
              <p class="text-xs text-muted">上游地址</p>
              <p class="mt-0.5 text-sm">{{ endpointText(view.telegram.api_base_source) }}</p>
            </div>
          </div>
        </section>

        <section class="mt-5 rounded-lg border border-line bg-surface">
          <div class="border-b border-line px-5 py-4">
            <h2 class="text-sm font-semibold">Telegraph 发布</h2>
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
                class="mt-1 h-10 w-full rounded-md border border-line bg-canvas px-3 text-sm outline-none transition focus:border-brand focus:bg-surface disabled:opacity-60"
                :class="fieldErrors.includes(FIELD_TELEGRAPH_AUTHOR) ? 'border-danger' : ''"
              />
              <p v-if="managed(FIELD_TELEGRAPH_AUTHOR)" class="mt-1 text-xs text-muted">{{ ENV_MANAGED_HINT }}</p>
            </label>

            <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
              <div class="rounded-md border border-line bg-canvas px-3 py-2">
                <p class="text-xs text-muted">访问令牌</p>
                <p class="mt-0.5 text-sm">{{ view.telegraph.token_set ? '已配置' : '未配置' }}</p>
              </div>
              <div class="rounded-md border border-line bg-canvas px-3 py-2">
                <p class="text-xs text-muted">上游地址</p>
                <p class="mt-0.5 text-sm">{{ endpointText(view.telegraph.api_base_source) }}</p>
              </div>
            </div>
          </div>
        </section>

        <section class="mt-5 rounded-lg border border-line bg-surface">
          <div class="border-b border-line px-5 py-4">
            <h2 class="text-sm font-semibold">网络代理</h2>
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
                  class="flex cursor-pointer items-center gap-2 rounded-md border border-line bg-canvas px-3 py-2 text-sm transition"
                  :class="form.proxyMode === option.value ? 'border-brand bg-surface' : ''"
                >
                  <input
                    v-model="form.proxyMode"
                    type="radio"
                    name="proxy-mode"
                    :value="option.value"
                    :disabled="saving || managed(FIELD_PROXY_MODE)"
                    class="h-4 w-4 accent-brand"
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
                  class="h-10 min-w-0 flex-1 rounded-md border border-line bg-canvas px-3 text-sm outline-none transition focus:border-brand focus:bg-surface disabled:opacity-60"
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
              <p class="mt-1 text-xs text-muted">
                <template v-if="managed(FIELD_PROXY_URL)">{{ ENV_MANAGED_HINT }}</template>
                <template v-else>
                  {{ proxyURLText(view.proxy.url_set, view.proxy.url_source) }}；切回直连后才能清除已保存的地址。
                </template>
              </p>
            </label>

            <div class="grid grid-cols-1 gap-2 sm:grid-cols-2">
              <label class="flex items-center gap-2 rounded-md border border-line bg-canvas px-3 py-2 text-sm">
                <input
                  v-model="form.proxyTelegram"
                  type="checkbox"
                  :disabled="saving || form.proxyMode === 'direct' || managed(FIELD_PROXY_TELEGRAM)"
                  class="h-4 w-4 accent-brand"
                />
                <span>Telegram Bot API 走代理</span>
              </label>
              <label class="flex items-center gap-2 rounded-md border border-line bg-canvas px-3 py-2 text-sm">
                <input
                  v-model="form.proxyTelegraph"
                  type="checkbox"
                  :disabled="saving || form.proxyMode === 'direct' || managed(FIELD_PROXY_TELEGRAPH)"
                  class="h-4 w-4 accent-brand"
                />
                <span>Telegraph API 走代理</span>
              </label>
            </div>
            <p class="text-xs text-muted">
              直连模式忽略上面的开关；跟随系统模式读取进程环境变量 HTTP_PROXY / HTTPS_PROXY / ALL_PROXY。
            </p>
          </div>
        </section>

        <section class="mt-5 rounded-lg border border-line bg-surface">
          <div class="flex flex-wrap items-center justify-between gap-3 border-b border-line px-5 py-4">
            <div>
              <h2 class="text-sm font-semibold">连通性自检</h2>
              <p class="mt-1 text-xs text-muted">并行检测 Bot 与频道权限、Telegraph 账户，只读请求，不发送任何内容。</p>
            </div>
            <button
              type="button"
              :disabled="checking"
              class="h-9 shrink-0 rounded-md border border-line px-3 text-sm font-medium transition hover:bg-canvas disabled:opacity-60"
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
                  class="flex flex-wrap items-center justify-between gap-2 rounded-md border border-line bg-canvas px-3 py-2"
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
        </section>

        <div class="mt-5 flex flex-wrap items-center justify-between gap-3 rounded-lg border border-line bg-surface px-5 py-4">
          <div class="min-w-0 text-xs">
            <p v-if="saveError !== ''" class="text-danger">{{ saveError }}</p>
            <p v-else-if="feedback !== ''" class="text-accent">{{ feedback }}</p>
            <p v-else class="text-muted">已发布帖会保留原页面路径，修改作者名只影响后续发布。</p>
          </div>
          <button
            type="button"
            :disabled="saving"
            class="h-10 shrink-0 rounded-md bg-brand px-4 text-sm font-medium text-brand-ink transition hover:bg-brand-strong disabled:opacity-60"
            @click="save"
          >
            {{ saving ? '保存中…' : '保存设置' }}
          </button>
        </div>
      </template>
    </main>
  </div>
</template>
