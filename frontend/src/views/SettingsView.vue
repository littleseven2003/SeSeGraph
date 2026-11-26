<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { setThemeMode, themeMode } from '../utils/theme.ts'
import { ApiError } from '../api/client.ts'
import {
  clearMediaThumbs,
  createTelegraphAccount,
  fetchBackupStatus,
  fetchBots,
  fetchSettings,
  fetchTraffic,
  saveBots,
  resetPostThumbs,
  saveSettings,
  testConnection,
  triggerBackup,
  updateAdminCredentials,
  SettingsError,
  type BackupStatus,
  type BotEntry,
  type ConnectionResult,
  type SettingsPatch,
  type SettingsView,
  type TrafficStats,
} from '../api/settings.ts'
import AppHeader from '../components/AppHeader.vue'
import BackToTop from '../components/BackToTop.vue'
import {
  UPLOAD_CONCURRENCY_MAX,
  UPLOAD_CONCURRENCY_MIN,
  clampUploadConcurrency,
  readUploadConcurrency,
  saveUploadConcurrency,
  serverUploadConcurrency,
} from '../utils/upload-pref.ts'
import { fetchGallery, fetchPostDetail, type GalleryItem } from '../api/gallery.ts'
import { fetchMediaBlob, uploadThumb } from '../api/posts.ts'
import { renderThumb, THUMB_MAX_PIXELS } from '../utils/thumbs.ts'
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
  FIELD_TELEGRAPH_PATH_MODE,
  FIELD_TELEGRAPH_TOKEN,
  FIELD_UPLOAD_MODE,
  FIELD_TRAFFIC_ALERT,
  FIELD_TRAFFIC_LIMIT,
  FIELD_BACKUP_ENABLED,
  FIELD_BACKUP_TIME,
  FIELD_BACKUP_KEEP,
  FIELD_MEDIA_THUMBS_LIMIT,
  backupTimeInvalid,
  bytesToGB,
  gbToBytes,
  credentialErrors,
  credentialFieldError,
  hasCredentialErrors,
  hasPatch,
  PATH_MODES,
  UPLOAD_MODES,
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
  telegraphAccountDisabled,
  telegraphAccountHint,
  telegraphAccountState,
  type CredentialErrors,
  type CredentialField,
  type CredentialForm,
  formatTrafficBytes,
  trafficState,
  trafficStateText,
  trafficSummary,
} from '../utils/settings.ts'

// 主题三态：跟随系统、浅色、深色；只记本机，不动媒体遮罩偏好。
const THEME_OPTIONS = [
  { value: 'system' as const, label: '跟随系统' },
  { value: 'light' as const, label: '浅色' },
  { value: 'dark' as const, label: '深色' },
]

const view = ref<SettingsView | null>(null)
// uploadCap 是服务端同时接收上传的上限，仅用于说明；派发时按 min(本地偏好, 上限) 执行。
const uploadCap = computed(() => serverUploadConcurrency(view.value?.upload.concurrency))
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
const telegraphSubmitting = ref(false)
const telegraphError = ref('')
const telegraphNote = ref('')
const credentialForm = reactive<CredentialForm>({
  currentPassword: '',
  username: '',
  newPassword: '',
  confirmPassword: '',
})
const credentialFieldErrors = ref<CredentialErrors>({})
const credentialSubmitting = ref(false)
// 逐图缩略图缓存的在线管理：清理与按图集重建都在浏览器内单路执行。
const thumbBusy = ref(false)
const thumbError = ref('')
const thumbNote = ref('')
const rebuildPosts = ref<GalleryItem[]>([])
const rebuildPostId = ref<number | null>(null)
const rebuildRunning = ref(false)
const rebuildCancel = ref(false)
// rebuildAbort 取消正在读取的原媒体，界面不必等当前请求自然结束。
let rebuildAbort: AbortController | null = null
const rebuildNote = ref('')
const rebuildError = ref('')
const rebuildProgress = reactive({ done: 0, total: 0, failed: 0 })
const credentialError = ref('')
const credentialNote = ref('')
const router = useRouter()
let reloginTimer = 0
const result = ref<ConnectionResult | null>(null)
// activeSection 记录当前展开的分组；四组一次只显示一组，避免长滚屏。
const activeSection = ref('channel')

// 分组导航：桌面左侧目录与手机顶部横向目录共用同一份分组。
const SETTING_NAV = [
  { id: 'telegram', label: 'Telegram 图床', group: '上游连接' },
  { id: 'network', label: '代理与自检' },
  { id: 'telegraph', label: 'Telegraph 发布', group: '内容与运维' },
  { id: 'upload', label: '上传默认值' },
  { id: 'traffic', label: '流量看护' },
  { id: 'maintenance', label: '备份与缓存' },
  { id: 'appearance', label: '外观与安全', group: '系统与偏好' },
]

// LEGACY_ANCHORS 让旧地址里的分区锚点安静落到新分组，不报错也不丢定位。
const LEGACY_ANCHORS: Record<string, string> = {
  channel: 'telegram',
  upstream: 'telegram',
  check: 'network',
  media: 'upload',
  defaults: 'upload',
  storage: 'traffic',
  ops: 'traffic',
  backup: 'maintenance',
  cache: 'maintenance',
  account: 'appearance',
  theme: 'appearance',
}

// fieldErrorText 给字段就近的报错文案，避免只在底部提示。
function fieldErrorText(field: string): string {
  if (!fieldErrors.value.includes(field)) return ''
  if (field === FIELD_TELEGRAM_CHANNEL) return '需为 @用户名 或数字编号'
  if (field === FIELD_TELEGRAPH_AUTHOR) return '作者名不超过 100 字'
  if (field === FIELD_PROXY_URL) return '需为 http://、https:// 或 socks5:// 地址，自定义模式必须填写'
  if (field === FIELD_TRAFFIC_ALERT || field === FIELD_TRAFFIC_LIMIT) return '请填写不小于 0 的 GB 数值，留空表示关闭'
  if (field === FIELD_BACKUP_TIME) return '时刻格式为 HH:MM，例如 03:00'
  if (field === FIELD_BACKUP_KEEP) return '保留份数为 1–60 的整数'
  return '该项填写有误'
}

const form = reactive({
  alertGB: '',
  limitGB: '',
  backupEnabled: false,
  backupTime: '03:00',
  backupKeep: '7',
  thumbGB: '2',
  token: '',
  channelId: '',
  authorName: '',
  pathMode: 'title',
  defaultMode: 'photo',
  uploadConcurrency: String(readUploadConcurrency(typeof window === 'undefined' ? undefined : window.localStorage)),
  proxyMode: 'direct',
  proxyUrl: '',
  proxyTelegram: false,
  proxyTelegraph: false,
  metaFilenames: '',
  metaParseMode: 'first-line',
})

// Bot 池：草稿与服务端条目分开保存，保存成功后整体刷新。
const bots = ref<BotEntry[]>([])
const botDraft = ref<{ bot_id: number; alias: string; token: string; status: string }[]>([])
const botSaving = ref(false)
const botError = ref('')
const botNotice = ref('')

function applyBots(list: BotEntry[]): void {
  bots.value = list
  botDraft.value = list.map((bot) => ({ bot_id: bot.bot_id, alias: bot.alias, token: '', status: bot.status }))
}

function maskedTokenOf(botID: number): string {
  const found = bots.value.find((bot) => bot.bot_id === botID)
  return found !== undefined && found.masked_token !== '' ? found.masked_token : 'Bot Token'
}

function botStatusClass(botID: number): string {
  const found = bots.value.find((bot) => bot.bot_id === botID)
  if (found === undefined) return ''
  return found.status === 'frozen' || found.status === 'paused' ? 'text-danger' : ''
}

function statusTextOf(botID: number): string {
  const found = bots.value.find((bot) => bot.bot_id === botID)
  if (found === undefined) return ''
  if (found.status === 'frozen') {
    return found.status_note !== '' ? `已冻结：${found.status_note}` : '已冻结'
  }
  if (found.status === 'paused') return '已停用（不参与新写入）'
  return '可写入'
}

async function loadBots(): Promise<void> {
  try {
    applyBots((await fetchBots()).bots)
  } catch {
    // 池接口未启用时保持空列表，单 Bot 配置照常可用。
    applyBots([])
  }
}

function addBotDraft(): void {
  botNotice.value = ''
  botError.value = ''
  if (botDraft.value.length >= 10) {
    botError.value = '最多配置 10 个 Bot'
    return
  }
  botDraft.value.push({ bot_id: 0, alias: '', token: '', status: 'ok' })
}

function removeBotDraft(index: number): void {
  botNotice.value = ''
  botError.value = ''
  botDraft.value.splice(index, 1)
}

// toggleBotDraft 在草稿里切换停用／启用；停用只退出新写入，已有图片仍可读。
function toggleBotDraft(index: number): void {
  botNotice.value = ''
  botError.value = ''
  const bot = botDraft.value[index]
  bot.status = bot.status === 'paused' ? 'ok' : 'paused'
}

function moveBotDraft(index: number, offset: number): void {
  const target = index + offset
  if (target < 0 || target >= botDraft.value.length) return
  const list = botDraft.value
  ;[list[index], list[target]] = [list[target], list[index]]
}

async function saveBotPool(): Promise<void> {
  if (botSaving.value) return
  botSaving.value = true
  botError.value = ''
  botNotice.value = ''
  try {
    const saved = await saveBots(botDraft.value.map((bot, index) => ({
      bot_id: bot.bot_id,
      alias: bot.alias.trim(),
      token: bot.token.trim(),
      priority: index,
      status: bot.status,
    })))
    applyBots(saved.bots)
    botNotice.value = saved.pool_enabled
      ? '已保存，上传遇到账号受限时会自动换用池内其他 Bot'
      : '已保存；池内至少两个可用 Bot 时才启用自动切换'
  } catch (cause) {
    botError.value = cause instanceof ApiError ? cause.message : '保存 Bot 池失败，请稍后重试'
  } finally {
    botSaving.value = false
  }
}

function applyView(next: SettingsView): void {
  view.value = next
  form.channelId = next.telegram.channel_id
  form.authorName = next.telegraph.author_name
  form.pathMode = next.telegraph.path_mode
  form.defaultMode = next.upload.default_mode
  form.alertGB = bytesToGB(next.traffic.alert_bytes)
  form.limitGB = bytesToGB(next.traffic.limit_bytes)
  form.backupEnabled = next.backup.enabled
  form.backupTime = next.backup.time
  form.backupKeep = String(next.backup.keep)
  form.thumbGB = String(Math.round((next.media_thumbs.limit_bytes / (1 << 30)) * 10) / 10)
  form.token = ''
  form.proxyMode = next.proxy.mode
  form.proxyUrl = ''
  form.proxyTelegram = next.proxy.telegram_enabled
  form.proxyTelegraph = next.proxy.telegraph_enabled
  form.metaFilenames = next.meta.filenames.join(', ')
  form.metaParseMode = next.meta.parse_mode
}

// applyUploadConcurrency 保存本地偏好，属于本机性能选项，不进服务端配置。
function applyUploadConcurrency(): void {
  form.uploadConcurrency = String(
    saveUploadConcurrency(typeof window === 'undefined' ? undefined : window.localStorage, clampUploadConcurrency(form.uploadConcurrency)),
  )
}

function managed(field: string): boolean {
  return view.value !== null && view.value.env_managed.includes(field)
}

function telegraphState(): ReturnType<typeof telegraphAccountState> {
  return view.value === null ? 'ready' : telegraphAccountState(view.value)
}

async function requestTelegraphAccount(): Promise<void> {
  if (view.value === null || telegraphSubmitting.value || telegraphAccountDisabled(telegraphState())) return
  telegraphSubmitting.value = true
  telegraphError.value = ''
  telegraphNote.value = ''
  try {
    const result = await createTelegraphAccount(form.authorName)
    if (view.value !== null) {
      view.value = {
        ...view.value,
        telegraph: {
          ...view.value.telegraph,
          token_set: result.configured,
          token_source: result.source === '' ? 'stored' : result.source,
          author_name: result.author_name === '' ? view.value.telegraph.author_name : result.author_name,
        },
      }
    }
    telegraphNote.value = result.configured ? '已申请匿名账户，访问令牌保存在服务端' : '账户已存在，沿用现有配置'
  } catch (cause) {
    telegraphError.value = cause instanceof SettingsError ? cause.message : '申请账户失败，请稍后重试'
  } finally {
    telegraphSubmitting.value = false
  }
}

function credentialErrorText(field: CredentialField): string {
  return credentialFieldErrors.value[field] ?? ''
}

async function submitCredentials(): Promise<void> {
  if (credentialSubmitting.value) return
  credentialError.value = ''
  credentialNote.value = ''
  const errors = credentialErrors(credentialForm)
  credentialFieldErrors.value = errors
  if (hasCredentialErrors(errors)) {
    credentialError.value = '请检查标红的字段'
    return
  }
  credentialSubmitting.value = true
  try {
    await updateAdminCredentials({
      current_password: credentialForm.currentPassword,
      username: credentialForm.username.trim(),
      new_password: credentialForm.newPassword,
      confirm_password: credentialForm.confirmPassword,
    })
    credentialForm.currentPassword = ''
    credentialForm.username = ''
    credentialForm.newPassword = ''
    credentialForm.confirmPassword = ''
    credentialNote.value = '账号已更新，请使用新凭据重新登录'
    reloginTimer = window.setTimeout(() => {
      void router.push({ name: 'login' })
    }, 1200)
  } catch (cause) {
    if (cause instanceof SettingsError) {
      credentialError.value = cause.message
      const mapped: CredentialErrors = {}
      for (const field of cause.fields) {
        const key = credentialFieldError(field)
        if (key !== null) mapped[key] = cause.message
      }
      credentialFieldErrors.value = mapped
    } else {
      credentialError.value = '修改账号失败，请稍后重试'
    }
  } finally {
    credentialSubmitting.value = false
  }
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

// refreshThumbUsage 只刷新占用与条目数，避免冲掉表单里还没保存的改动。
async function refreshThumbUsage(): Promise<void> {
  if (view.value === null) return
  try {
    const next = await fetchSettings()
    if (view.value !== null) view.value = { ...view.value, media_thumbs: next.media_thumbs }
  } catch {
    // 占用只是展示，读取失败不打断其他操作
  }
}

// loadRebuildPosts 读取可重建的图集列表，供按图集重建选择。
async function loadRebuildPosts(): Promise<void> {
  try {
    const page = await fetchGallery({ q: '', tags: [], page: 1, pageSize: 100 })
    rebuildPosts.value = page.items.filter((item) => item.image_count > 0)
  } catch {
    // 列表只是辅助，失败时保留原样
  }
}

// clearThumbs 清空全部逐图小图；原图不受影响，下次打开会重新生成。
async function clearThumbs(): Promise<void> {
  if (thumbBusy.value) return
  if (!window.confirm('确定清空全部逐图缩略图缓存？已发布的原图不受影响，下次打开时会重新生成。')) return
  thumbBusy.value = true
  thumbError.value = ''
  thumbNote.value = ''
  try {
    const result = await clearMediaThumbs()
    thumbNote.value = '已清理 ' + String(result.removed) + ' 项，释放 ' + formatTrafficBytes(result.freed_bytes) + '。'
    await refreshThumbUsage()
  } catch (cause) {
    thumbError.value = cause instanceof Error ? cause.message : '清理失败，请稍后重试'
  } finally {
    thumbBusy.value = false
  }
}

// startRebuild 先清该帖小图并换新内容版本，再在浏览器内串行回源生成并回存，
// 与抽屉共用同一份 512px 规格，迟到写入会因版本不符被服务端拒绝。
async function startRebuild(): Promise<void> {
  if (rebuildRunning.value || rebuildPostId.value === null) return
  const postId = rebuildPostId.value
  rebuildRunning.value = true
  rebuildCancel.value = false
  rebuildError.value = ''
  rebuildNote.value = ''
  rebuildProgress.done = 0
  rebuildProgress.total = 0
  rebuildProgress.failed = 0
  rebuildAbort = new AbortController()
  const signal = rebuildAbort.signal
  try {
    await resetPostThumbs(postId)
    const detail = await fetchPostDetail(postId)
    const targets = detail.images.filter((image) => image.status === 'stored')
    rebuildProgress.total = targets.length
    for (const image of targets) {
      if (signal.aborted) break
      try {
        const source = await fetchMediaBlob(postId, image.sort_order, signal)
        const thumb = await renderThumb(source, THUMB_MAX_PIXELS, signal)
        if (signal.aborted) break
        await uploadThumb(postId, image.sort_order, image.media_version, thumb, signal)
      } catch {
        // 取消导致的中断不算失败项。
        if (!signal.aborted) rebuildProgress.failed += 1
      }
      if (!signal.aborted) rebuildProgress.done += 1
    }
    const okCount = rebuildProgress.done - rebuildProgress.failed
    rebuildNote.value = signal.aborted
      ? '已取消重建，已完成的项保留小图。'
      : '重建完成：' + String(okCount) + '/' + String(rebuildProgress.total) + ' 项成功。'
    await refreshThumbUsage()
  } catch (cause) {
    if (!signal.aborted) rebuildError.value = cause instanceof Error ? cause.message : '重建失败，请稍后重试'
  } finally {
    rebuildAbort = null
    rebuildRunning.value = false
  }
}

// cancelRebuild 立即中止正在读取的原媒体，并保留已完成项。
function cancelRebuild(): void {
  rebuildCancel.value = true
  rebuildAbort?.abort()
}

async function load(): Promise<void> {
  loading.value = true
  loadError.value = ''
  try {
    applyView(await fetchSettings())
    await loadBots()
    await loadTraffic()
    await loadBackup()
    await loadRebuildPosts()
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


// restorePendingDraft 把本次没有提交的组还原成保存前的输入，保留未保存草稿。
function restorePendingDraft(before: typeof form, patch: SettingsPatch): void {
  if (patch.telegram === undefined) form.channelId = before.channelId
  if (patch.telegraph === undefined) {
    form.authorName = before.authorName
    form.pathMode = before.pathMode
  }
  if (patch.proxy === undefined) {
    form.proxyMode = before.proxyMode
    form.proxyUrl = before.proxyUrl
    form.proxyTelegram = before.proxyTelegram
    form.proxyTelegraph = before.proxyTelegraph
  }
  if (patch.upload === undefined) form.defaultMode = before.defaultMode
  if (patch.traffic === undefined) {
    form.alertGB = before.alertGB
    form.limitGB = before.limitGB
  }
  if (patch.backup === undefined) {
    form.backupEnabled = before.backupEnabled
    form.backupTime = before.backupTime
    form.backupKeep = before.backupKeep
  }
  if (patch.media_thumbs === undefined) form.thumbGB = before.thumbGB
  if (patch.meta === undefined) {
    form.metaFilenames = before.metaFilenames
    form.metaParseMode = before.metaParseMode
  }
}


// botDraftChanged 判断 Bot 列表是否有未保存改动（别名、令牌、顺序、启停）。
function botDraftChanged(): boolean {
  if (botDraft.value.length !== bots.value.length) return true
  return botDraft.value.some((bot, index) => {
    const saved = bots.value[index]
    return saved === undefined || bot.bot_id !== saved.bot_id || bot.alias !== saved.alias
      || bot.token !== '' || bot.status !== saved.status
  })
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
  if (patch.traffic !== undefined && (gbToBytes(form.alertGB) === null || gbToBytes(form.limitGB) === null)) {
    saveError.value = '流量阈值需为不小于 0 的 GB 数值'
    fieldErrors.value = [FIELD_TRAFFIC_ALERT, FIELD_TRAFFIC_LIMIT]
    return
  }
  if (patch.backup !== undefined && backupTimeInvalid(form.backupTime)) {
    saveError.value = '备份时刻格式为 HH:MM'
    fieldErrors.value = [FIELD_BACKUP_TIME]
    return
  }
  const keep = Number(form.backupKeep)
  if (patch.backup !== undefined && (!Number.isInteger(keep) || keep < 1 || keep > 60)) {
    saveError.value = '备份保留份数为 1–60 的整数'
    fieldErrors.value = [FIELD_BACKUP_KEEP]
    return
  }
  if (patch.proxy !== undefined && proxyCustomNeedsURL(form.proxyMode, view.value.proxy.url_set, form.proxyUrl)) {
    saveError.value = '自定义代理需要填写代理地址'
    fieldErrors.value = [FIELD_PROXY_URL]
    return
  }

  saving.value = true
  // 记下保存前的输入：本次没提交的组要把草稿还原回去，避免被服务端结果覆盖。
  const pending = { ...form }
  try {
    // Telegram 页把 Bot 列表和频道一起提交：列表失败就不保存频道，避免半套配置。
    const savingBotsInTelegram = activeSection.value === 'telegram' && botDraftChanged()
    if (savingBotsInTelegram) {
      await saveBotPool()
      if (botError.value !== '') return
    }
    applyView(await saveSettings(patch))
    restorePendingDraft(pending, patch)
    feedback.value = '设置已保存并立即生效'
    void loadTraffic()
    void loadBackup()
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

// hashSection 取出地址里的有效分组锚点；刷新直达时浏览器早于分组渲染尝试定位，需自己补一次。
function hashSection(): string {
  const raw = window.location.hash.replace(/^#/, '')
  if (SETTING_NAV.some((item) => item.id === raw)) return raw
  return LEGACY_ANCHORS[raw] ?? ''
}

// pendingJump 保护主动跳转的选中项：桌面端尾部分组滚不到顶部时，
// 位置判定会把选中项让给上一组，这里保留到用户自己滚动为止。
let pendingJump = ''
let pendingTimer = 0

function jumpToSection(id: string): void {
  if (!SETTING_NAV.some((item) => item.id === id)) return
  activeSection.value = id
}

// followHash 在分组渲染后补一次定位，刷新、前进后退和直接打开都能落到同一分组。
// CARD_ANCHORS 指向同一栏内的具体卡片，切到对应菜单后再滚到卡片位置。
const CARD_ANCHORS: Record<string, string> = {
  backup: 'backup',
  cache: 'cache',
  theme: 'theme',
  security: 'account',
}

function followHash(): void {
  const raw = window.location.hash.replace(/^#/, '')
  const id = hashSection()
  if (id === '') return
  jumpToSection(id)
  const card = CARD_ANCHORS[raw]
  if (card === undefined) return
  void nextTick(() => {
    document.getElementById(card)?.scrollIntoView({ block: 'start' })
  })
}

// syncActiveFromScroll 以顶栏下沿为准判定当前分组；滚到底部时直接取最后一组，
// 尾部分组比视口高时观察器算不出比例，按位置判定不会再漏。
function syncActiveFromScroll(): void {
  // 单组显示后不再按滚动位置改分组，翻到下一组只能手动点选。
  if (pendingJump !== '') {
    activeSection.value = pendingJump
    return
  }
}

onBeforeUnmount(() => {
  rebuildAbort?.abort()
})

onMounted(() => {
  window.addEventListener('scroll', syncActiveFromScroll, { passive: true })
  window.addEventListener('hashchange', followHash)
  void load()
})

// 设置读完、分组真正渲染出来之后才读地址：applyView 早于 loading 结束，此时节点还不存在。
watch(loading, async (busy) => {
  if (busy || view.value === null) return
  await nextTick()
  followHash()
  syncActiveFromScroll()
})

// selectSection 让点按立即更新色点，不依赖滚动的下一帧。
function selectSection(id: string): void {
  activeSection.value = id
}

onBeforeUnmount(() => {
  window.clearTimeout(reloginTimer)
  window.clearTimeout(pendingTimer)
  pendingJump = ''
  window.removeEventListener('scroll', syncActiveFromScroll)
  window.removeEventListener('hashchange', followHash)
})
</script>

<template>
  <div class="min-h-full">
    <AppHeader />

    <main class="mx-auto w-full max-w-[1120px] px-5 py-7">
      <div class="mb-5">
        <h1 class="text-2xl font-semibold tracking-tight sm:text-3xl">设置</h1>
        <p class="mt-1 text-sm text-muted">图床、发布默认值与存储备份都在这里调整，保存后立即生效。</p>
      </div>
      <nav class="mb-4 flex gap-2 overflow-x-auto overscroll-x-contain py-1 lg:hidden" aria-label="设置分组">
        <a
          v-for="item in SETTING_NAV"
          :key="'narrow-' + item.id"
          :href="'#' + item.id"
          class="inline-flex h-11 shrink-0 items-center -my-1"
          @click="selectSection(item.id)"
        >
          <span
            class="inline-flex h-9 items-center rounded-lg border px-3 text-xs font-medium transition"
            :class="activeSection === item.id ? 'border-brand-soft bg-brand-soft text-ink' : 'border-line bg-surface text-muted hover:bg-canvas hover:text-ink'"
          >
            {{ item.label }}
          </span>
        </a>
      </nav>
      <div class="flex items-start gap-8">
      <nav class="sticky top-24 hidden w-[184px] shrink-0 lg:block" aria-label="设置分组">
        <ul class="space-y-0.5">
          <li v-for="item in SETTING_NAV" :key="item.id">
            <p v-if="item.group" class="px-3 pb-1 pt-3 text-[11px] font-medium text-muted/70 first:pt-0">{{ item.group }}</p>
            <a
              :href="'#' + item.id"
              class="flex items-center gap-2 whitespace-nowrap rounded-lg px-3 py-2 text-sm transition hover:bg-brand-soft/70 hover:text-ink"
              @click="selectSection(item.id)"
              :class="activeSection === item.id ? 'bg-brand-soft text-ink' : 'text-muted'"
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
            class="mt-3 h-11 w-full rounded-xl bg-brand px-4 text-sm font-medium text-brand-ink transition hover:bg-brand-strong disabled:opacity-60"
            @click="save"
          >
            {{ saving ? '保存中…' : '保存设置' }}
          </button>
        </div>
      </nav>

      <div class="min-w-0 max-w-[880px] flex-1">
        <p v-if="loading" class="py-16 text-center text-sm text-muted">正在读取设置…</p>

        <div v-else-if="loadError !== ''" class="rounded-2xl bg-surface px-4 py-10 text-center shadow-[0_1px_0_0_rgba(27,26,23,0.05)]">
          <p class="text-sm text-danger">{{ loadError }}</p>
          <button
            type="button"
            class="mt-3 h-11 rounded-xl border border-line px-3.5 text-sm font-medium text-muted transition hover:bg-canvas hover:text-ink"
            @click="load"
          >
            重试
          </button>
        </div>

        <template v-else-if="view !== null">
          <div class="space-y-8">
            <div v-show="activeSection === 'telegram' || activeSection === 'network'" class="space-y-8">
            <section id="upstream" class="scroll-mt-36">
              <div class="flex items-center gap-2">
                <h2 v-show="activeSection === 'telegram'" class="text-base font-semibold tracking-tight">Telegram 图床</h2>
                <h2 v-show="activeSection === 'network'" class="text-base font-semibold tracking-tight">代理与自检</h2>
              </div>
              <p v-show="activeSection === 'telegram'" class="mt-1 text-xs text-muted">Bot 凭据与频道地址，保存后上传队列与图片网关立即使用新配置。</p>
              <p v-show="activeSection === 'network'" class="mt-1 text-xs text-muted">代理走向与上游连通性检测，保存后立即重建传输层。</p>

              <div class="mt-3 space-y-3">
                <div v-show="activeSection === 'telegram'" class="rounded-2xl bg-surface shadow-[0_1px_0_0_rgba(27,26,23,0.05)]">
                  <div class="border-b border-line/70 px-5 py-4">
                    <h3 class="text-sm font-semibold">Telegram 图床</h3>
                    <p class="mt-1 text-xs text-muted">Bot 凭据统一在下方「Bot 列表」维护；这里只保留频道与上游地址。</p>
                  </div>
                  <div class="space-y-4 px-5 py-4">
                    <label class="block">
                      <span class="text-xs font-medium text-muted">频道标识</span>
                      <input
                        v-model="form.channelId"
                        type="text"
                        autocomplete="off"
                        :disabled="saving || managed(FIELD_TELEGRAM_CHANNEL)"
                        placeholder="@channel 或 -1001234567890"
                        class="mt-1 h-11 w-full rounded-xl border border-line bg-canvas px-3 text-sm outline-none transition focus:border-ink/40 focus:bg-surface disabled:opacity-60"
                        :class="fieldErrors.includes(FIELD_TELEGRAM_CHANNEL) ? 'border-danger' : ''"
                      />
                      <p v-if="fieldErrorText(FIELD_TELEGRAM_CHANNEL) !== ''" class="mt-1 text-xs text-danger">
                        {{ fieldErrorText(FIELD_TELEGRAM_CHANNEL) }}
                      </p>
                      <p v-else-if="managed(FIELD_TELEGRAM_CHANNEL)" class="mt-1 text-xs text-muted">
                        {{ ENV_MANAGED_HINT }}
                      </p>
                    </label>

                    <div class="rounded-xl bg-canvas px-3 py-2.5">
                      <p class="text-xs text-muted">上游地址</p>
                      <p class="mt-0.5 text-sm">{{ endpointText(view.telegram.api_base_source) }}</p>
                    </div>
                  </div>
                </div>

                <div v-show="activeSection === 'telegram'" class="rounded-2xl bg-surface shadow-[0_1px_0_0_rgba(27,26,23,0.05)]">
                  <div class="border-b border-line/70 px-5 py-4">
                    <div class="flex flex-wrap items-center justify-between gap-2">
                      <div>
                        <h3 class="text-sm font-semibold">Bot 列表</h3>
                        <p class="mt-1 text-xs text-muted">按顺序尝试，遇到账号受限自动换下一个；令牌只以掩码显示，留空表示沿用原值。</p>
                      </div>
                      <button
                        type="button"
                        :disabled="botSaving"
                        class="h-9 rounded-lg border border-line px-3 text-xs font-medium text-muted transition hover:bg-canvas disabled:opacity-60"
                        @click="addBotDraft"
                      >
                        添加 Bot
                      </button>
                    </div>
                  </div>
                  <div class="space-y-3 px-5 py-4">
                    <div v-for="(bot, index) in botDraft" :key="index" class="rounded-xl bg-canvas px-3 py-2.5">
                      <div class="flex flex-col gap-2 sm:flex-row sm:items-center">
                        <input
                          v-model="bot.alias"
                          type="text"
                          autocomplete="off"
                          placeholder="别名，如 主力"
                          class="h-9 w-full rounded-lg border border-line bg-surface px-2 text-xs outline-none transition focus:border-ink/40 sm:w-32"
                        />
                        <input
                          v-model="bot.token"
                          type="password"
                          autocomplete="off"
                          :placeholder="bot.bot_id > 0 ? maskedTokenOf(bot.bot_id) : '粘贴新的 Bot Token'"
                          class="h-9 min-w-0 flex-1 rounded-lg border border-line bg-surface px-2 text-xs outline-none transition focus:border-ink/40"
                        />
                      </div>
                      <div class="mt-2 flex flex-wrap items-center gap-2 text-xs text-muted">
                        <span class="tnum">{{ index + 1 }}</span>
                        <span v-if="bot.bot_id > 0" class="tnum">编号 {{ bot.bot_id }}</span>
                        <span v-if="bot.bot_id > 0" :class="botStatusClass(bot.bot_id)">{{ statusTextOf(bot.bot_id) }}</span>
                        <span v-else>保存后按令牌识别</span>
                        <span class="ml-auto flex gap-1">
                          <button
                            type="button"
                            :disabled="index === 0 || botSaving"
                            class="h-8 rounded-lg border border-line px-2 transition hover:bg-surface disabled:opacity-40"
                            @click="moveBotDraft(index, -1)"
                          >
                            上移
                          </button>
                          <button
                            type="button"
                            :disabled="index === botDraft.length - 1 || botSaving"
                            class="h-8 rounded-lg border border-line px-2 transition hover:bg-surface disabled:opacity-40"
                            @click="moveBotDraft(index, 1)"
                          >
                            下移
                          </button>
                          <button
                            type="button"
                            :disabled="botSaving"
                            class="h-8 rounded-lg border border-line px-2 transition hover:bg-surface disabled:opacity-40"
                            @click="toggleBotDraft(index)"
                          >
                            {{ bot.status === 'paused' ? '启用' : '停用' }}
                          </button>
                          <button
                            type="button"
                            :disabled="botSaving"
                            class="h-8 rounded-lg border border-line px-2 text-danger transition hover:bg-surface disabled:opacity-40"
                            @click="removeBotDraft(index)"
                          >
                            移除
                          </button>
                        </span>
                      </div>
                    </div>
                    <p v-if="botDraft.length === 0" class="text-xs text-muted">还没有配置额外 Bot，单 Bot 配置照常可用。</p>
                    <div class="flex flex-wrap items-center gap-2">
                      <button
                        type="button"
                        :disabled="botSaving"
                        class="h-9 rounded-lg border border-line px-3 text-xs font-medium text-muted transition hover:bg-canvas disabled:opacity-60"
                        @click="addBotDraft"
                      >
                        添加 Bot
                      </button>
                      <button
                        type="button"
                        :disabled="botSaving"
                        class="h-9 rounded-lg bg-brand px-3 text-xs font-medium text-brand-ink transition hover:bg-brand-strong disabled:opacity-60"
                        @click="saveBotPool"
                      >
                        保存 Bot 池
                      </button>
                    </div>
                    <p v-if="botError !== ''" class="text-xs text-danger">{{ botError }}</p>
                    <p v-if="botNotice !== ''" class="text-xs text-muted">{{ botNotice }}</p>
                  </div>
                </div>

                <div v-show="activeSection === 'network'" class="rounded-2xl bg-surface shadow-[0_1px_0_0_rgba(27,26,23,0.05)]">
                  <div class="border-b border-line/70 px-5 py-4">
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
                          class="flex min-h-11 cursor-pointer items-center gap-2.5 rounded-xl border border-line bg-canvas px-3 py-2.5 text-sm transition hover:border-ink/25 focus-within:border-ink"
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
                          class="h-11 min-w-0 flex-1 rounded-xl border border-line bg-canvas px-3 text-sm outline-none transition focus:border-ink/40 focus:bg-surface disabled:opacity-60"
                          :class="fieldErrors.includes(FIELD_PROXY_URL) ? 'border-danger' : ''"
                        />
                        <button
                          v-if="view.proxy.url_set && view.proxy.mode !== 'custom' && !managed(FIELD_PROXY_URL)"
                          type="button"
                          :disabled="saving"
                          class="h-11 shrink-0 rounded-xl border border-line px-3 text-xs font-medium text-muted transition hover:bg-canvas disabled:opacity-60"
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
                        class="flex min-h-11 cursor-pointer items-center gap-2.5 rounded-xl border border-line bg-canvas px-3 py-2.5 text-sm transition hover:border-ink/25 focus-within:border-ink"
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
                        class="flex min-h-11 cursor-pointer items-center gap-2.5 rounded-xl border border-line bg-canvas px-3 py-2.5 text-sm transition hover:border-ink/25 focus-within:border-ink"
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

            <section id="check" v-show="activeSection === 'network'" class="scroll-mt-36">
              <div class="flex items-center gap-2">
                <h2 class="text-base font-semibold tracking-tight">连通性自检</h2>
              </div>
              <p class="mt-1 text-xs text-muted">并行检测 Bot 与频道权限、Telegraph 账户，只读请求，不发送任何内容。</p>

              <div class="mt-3 rounded-2xl bg-surface shadow-[0_1px_0_0_rgba(27,26,23,0.05)]">
                <div class="flex flex-wrap items-center justify-between gap-3 border-b border-line px-5 py-3.5">
                  <p class="text-xs text-muted">保存设置后再检测，读到的是最新上游配置。</p>
                  <button
                    type="button"
                    :disabled="checking"
                    class="h-11 shrink-0 rounded-xl border border-line px-3.5 text-sm font-medium text-muted transition hover:bg-canvas hover:text-ink disabled:opacity-60"
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
                        class="flex flex-wrap items-center justify-between gap-2 rounded-xl bg-canvas px-3 py-2.5"
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
            </div>

            <div v-show="activeSection === 'telegraph' || activeSection === 'upload'" class="space-y-8">
            <section id="defaults" v-show="activeSection === 'upload'" class="scroll-mt-36">
              <div class="flex items-center gap-2">
                <h2 class="text-base font-semibold tracking-tight">发帖默认值</h2>
              </div>
              <p class="mt-1 text-xs text-muted">说明文件命中后自动预填标题与说明，工作台的解析方式默认按这里。</p>

              <div class="mt-3 rounded-2xl bg-surface shadow-[0_1px_0_0_rgba(27,26,23,0.05)]">
                <div class="space-y-4 px-5 py-4">
                  <div>
                    <label class="block text-xs font-medium text-muted" for="meta-filenames">说明文件名</label>
                    <input
                      id="meta-filenames"
                      v-model="form.metaFilenames"
                      type="text"
                      class="mt-1 h-11 w-full rounded-xl border border-line bg-canvas px-3 text-sm outline-none transition focus:border-ink/40 focus:bg-surface"
                      placeholder="info.txt, meta.txt, readme.txt"
                    />
                    <p class="mt-1 text-xs text-muted">多个文件名用逗号分隔，最多 10 个，匹配时不区分大小写。</p>
                  </div>
                  <div>
                    <label class="block text-xs font-medium text-muted" for="meta-parse-mode">解析方式</label>
                    <select
                      id="meta-parse-mode"
                      v-model="form.metaParseMode"
                      class="mt-1 h-11 w-full rounded-xl border border-line bg-canvas px-3 text-sm outline-none transition focus:border-ink/40 focus:bg-surface"
                    >
                      <option value="first-line">首行标题 + 其余正文</option>
                      <option value="key-value">键值匹配</option>
                    </select>
                  </div>
                  <fieldset>
                    <legend class="text-xs font-medium text-muted">默认画质</legend>
                    <div class="mt-1 grid grid-cols-1 gap-2 sm:grid-cols-2">
                      <label
                        v-for="option in UPLOAD_MODES"
                        :key="option.value"
                        class="flex min-h-11 cursor-pointer items-center gap-2.5 rounded-xl border border-line bg-canvas px-3 py-2.5 text-sm transition hover:border-ink/25 focus-within:border-ink"
                        :class="form.defaultMode === option.value ? 'border-accent bg-accent-soft' : ''"
                      >
                        <input
                          v-model="form.defaultMode"
                          type="radio"
                          name="default-mode"
                          :value="option.value"
                          :disabled="saving || managed(FIELD_UPLOAD_MODE)"
                          class="h-6 w-6 shrink-0 accent-accent"
                        />
                        <span>{{ option.label }}</span>
                      </label>
                    </div>
                    <p v-if="managed(FIELD_UPLOAD_MODE)" class="mt-1 text-xs text-muted">{{ ENV_MANAGED_HINT }}</p>
                    <p class="mt-1 text-xs text-muted">新任务默认使用该画质，单帖仍可在工作台覆盖；续传沿用原任务的画质。</p>
                  </fieldset>
                  <label class="block">
                    <span class="text-xs font-medium text-muted">同时上传数</span>
                    <select
                      v-model="form.uploadConcurrency"
                      class="mt-1 h-11 w-full rounded-xl border border-line bg-canvas px-3 text-sm outline-none transition focus:border-ink/40 focus:bg-surface sm:w-56"
                      @change="applyUploadConcurrency"
                    >
                      <option v-for="count in 4" :key="count" :value="String(count)">{{ count }} 路</option>
                    </select>
                    <p class="mt-1 text-xs text-muted">
                      每轮同时上传的图片数，范围 {{ UPLOAD_CONCURRENCY_MIN }}–{{ UPLOAD_CONCURRENCY_MAX }} 路，默认 2 路；服务端上限 {{ uploadCap }} 路，超出时按上限派发，调整后下一轮上传生效。
                    </p>
                  </label>
                </div>
              </div>
            </section>

                <div v-show="activeSection === 'telegraph'" class="rounded-2xl bg-surface shadow-[0_1px_0_0_rgba(27,26,23,0.05)]">
                  <div class="border-b border-line/70 px-5 py-4">
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
                        class="mt-1 h-11 w-full rounded-xl border border-line bg-canvas px-3 text-sm outline-none transition focus:border-ink/40 focus:bg-surface disabled:opacity-60"
                        :class="fieldErrors.includes(FIELD_TELEGRAPH_AUTHOR) ? 'border-danger' : ''"
                      />
                      <p v-if="fieldErrorText(FIELD_TELEGRAPH_AUTHOR) !== ''" class="mt-1 text-xs text-danger">
                        {{ fieldErrorText(FIELD_TELEGRAPH_AUTHOR) }}
                      </p>
                      <p v-else-if="managed(FIELD_TELEGRAPH_AUTHOR)" class="mt-1 text-xs text-muted">
                        {{ ENV_MANAGED_HINT }}
                      </p>
                    </label>

                    <fieldset>
                      <legend class="text-xs font-medium text-muted">链接生成方式</legend>
                      <div class="mt-1 grid grid-cols-1 gap-2 sm:grid-cols-2">
                        <label
                          v-for="option in PATH_MODES"
                          :key="option.value"
                          class="flex min-h-11 cursor-pointer items-center gap-2.5 rounded-xl border border-line bg-canvas px-3 py-2.5 text-sm transition hover:border-ink/25 focus-within:border-ink"
                          :class="form.pathMode === option.value ? 'border-accent bg-accent-soft' : ''"
                        >
                          <input
                            v-model="form.pathMode"
                            type="radio"
                            name="path-mode"
                            :value="option.value"
                            :disabled="saving || managed(FIELD_TELEGRAPH_PATH_MODE)"
                            class="h-6 w-6 shrink-0 accent-accent"
                          />
                          <span>{{ option.label }}</span>
                        </label>
                      </div>
                      <p class="mt-1 text-xs text-muted">
                        只影响之后发起的发布，页面仍显示原标题；已发布页面链接不会改变。
                      </p>
                      <p v-if="managed(FIELD_TELEGRAPH_PATH_MODE)" class="mt-1 text-xs text-muted">{{ ENV_MANAGED_HINT }}</p>
                    </fieldset>

                    <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
                      <div class="rounded-xl bg-canvas px-3 py-2.5">
                        <p class="text-xs text-muted">访问令牌</p>
                        <p class="mt-0.5 text-sm">{{ view.telegraph.token_set ? '已配置' : '未配置' }}</p>
                      </div>
                      <div class="rounded-xl bg-canvas px-3 py-2.5">
                        <p class="text-xs text-muted">上游地址</p>
                        <p class="mt-0.5 text-sm">{{ endpointText(view.telegraph.api_base_source) }}</p>
                      </div>
                    </div>

                    <div class="flex flex-wrap items-center justify-between gap-3 rounded-xl bg-canvas px-3 py-2.5">
                      <div class="min-w-0">
                        <p class="text-xs text-muted">{{ telegraphAccountHint(telegraphState(), telegraphSubmitting) }}</p>
                        <p v-if="telegraphError !== ''" class="mt-1 text-xs text-danger">{{ telegraphError }}</p>
                        <p v-else-if="telegraphNote !== ''" class="mt-1 text-xs text-accent">{{ telegraphNote }}</p>
                      </div>
                      <button
                        type="button"
                        :disabled="telegraphAccountDisabled(telegraphState()) || telegraphSubmitting"
                        class="h-11 shrink-0 rounded-xl border border-line px-3.5 text-sm font-medium text-muted transition hover:bg-surface hover:text-ink disabled:opacity-60"
                        @click="requestTelegraphAccount"
                      >
                        {{ telegraphSubmitting ? '申请中…' : '申请账户' }}
                      </button>
                    </div>
                  </div>
                </div>

            </div>





            <div v-show="activeSection === 'traffic' || activeSection === 'maintenance'" class="space-y-8">
            <section id="ops" class="scroll-mt-36">
              <div class="flex items-center gap-2">
                <h2 v-show="activeSection === 'traffic'" class="text-base font-semibold tracking-tight">流量看护</h2>
                <h2 v-show="activeSection === 'maintenance'" class="text-base font-semibold tracking-tight">备份与缓存</h2>
              </div>
              <p v-show="activeSection === 'traffic'" class="mt-1 text-xs text-muted">按自然月统计图片出站流量，达到阈值提醒或熔断。</p>
              <p v-show="activeSection === 'maintenance'" class="mt-1 text-xs text-muted">每日快照与逐图小图缓存，各自独立保存与维护。</p>

              <div class="mt-3 space-y-3">
                <div v-show="activeSection === 'traffic'" class="rounded-2xl bg-surface shadow-[0_1px_0_0_rgba(27,26,23,0.05)]">
                  <div class="flex flex-wrap items-center justify-between gap-3 border-b border-line px-5 py-3.5">
                    <h3 class="text-sm font-semibold">流量看护</h3>
                    <button
                      type="button"
                      class="h-11 shrink-0 rounded-xl border border-line px-3.5 text-sm font-medium text-muted transition hover:bg-canvas hover:text-ink"
                      @click="loadTraffic"
                    >
                      刷新
                    </button>
                  </div>
                  <div class="px-5 py-4">
                    <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
                      <label class="block">
                        <span class="text-xs font-medium text-muted">月流量预警（GB）</span>
                        <input
                          v-model="form.alertGB"
                          type="number"
                          min="0"
                          step="0.1"
                          :disabled="saving || managed(FIELD_TRAFFIC_ALERT)"
                          placeholder="留空表示关闭"
                          class="mt-1 h-11 w-full rounded-xl border border-line bg-canvas px-3 text-sm outline-none transition focus:border-ink/40 focus:bg-surface disabled:opacity-60"
                          :class="fieldErrors.includes(FIELD_TRAFFIC_ALERT) ? 'border-danger' : ''"
                        />
                        <p v-if="managed(FIELD_TRAFFIC_ALERT)" class="mt-1 text-xs text-muted">{{ ENV_MANAGED_HINT }}</p>
                      </label>
                      <label class="block">
                        <span class="text-xs font-medium text-muted">月流量硬上限（GB）</span>
                        <input
                          v-model="form.limitGB"
                          type="number"
                          min="0"
                          step="0.1"
                          :disabled="saving || managed(FIELD_TRAFFIC_LIMIT)"
                          placeholder="留空表示不熔断"
                          class="mt-1 h-11 w-full rounded-xl border border-line bg-canvas px-3 text-sm outline-none transition focus:border-ink/40 focus:bg-surface disabled:opacity-60"
                          :class="fieldErrors.includes(FIELD_TRAFFIC_LIMIT) ? 'border-danger' : ''"
                        />
                        <p v-if="managed(FIELD_TRAFFIC_LIMIT)" class="mt-1 text-xs text-muted">{{ ENV_MANAGED_HINT }}</p>
                      </label>
                    </div>
                    <p class="mt-1 text-xs text-muted">保存后立即生效，当前月份计数保持不变；硬上限触发后图片网关返回 503。</p>

                    <p v-if="trafficError !== ''" class="text-xs text-danger">{{ trafficError }}</p>
                    <p v-else-if="traffic === null" class="text-xs text-muted">尚未读取。</p>
                    <template v-else>
                      <div class="grid grid-cols-1 gap-3 sm:grid-cols-3">
                        <div class="rounded-xl bg-canvas px-3 py-2.5">
                          <p class="text-xs text-muted">本月出站</p>
                          <p class="mt-0.5 text-sm font-medium tnum">{{ formatTrafficBytes(traffic.bytes_out) }}</p>
                        </div>
                        <div class="rounded-xl bg-canvas px-3 py-2.5">
                          <p class="text-xs text-muted">统计月份</p>
                          <p class="mt-0.5 text-sm font-medium tnum">{{ traffic.month }}</p>
                        </div>
                        <div class="rounded-xl bg-canvas px-3 py-2.5">
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

                <div id="backup" v-show="activeSection === 'maintenance'" class="scroll-mt-36 rounded-2xl bg-surface shadow-[0_1px_0_0_rgba(27,26,23,0.05)]">
                  <div class="flex flex-wrap items-center justify-between gap-3 border-b border-line px-5 py-3.5">
                    <h3 class="text-sm font-semibold">自动备份</h3>
                    <div class="flex shrink-0 items-center gap-2">
                      <button
                        type="button"
                        :disabled="backing"
                        class="h-9 rounded-lg border border-line px-3 text-xs font-medium text-muted transition hover:bg-canvas hover:text-ink disabled:opacity-60"
                        @click="runBackup"
                      >
                        {{ backing ? '备份中…' : '立即备份' }}
                      </button>
                      <a
                        v-if="backup !== null && backup.latest !== null"
                        href="/api/backup/download"
                        class="inline-flex h-9 items-center rounded-lg border border-line px-3 text-xs font-medium text-muted transition hover:bg-canvas hover:text-ink"
                      >
                        下载最近备份
                      </a>
                    </div>
                  </div>
                  <div class="px-5 py-4">
                    <label class="flex min-h-11 items-center justify-between gap-2.5 rounded-xl bg-canvas px-3.5 py-2.5 text-sm">
                      <span class="min-w-0">启用每日自动备份<span v-if="managed(FIELD_BACKUP_ENABLED)" class="mt-0.5 block text-xs text-muted">{{ ENV_MANAGED_HINT }}</span></span>
                      <input
                        v-model="form.backupEnabled"
                        type="checkbox"
                        :disabled="saving || managed(FIELD_BACKUP_ENABLED)"
                        class="h-5 w-5 shrink-0 accent-accent"
                      />
                    </label>

                    <div class="mt-3 grid grid-cols-1 gap-3 sm:grid-cols-2">
                      <label class="block">
                        <span class="text-xs font-medium text-muted">每日时刻</span>
                        <input
                          v-model="form.backupTime"
                          type="time"
                          :disabled="saving || managed(FIELD_BACKUP_TIME)"
                          class="mt-1 h-11 w-full rounded-xl border border-line bg-canvas px-3 text-sm outline-none transition focus:border-ink/40 focus:bg-surface disabled:opacity-60"
                          :class="fieldErrors.includes(FIELD_BACKUP_TIME) ? 'border-danger' : ''"
                        />
                      </label>
                      <label class="block">
                        <span class="text-xs font-medium text-muted">本地保留份数</span>
                        <input
                          v-model="form.backupKeep"
                          type="number"
                          min="1"
                          max="60"
                          :disabled="saving || managed(FIELD_BACKUP_KEEP)"
                          class="mt-1 h-11 w-full rounded-xl border border-line bg-canvas px-3 text-sm outline-none transition focus:border-ink/40 focus:bg-surface disabled:opacity-60"
                          :class="fieldErrors.includes(FIELD_BACKUP_KEEP) ? 'border-danger' : ''"
                        />
                      </label>
                    </div>
                    <p class="mt-1 text-xs text-muted">按服务器时区执行；计划保存后立即更新下一次执行时间。运行中的备份会自然完成。</p>

                    <p v-if="backupError !== ''" class="text-xs text-danger">{{ backupError }}</p>
                    <p v-else-if="backupNote !== ''" class="text-xs text-accent">{{ backupNote }}</p>
                    <div class="mt-3 grid grid-cols-1 gap-3 rounded-xl bg-canvas px-3.5 py-2.5 sm:grid-cols-2">
                      <div>
                        <p class="text-xs text-muted">执行计划</p>
                        <p class="mt-0.5 text-sm">{{ backup !== null ? backupScheduleText(backup) : '尚未读取' }}</p>
                      </div>
                      <div>
                        <p class="text-xs text-muted">最近备份</p>
                        <p class="mt-0.5 text-sm">{{ backup !== null ? backupLatestText(backup) : '尚未读取' }}</p>
                      </div>
                    </div>
                  </div>
                </div>

                <div id="cache" v-show="activeSection === 'maintenance'" class="scroll-mt-36 rounded-2xl bg-surface shadow-[0_1px_0_0_rgba(27,26,23,0.05)]">
                  <div class="flex flex-wrap items-center justify-between gap-3 border-b border-line px-5 py-3.5">
                    <h3 class="text-sm font-semibold">媒体缩略图缓存</h3>
                    <button
                      type="button"
                      class="h-9 shrink-0 rounded-lg border border-line px-3 text-xs font-medium text-muted transition hover:bg-canvas hover:text-ink"
                      @click="refreshThumbUsage"
                    >
                      刷新占用
                    </button>
                  </div>
                  <div class="space-y-3 px-5 py-4">
                    <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
                    <label class="block">
                      <span class="text-xs font-medium text-muted">缓存上限（GB）</span>
                      <input
                        v-model="form.thumbGB"
                        type="number"
                        min="0.0625"
                        max="50"
                        step="0.5"
                        :disabled="saving || managed(FIELD_MEDIA_THUMBS_LIMIT)"
                        class="mt-1 h-11 w-full rounded-xl border border-line bg-canvas px-3 text-sm outline-none transition focus:border-ink/40 focus:bg-surface disabled:opacity-60"
                      />
                      <p v-if="managed(FIELD_MEDIA_THUMBS_LIMIT)" class="mt-1 text-xs text-muted">{{ ENV_MANAGED_HINT }}</p>
                    </label>
                    <div class="grid grid-cols-2 gap-3 rounded-xl bg-canvas px-3 py-2.5">
                      <div>
                        <p class="text-xs text-muted">已占用</p>
                        <p class="mt-0.5 text-sm font-medium tnum">{{ formatTrafficBytes(view?.media_thumbs.used_bytes ?? 0) }}</p>
                      </div>
                      <div>
                        <p class="text-xs text-muted">条目数</p>
                        <p class="mt-0.5 text-sm font-medium tnum">{{ view?.media_thumbs.count ?? 0 }}</p>
                      </div>
                    </div>
                    </div>
                    <p class="text-xs text-muted">每项媒体保存一份最长边 512px 的 WebP，行内小图与放大气泡共用；保存后按最近最少使用淘汰到新上限，磁盘余量不足时暂停写入新的小图。</p>

                    <div class="flex flex-wrap items-center gap-3">
                      <button
                        type="button"
                        class="h-9 rounded-lg border border-line px-3 text-xs font-medium text-muted transition hover:bg-canvas hover:text-ink disabled:opacity-60"
                        :disabled="thumbBusy || saving"
                        @click="clearThumbs"
                      >
                        清空缓存
                      </button>
                      <span class="text-xs text-muted">只删小图，原图与已发布内容不受影响。</span>
                    </div>

                    <div class="space-y-2 rounded-xl bg-canvas px-3 py-3">
                      <p class="text-xs font-medium text-muted">按图集重建</p>
                      <div class="flex flex-wrap items-center gap-2">
                        <select
                          v-model.number="rebuildPostId"
                          :disabled="rebuildRunning"
                          class="h-11 min-w-0 flex-1 rounded-xl border border-line bg-surface px-3 text-sm outline-none transition focus:border-ink/40 disabled:opacity-60"
                        >
                          <option :value="null">选择图集</option>
                          <option v-for="item in rebuildPosts" :key="item.id" :value="item.id">
                            {{ item.title }}（{{ item.image_count }} 项）
                          </option>
                        </select>
                        <button
                          type="button"
                          class="h-9 shrink-0 rounded-lg bg-brand px-3 text-xs font-medium text-brand-ink transition hover:bg-brand-strong disabled:opacity-60"
                          :disabled="rebuildRunning || rebuildPostId === null"
                          @click="startRebuild"
                        >
                          开始重建
                        </button>
                        <button
                          v-if="rebuildRunning"
                          type="button"
                          class="h-9 shrink-0 rounded-lg border border-line px-3 text-xs font-medium text-muted transition hover:bg-surface hover:text-ink"
                          @click="cancelRebuild"
                        >
                          取消
                        </button>
                      </div>
                      <p v-if="rebuildProgress.total > 0" class="text-xs text-muted tnum">
                        已处理 {{ rebuildProgress.done }} / {{ rebuildProgress.total }}，失败 {{ rebuildProgress.failed }}
                      </p>
                      <p v-if="rebuildNote !== ''" class="text-xs text-accent">{{ rebuildNote }}</p>
                      <p v-if="rebuildError !== ''" class="text-xs text-danger">{{ rebuildError }}</p>
                      <p v-else class="text-xs text-muted">重建会清掉该图集的小图并换新内容版本，再在浏览器内逐项回源生成，避免同时展开多张大图。</p>
                    </div>

                    <p v-if="thumbError !== ''" class="text-xs text-danger">{{ thumbError }}</p>
                    <p v-else-if="thumbNote !== ''" class="text-xs text-accent">{{ thumbNote }}</p>
                  </div>
                </div>
              </div>
            </section>            </div>


            <div v-show="activeSection === 'appearance'" class="space-y-8">
            <div id="theme" class="scroll-mt-36 rounded-2xl bg-surface shadow-[0_1px_0_0_rgba(27,26,23,0.05)]">
              <div class="border-b border-line/70 px-5 py-4">
                <h3 class="text-sm font-semibold">外观主题</h3>
                <p class="mt-1 text-xs text-muted">跟随系统会随设备日夜自动切换；主题只记在本机，不影响媒体遮罩开关。</p>
              </div>
              <div class="px-5 py-4">
                <div class="inline-flex w-max items-center gap-1 rounded-xl border border-line/70 bg-canvas p-1" role="group" aria-label="主题模式">
                  <button
                    v-for="item in THEME_OPTIONS"
                    :key="item.value"
                    type="button"
                    class="inline-flex h-11 items-center -my-1"
                    :aria-pressed="themeMode === item.value"
                    @click="setThemeMode(item.value)"
                  >
                    <span
                      class="inline-flex h-9 items-center rounded-lg px-3 text-xs font-medium transition"
                      :class="themeMode === item.value ? 'bg-surface text-ink shadow-[0_1px_0_0_rgba(27,26,23,0.08)]' : 'text-muted hover:text-ink'"
                    >
                      {{ item.label }}
                    </span>
                  </button>
                </div>
              </div>
            </div>

            <section id="account" class="scroll-mt-36">
              <div class="flex items-center gap-2">
                <h2 class="text-base font-semibold tracking-tight">账号安全</h2>
              </div>
              <p class="mt-1 text-xs text-muted">修改网站管理员账号名或密码，成功后需要重新登录。</p>

              <div class="mt-3 space-y-3">
                <div class="rounded-2xl bg-surface shadow-[0_1px_0_0_rgba(27,26,23,0.05)]">
                  <div class="border-b border-line/70 px-5 py-4">
                    <h3 class="text-sm font-semibold">修改管理员用户名与密码</h3>
                    <p class="mt-1 text-xs text-muted">先验证当前密码；用户名或新密码留空表示不修改，至少改动一项。</p>
                  </div>
                  <div class="space-y-4 px-5 py-4">
                    <label class="block">
                      <span class="text-xs font-medium text-muted">当前密码</span>
                      <input
                        v-model="credentialForm.currentPassword"
                        type="password"
                        autocomplete="current-password"
                        :disabled="credentialSubmitting"
                        placeholder="用于确认是本人操作"
                        class="mt-1 h-11 w-full rounded-xl border border-line bg-canvas px-3 text-sm outline-none transition focus:border-ink/40 focus:bg-surface disabled:opacity-60"
                        :class="credentialErrorText('current_password') !== '' ? 'border-danger' : ''"
                      />
                      <p v-if="credentialErrorText('current_password') !== ''" class="mt-1 text-xs text-danger">
                        {{ credentialErrorText('current_password') }}
                      </p>
                    </label>

                    <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
                      <label class="block">
                        <span class="text-xs font-medium text-muted">新用户名</span>
                        <input
                          v-model="credentialForm.username"
                          type="text"
                          autocomplete="username"
                          :disabled="credentialSubmitting"
                          placeholder="留空则不修改"
                          class="mt-1 h-11 w-full rounded-xl border border-line bg-canvas px-3 text-sm outline-none transition focus:border-ink/40 focus:bg-surface disabled:opacity-60"
                          :class="credentialErrorText('username') !== '' ? 'border-danger' : ''"
                        />
                        <p v-if="credentialErrorText('username') !== ''" class="mt-1 text-xs text-danger">
                          {{ credentialErrorText('username') }}
                        </p>
                      </label>
                      <label class="block">
                        <span class="text-xs font-medium text-muted">新密码</span>
                        <input
                          v-model="credentialForm.newPassword"
                          type="password"
                          autocomplete="new-password"
                          :disabled="credentialSubmitting"
                          placeholder="至少 12 个字符"
                          class="mt-1 h-11 w-full rounded-xl border border-line bg-canvas px-3 text-sm outline-none transition focus:border-ink/40 focus:bg-surface disabled:opacity-60"
                          :class="credentialErrorText('new_password') !== '' ? 'border-danger' : ''"
                        />
                        <p v-if="credentialErrorText('new_password') !== ''" class="mt-1 text-xs text-danger">
                          {{ credentialErrorText('new_password') }}
                        </p>
                      </label>
                    </div>

                    <label class="block">
                      <span class="text-xs font-medium text-muted">确认新密码</span>
                      <input
                        v-model="credentialForm.confirmPassword"
                        type="password"
                        autocomplete="new-password"
                        :disabled="credentialSubmitting"
                        placeholder="再次输入新密码"
                        class="mt-1 h-11 w-full rounded-xl border border-line bg-canvas px-3 text-sm outline-none transition focus:border-ink/40 focus:bg-surface disabled:opacity-60"
                        :class="credentialErrorText('confirm_password') !== '' ? 'border-danger' : ''"
                      />
                      <p v-if="credentialErrorText('confirm_password') !== ''" class="mt-1 text-xs text-danger">
                        {{ credentialErrorText('confirm_password') }}
                      </p>
                    </label>

                    <div class="flex flex-wrap items-center justify-between gap-3">
                      <p
                        class="text-xs"
                        :class="credentialError !== '' ? 'text-danger' : credentialNote !== '' ? 'text-accent' : 'text-muted'"
                      >
                        {{ credentialError !== '' ? credentialError : credentialNote !== '' ? credentialNote : '修改成功后当前登录会失效，需要用新凭据重新登录。' }}
                      </p>
                      <button
                        type="button"
                        :disabled="credentialSubmitting"
                        class="h-11 shrink-0 rounded-xl bg-brand px-4 text-sm font-medium text-brand-ink transition hover:bg-brand-strong disabled:opacity-60"
                        @click="submitCredentials"
                      >
                        {{ credentialSubmitting ? '提交中…' : '保存账号修改' }}
                      </button>
                    </div>
                  </div>
                </div>
              </div>
            </section>            </div>

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
            class="h-11 shrink-0 rounded-xl bg-brand px-4 text-sm font-medium text-brand-ink transition hover:bg-brand-strong disabled:opacity-60"
            @click="save"
          >
            {{ saving ? '保存中…' : '保存设置' }}
          </button>
        </div>
      </div>
    </main>

    <BackToTop />
  </div>
</template>
