// 设置页的展示与表单逻辑，保持为纯函数便于单元测试。
import type {
  BackupStatus,
  ConnectionResult,
  ConnectionTarget,
  SettingsPatch,
  SettingsView,
  TrafficStats,
} from '../api/settings.ts'

/** 环境变量托管字段的统一说明。 */
export const ENV_MANAGED_HINT = '由环境变量提供，界面不可修改'

/** 设置页可编辑字段与后端字段名的对应关系。 */
export const FIELD_TELEGRAM_TOKEN = 'telegram.token'
export const FIELD_TELEGRAM_CHANNEL = 'telegram.channel_id'
export const FIELD_TELEGRAPH_AUTHOR = 'telegraph.author_name'
export const FIELD_TELEGRAPH_TOKEN = 'telegraph.token'
export const FIELD_PROXY_URL = 'proxy.url'
export const FIELD_PROXY_MODE = 'proxy.mode'
export const FIELD_PROXY_TELEGRAM = 'proxy.telegram'
export const FIELD_PROXY_TELEGRAPH = 'proxy.telegraph'
export const FIELD_TELEGRAPH_PATH_MODE = 'telegraph.path_mode'
export const FIELD_UPLOAD_MODE = 'post.upload_mode'
export const FIELD_TRAFFIC_ALERT = 'traffic.alert_bytes'
export const FIELD_TRAFFIC_LIMIT = 'traffic.limit_bytes'
export const FIELD_BACKUP_TIME = 'backup.time'
export const FIELD_BACKUP_KEEP = 'backup.keep'

/** 代理模式选项，顺序即界面顺序。 */
export const PROXY_MODES = [
  { value: 'direct', label: '直连' },
  { value: 'system', label: '跟随系统环境变量' },
  { value: 'custom', label: '自定义代理' },
] as const

/** 链接生成方式选项，顺序即界面顺序。 */
export const PATH_MODES = [
  { value: 'title', label: '按标题生成' },
  { value: 'slug', label: '自动生成无中文链接' },
] as const

/** 默认画质选项，文案与工作台、详情页保持一致。 */
export const UPLOAD_MODES = [
  { value: 'photo', label: 'photo 省流量' },
  { value: 'document', label: 'document 原图' },
] as const

/** pathModeLabel 返回链接生成方式的中文名，未知取值原样展示。 */
export function pathModeLabel(mode: string): string {
  const found = PATH_MODES.find((item) => item.value === mode)
  return found === undefined ? mode : found.label
}

/** uploadModeLabel 返回画质模式的中文名，未知取值原样展示。 */
export function uploadModeLabel(mode: string): string {
  const found = UPLOAD_MODES.find((item) => item.value === mode)
  return found === undefined ? mode : found.label
}

/** bytesToGB 把字节数转成输入框里的 GB 文本，0 显示为空。 */
export function bytesToGB(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return ''
  return String(Math.round((bytes / (1 << 30)) * 100) / 100)
}

/** gbToBytes 把 GB 文本转成字节数；空文本表示关闭，非法返回 null。 */
export function gbToBytes(text: string): number | null {
  const trimmed = text.trim()
  if (trimmed === '') return 0
  const value = Number(trimmed)
  if (!Number.isFinite(value) || value < 0) return null
  return Math.round(value * (1 << 30))
}

/** backupTimeInvalid 校验 HH:MM 时刻。 */
export function backupTimeInvalid(text: string): boolean {
  return !/^([01]?[0-9]|2[0-3]):[0-5][0-9]$/.test(text.trim())
}

/** proxyModeLabel 返回代理模式的中文名，未知取值原样展示。 */
export function proxyModeLabel(mode: string): string {
  const found = PROXY_MODES.find((item) => item.value === mode)
  return found === undefined ? mode : found.label
}

/** proxyURLHint 描述自定义代理地址的占位文案。 */
export function proxyURLHint(urlSet: boolean, mode: string): string {
  if (urlSet) return '已设置，留空不修改'
  if (mode === 'custom') return 'http://、https:// 或 socks5:// 地址'
  return '仅在自定义模式使用'
}

/** proxyURLInvalid 校验自定义代理地址的形态：协议、主机与可选账密。 */
export function proxyURLInvalid(value: string): boolean {
  const trimmed = value.trim()
  if (trimmed === '') return false
  if (/\s/.test(trimmed)) return true
  return !/^(?:https?|socks5):\/\/[^/?#]+/i.test(trimmed)
}

/** proxyURLText 说明代理地址来源，地址本身一律不回显。 */
export function proxyURLText(urlSet: boolean, source: string): string {
  if (!urlSet) return '未设置'
  if (source === 'stored') return '已保存在服务端，不回显'
  return '已设置'
}

/** proxyCustomNeedsURL 判断切到自定义模式时是否还缺地址。 */
export function proxyCustomNeedsURL(mode: string, urlSet: boolean, newURL: string): boolean {
  return mode === 'custom' && !urlSet && newURL.trim() === ''
}

/** tokenPlaceholder 返回令牌输入框的占位文案。 */
export function tokenPlaceholder(tokenSet: boolean): string {
  return tokenSet ? '已设置，留空不修改' : '填入 Bot Token'
}

/** endpointText 描述上游地址的来源；自定义地址不回显，只展示来源。 */
export function endpointText(source: string): string {
  switch (source) {
    case 'default':
      return '官方默认地址'
    case 'env':
      return '由环境变量提供，地址不回显'
    case 'stored':
      return '数据库中的地址'
    default:
      return '未配置'
  }
}

/** connectionLabel 返回自检状态的按钮文案。 */
export function connectionLabel(status: string): string {
  switch (status) {
    case 'ok':
      return '可用'
    case 'failed':
      return '失败'
    case 'unconfigured':
      return '未配置'
    default:
      return '未检测'
  }
}

/** connectionTone 返回自检状态对应的文字色。 */
export function connectionTone(status: string): string {
  switch (status) {
    case 'ok':
      return 'text-accent'
    case 'failed':
      return 'text-danger'
    case 'unconfigured':
      return 'text-muted'
    default:
      return 'text-muted'
  }
}

/** connectionLatency 把毫秒数格式化为可读文案，未配置时不显示耗时。 */
export function connectionLatency(target: ConnectionTarget | undefined): string {
  if (target === undefined || target.status === 'unconfigured') return ''
  return target.latency_ms + ' ms'
}

/** connectionSummary 汇总一次自检的结论，用于按钮旁的提示。 */
export function connectionSummary(result: ConnectionResult): string {
  const failed = [result.telegram, result.telegraph].filter((item) => item.status === 'failed').length
  const ok = [result.telegram, result.telegraph].filter((item) => item.status === 'ok').length
  if (failed > 0) return '有 ' + String(failed) + ' 项检测失败，请检查配置'
  if (ok === 2) return '两项检测均通过'
  return '部分目标尚未配置'
}

/** SettingsForm 是设置页表单的取值。 */
export interface SettingsForm {
  alertGB: string
  limitGB: string
  backupEnabled: boolean
  backupTime: string
  backupKeep: string
  token: string
  channelId: string
  authorName: string
  pathMode: string
  defaultMode: string
  proxyMode: string
  proxyUrl: string
  proxyTelegram: boolean
  proxyTelegraph: boolean
  metaFilenames: string
  metaParseMode: string
}

/** splitMetaFilenames 把逗号或顿号分隔的输入拆成候选文件名列表。 */
export function splitMetaFilenames(raw: string): string[] {
  return raw
    .split(/[,，、]/)
    .map((name) => name.trim())
    .filter((name) => name !== '')
}

/** changedPatch 只提交改动且非环境变量托管的字段。 */
export function changedPatch(view: SettingsView, form: SettingsForm): SettingsPatch {
  const patch: SettingsPatch = {}
  const telegram: { token?: string; channel_id?: string } = {}
  const telegraph: { author_name?: string; path_mode?: string } = {}
  const proxy: NonNullable<SettingsPatch['proxy']> = {}

  if (form.token !== '' && !view.env_managed.includes(FIELD_TELEGRAM_TOKEN)) {
    telegram.token = form.token
  }
  if (form.channelId !== view.telegram.channel_id && !view.env_managed.includes(FIELD_TELEGRAM_CHANNEL)) {
    telegram.channel_id = form.channelId
  }
  if (form.authorName !== view.telegraph.author_name && !view.env_managed.includes(FIELD_TELEGRAPH_AUTHOR)) {
    telegraph.author_name = form.authorName
  }
  if (form.pathMode !== view.telegraph.path_mode && !view.env_managed.includes(FIELD_TELEGRAPH_PATH_MODE)) {
    telegraph.path_mode = form.pathMode
  }
  if (form.proxyMode !== view.proxy.mode) {
    proxy.mode = form.proxyMode
  }
  if (form.proxyUrl !== '') {
    proxy.url = form.proxyUrl
  }
  if (form.proxyTelegram !== view.proxy.telegram_enabled) {
    proxy.telegram_enabled = form.proxyTelegram
  }
  if (form.proxyTelegraph !== view.proxy.telegraph_enabled) {
    proxy.telegraph_enabled = form.proxyTelegraph
  }
  const meta: NonNullable<SettingsPatch['meta']> = {}
  if (form.metaFilenames.trim() !== view.meta.filenames.join(', ')) {
    meta.filenames = splitMetaFilenames(form.metaFilenames)
  }
  if (form.metaParseMode !== view.meta.parse_mode) {
    meta.parse_mode = form.metaParseMode
  }
  if (form.defaultMode !== view.upload.default_mode && !view.env_managed.includes(FIELD_UPLOAD_MODE)) {
    patch.upload = { mode: form.defaultMode }
  }
  const alert = gbToBytes(form.alertGB)
  const limit = gbToBytes(form.limitGB)
  if (alert !== null && alert !== view.traffic.alert_bytes && !view.env_managed.includes(FIELD_TRAFFIC_ALERT)) {
    patch.traffic = { ...(patch.traffic ?? {}), alert_bytes: alert }
  }
  if (limit !== null && limit !== view.traffic.limit_bytes && !view.env_managed.includes(FIELD_TRAFFIC_LIMIT)) {
    patch.traffic = { ...(patch.traffic ?? {}), limit_bytes: limit }
  }
  const keep = Number(form.backupKeep)
  if (
    Number.isInteger(keep) &&
    keep >= 1 &&
    keep <= 60 &&
    keep !== view.backup.keep &&
    !view.env_managed.includes(FIELD_BACKUP_KEEP)
  ) {
    patch.backup = { ...(patch.backup ?? {}), keep }
  }
  if (form.backupEnabled !== view.backup.enabled) {
    patch.backup = { ...(patch.backup ?? {}), enabled: form.backupEnabled }
  }
  if (
    form.backupTime !== view.backup.time &&
    !backupTimeInvalid(form.backupTime) &&
    !view.env_managed.includes(FIELD_BACKUP_TIME)
  ) {
    patch.backup = { ...(patch.backup ?? {}), time: form.backupTime.trim() }
  }
  if (Object.keys(telegram).length > 0) patch.telegram = telegram
  if (Object.keys(telegraph).length > 0) patch.telegraph = telegraph
  if (Object.keys(proxy).length > 0) patch.proxy = proxy
  if (Object.keys(meta).length > 0) patch.meta = meta
  return patch
}

/** hasPatch 判断补丁是否包含任何字段。 */
export function hasPatch(patch: SettingsPatch): boolean {
  return Object.keys(patch).length > 0
}

/** channelInvalid 校验频道标识：@用户名或 -100 数字编号。 */
export function channelInvalid(value: string): boolean {
  const trimmed = value.trim()
  if (trimmed === '') return false
  return !/^(?:@[A-Za-z][A-Za-z0-9_]{3,63}|-?\d{5,20})$/.test(trimmed)
}

/** saveFailureText 把保存失败映射成界面提示，字段冲突时指出具体项。 */
export function saveFailureText(status: number, message: string, fields: string[]): string {
  if (status === 401) return '登录已失效，请重新登录后再修改'
  if (fields.length > 0) return message + '（' + fields.join('、') + '）'
  return message !== '' ? message : '保存失败，请稍后重试'
}

/** formatTrafficBytes 把字节数格式化为带单位的可读文本。 */
export function formatTrafficBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let value = bytes
  let index = 0
  while (value >= 1024 && index < units.length - 1) {
    value /= 1024
    index += 1
  }
  const text = index === 0 ? String(Math.round(value)) : value.toFixed(2)
  return text + ' ' + units[index]
}

/** trafficState 返回流量状态：正常、已达预警或已超上限。 */
export function trafficState(stats: TrafficStats): 'ok' | 'alert' | 'over' {
  if (stats.limit_bytes > 0 && stats.bytes_out >= stats.limit_bytes) return 'over'
  if (stats.alert_bytes > 0 && stats.bytes_out >= stats.alert_bytes) return 'alert'
  return 'ok'
}

/** trafficStateText 返回流量状态的中文说明。 */
export function trafficStateText(stats: TrafficStats): string {
  switch (trafficState(stats)) {
    case 'over':
      return '已达上限，图片网关返回 503'
    case 'alert':
      return '已达预警阈值，已发送一次提醒'
    default:
      return '正常'
  }
}

/** trafficSummary 汇总阈值文案，未配置时明确说明。 */
export function trafficSummary(stats: TrafficStats): string {
  const alert = stats.alert_bytes > 0 ? '预警 ' + formatTrafficBytes(stats.alert_bytes) : '未设置预警'
  const limit = stats.limit_bytes > 0 ? '上限 ' + formatTrafficBytes(stats.limit_bytes) : '未设置上限'
  return alert + ' · ' + limit + ' · 请求 ' + String(stats.request_count) + ' 次'
}

/** backupScheduleText 描述定时备份状态。 */
export function backupScheduleText(status: BackupStatus): string {
  if (!status.enabled || status.schedule === '') {
    return '未启用定时备份（设置 BACKUP_ENABLED=true 后按 BACKUP_CRON 执行）'
  }
  const next = status.next_run ? '，下一次 ' + status.next_run : ''
  return '定时备份：' + status.schedule + next
}

/** backupLatestText 描述最近一份备份；没有备份时给出提示。 */
export function backupLatestText(status: BackupStatus): string {
  if (status.latest === null) return '还没有备份'
  const created = new Date(status.latest.created_at)
  const stamp = Number.isNaN(created.getTime())
    ? status.latest.created_at
    : created.toLocaleString('zh-CN', { hour12: false })
  return status.latest.name + ' · ' + formatTrafficBytes(status.latest.size) + ' · ' + stamp
}

/** TelegraphAccountState 描述建号入口的可用状态。 */
export type TelegraphAccountState = 'managed' | 'configured' | 'ready'

/** telegraphAccountState 判断建号入口的状态：环境托管、已配置或可申请。 */
export function telegraphAccountState(view: SettingsView): TelegraphAccountState {
  if (view.env_managed.includes(FIELD_TELEGRAPH_TOKEN) || view.telegraph.token_source === 'env') {
    return 'managed'
  }
  if (view.telegraph.token_set) return 'configured'
  return 'ready'
}

/** telegraphAccountHint 说明建号入口当前为何不可点或可点。 */
export function telegraphAccountHint(state: TelegraphAccountState, submitting: boolean): string {
  switch (state) {
    case 'managed':
      return '访问令牌由环境变量提供，界面不再申请'
    case 'configured':
      return '已配置访问令牌，无需重复申请'
    default:
      return submitting ? '正在申请匿名账户…' : '未配置时可按当前作者名一键申请匿名账户'
  }
}

/** telegraphAccountDisabled 只在未配置且没有托管时允许申请。 */
export function telegraphAccountDisabled(state: TelegraphAccountState): boolean {
  return state !== 'ready'
}

/** 新用户名可用的字符与长度。 */
export const USERNAME_PATTERN = /^[A-Za-z0-9._-]{1,64}$/
/** 新密码的最短字符数与 bcrypt 的字节上限。 */
export const NEW_PASSWORD_MIN = 12
export const NEW_PASSWORD_MAX_BYTES = 72

/** CredentialForm 是管理员账号安全表单的取值。 */
export interface CredentialForm {
  currentPassword: string
  username: string
  newPassword: string
  confirmPassword: string
}

/** CredentialField 是表单字段的错误键。 */
export type CredentialField = 'current_password' | 'username' | 'new_password' | 'confirm_password'

/** CredentialErrors 保存字段级错误文案。 */
export type CredentialErrors = Partial<Record<CredentialField, string>>

/** credentialErrors 校验管理员凭据表单，返回字段级提示。 */
export function credentialErrors(form: CredentialForm): CredentialErrors {
  const errors: CredentialErrors = {}
  if (form.currentPassword === '') {
    errors.current_password = '请输入当前密码'
  }
  const username = form.username.trim()
  if (username !== '' && !USERNAME_PATTERN.test(username)) {
    errors.username = '用户名需为 1–64 位字母、数字、点、下划线或短横线'
  }
  if (form.newPassword !== '') {
    if (Array.from(form.newPassword).length < NEW_PASSWORD_MIN) {
      errors.new_password = '新密码至少 12 个字符'
    } else if (new TextEncoder().encode(form.newPassword).length > NEW_PASSWORD_MAX_BYTES) {
      errors.new_password = '新密码不超过 72 字节'
    } else if (form.confirmPassword !== form.newPassword) {
      errors.confirm_password = '两次输入的新密码不一致'
    }
  } else if (form.confirmPassword !== '') {
    errors.confirm_password = '请先填写新密码'
  }
  if (username === '' && form.newPassword === '' && errors.current_password === undefined) {
    errors.username = '用户名和新密码至少修改一项'
  }
  return errors
}

/** hasCredentialErrors 判断校验结果是否包含任何字段错误。 */
export function hasCredentialErrors(errors: CredentialErrors): boolean {
  return Object.keys(errors).length > 0
}

/** credentialFieldError 把后端字段名映射到表单字段。 */
export function credentialFieldError(field: string): CredentialField | null {
  switch (field) {
    case 'current_password':
    case 'username':
    case 'new_password':
      return field
    default:
      return null
  }
}
