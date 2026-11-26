// 设置接口客户端：读取生效配置、保存修改与并行连通性自检。
import { ApiError, apiRequest } from './client.ts'

export interface SettingsGroup {
  token_set: boolean
  token_source: string
  api_base: string
  api_base_source: string
}

export interface TelegramSettings extends SettingsGroup {
  channel_id: string
  channel_source: string
}

export interface TelegraphSettings extends SettingsGroup {
  author_name: string
  author_source: string
  path_mode: string
  path_mode_source: string
}

/** ProxySettings 描述代理配置：模式、地址是否已设置与两项上游的启用开关。 */
export interface ProxySettings {
  mode: string
  mode_source: string
  url_set: boolean
  url_source: string
  telegram_enabled: boolean
  telegraph_enabled: boolean
}

/** TrafficSettings 是月度预警与硬上限，单位字节，0 表示关闭。 */
export interface TrafficSettings {
  alert_bytes: number
  alert_source: string
  limit_bytes: number
  limit_source: string
}

/** MediaThumbsSettings 是逐图缩略图缓存的上限与当前占用。 */
export interface MediaThumbsSettings {
  limit_bytes: number
  limit_source: string
  used_bytes: number
  count: number
}

/** BackupSettings 是自动备份开关、每日时刻与保留份数。 */
export interface BackupSettings {
  enabled: boolean
  enabled_source: string
  time: string
  time_source: string
  keep: number
  keep_source: string
}

/** UploadSettings 是新任务的默认画质及其来源，以及服务端同时接收上传的上限。 */
export interface UploadSettings {
  default_mode: string
  mode_source: string
  /** 服务端上传入口的并发上限，客户端据此收敛派发宽度 */
  concurrency?: number
}

/** MetaSettings 是压缩包说明文件的候选名与默认解析方式。 */
export interface MetaSettings {
  filenames: string[]
  parse_mode: string
  source: string
}

export interface SettingsView {
  public_url: string
  telegram: TelegramSettings
  telegraph: TelegraphSettings
  proxy: ProxySettings
  traffic: TrafficSettings
  backup: BackupSettings
  media_thumbs: MediaThumbsSettings
  upload: UploadSettings
  meta: MetaSettings
  env_managed: string[]
}

export interface SettingsPatch {
  telegram?: { token?: string; channel_id?: string }
  telegraph?: { author_name?: string; path_mode?: string }
  proxy?: {
    mode?: string
    url?: string
    telegram_enabled?: boolean
    telegraph_enabled?: boolean
  }
  upload?: { mode?: string }
  traffic?: { alert_bytes?: number; limit_bytes?: number }
  backup?: { enabled?: boolean; time?: string; keep?: number }
  media_thumbs?: { limit_bytes?: number }
  meta?: {
    filenames?: string[]
    parse_mode?: string
  }
}

/** SettingsPatchKey 是补丁里可单独提交的组名，用于按当前菜单限定保存范围。 */
export type SettingsPatchKey = keyof SettingsPatch

/** TrafficStats 是当月出站流量统计与阈值。 */
export interface TrafficStats {
  month: string
  bytes_out: number
  request_count: number
  alert_sent: boolean
  alert_bytes: number
  limit_bytes: number
}

/** BackupInfo 是最近一份备份的概况。 */
export interface BackupInfo {
  name: string
  size: number
  created_at: string
}

export interface BackupStatus {
  schedule: string
  enabled: boolean
  /** 下一次自动备份时间（服务器时区），未启用时为空 */
  next_run?: string
  /** 服务器时区名称 */
  timezone?: string
  latest: BackupInfo | null
}

export interface BackupResult {
  name: string
  size: number
  created_at: string
  sent: boolean
  includes: string[]
  warning?: string
}

export interface ConnectionTarget {
  status: string
  latency_ms: number
  detail: string
}

export interface ConnectionResult {
  checked_at: string
  telegram: ConnectionTarget
  telegraph: ConnectionTarget
}

// SettingsError 保留后端返回的出错字段，便于界面定位到具体输入项。
export class SettingsError extends ApiError {
  readonly fields: string[]

  constructor(status: number, message: string, fields: string[]) {
    super(status, message)
    this.name = 'SettingsError'
    this.fields = fields
  }
}

// errorResponse 读取错误响应里的说明与字段列表。
function errorResponse(body: unknown): { message: string; fields: string[] } {
  if (typeof body === 'object' && body !== null) {
    const data = body as { error?: unknown; fields?: unknown }
    const message = typeof data.error === 'string' ? data.error : ''
    const fields = Array.isArray(data.fields)
      ? data.fields.filter((field): field is string => typeof field === 'string')
      : []
    return { message, fields }
  }
  return { message: '', fields: [] }
}

async function settingsRequest(path: string, init?: RequestInit): Promise<Response> {
  const response = await apiRequest(path, init)
  return response
}

// fetchSettings 读取当前生效配置与来源标注。
export async function fetchSettings(): Promise<SettingsView> {
  return (await settingsRequest('/api/settings')).json() as Promise<SettingsView>
}

// saveSettings 提交设置修改，成功后返回最新视图。
export async function saveSettings(patch: SettingsPatch): Promise<SettingsView> {
  const response = await fetch('/api/settings', {
    method: 'PUT',
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(patch),
  })
  if (!response.ok) {
    let parsed = { message: '', fields: [] as string[] }
    try {
      parsed = errorResponse(await response.json())
    } catch {
      // 响应不是 JSON 时保留统一说明
    }
    throw new SettingsError(response.status, parsed.message || '保存失败，请稍后重试', parsed.fields)
  }
  return (await response.json()) as SettingsView
}

// fetchTraffic 读取当月出站流量与阈值。
export async function fetchTraffic(): Promise<TrafficStats> {
  return (await settingsRequest('/api/stats/traffic')).json() as Promise<TrafficStats>
}

// fetchBackupStatus 读取定时备份说明与最近一份备份。
export async function fetchBackupStatus(): Promise<BackupStatus> {
  return (await settingsRequest('/api/backup/status')).json() as Promise<BackupStatus>
}

// clearMediaThumbs 清空逐图缩略图缓存，返回删除数与释放字节数。
export async function clearMediaThumbs(): Promise<{ removed: number; freed_bytes: number }> {
  const response = await apiRequest('/api/settings/media-thumbs/clear', { method: 'POST' })
  if (!response.ok) throw new Error('清理缩略图失败')
  return (await response.json()) as { removed: number; freed_bytes: number }
}

// resetPostThumbs 清掉某帖的逐图小图并换新内容版本，供按图集重建使用。
export async function resetPostThumbs(
  postId: number,
): Promise<{ removed: number; freed_bytes: number }> {
  const response = await apiRequest(`/api/posts/${postId}/media-thumbs/reset`, { method: 'POST' })
  if (!response.ok) throw new Error('重置缩略图失败')
  return (await response.json()) as { removed: number; freed_bytes: number }
}

// triggerBackup 立即生成一份备份。
export async function triggerBackup(): Promise<BackupResult> {
  const response = await apiRequest('/api/backup/trigger', { method: 'POST' })
  if (!response.ok) {
    let parsed = { message: '', fields: [] as string[] }
    try {
      parsed = errorResponse(await response.json())
    } catch {
      // 响应不是 JSON 时保留统一说明
    }
    throw new SettingsError(response.status, parsed.message || '备份失败，请稍后重试', parsed.fields)
  }
  return (await response.json()) as BackupResult
}

// testConnection 触发并行自检，成功与失败都返回两项结果。
export async function testConnection(): Promise<ConnectionResult> {
  return (await settingsRequest('/api/settings/test-connection', { method: 'POST' })).json() as Promise<ConnectionResult>
}

/** TelegraphAccountResult 是申请匿名账户后的状态。 */
export interface TelegraphAccountResult {
  configured: boolean
  source: string
  author_name: string
  author_url: string
}

/** AdminCredentialInput 是修改管理员凭据的请求体，密码留空表示不改。 */
export interface AdminCredentialInput {
  current_password: string
  username: string
  new_password: string
  confirm_password: string
}

/** AdminCredentialResult 是修改成功后的账号名与重新登录标记。 */
export interface AdminCredentialResult {
  username: string
  reauthenticate: boolean
}

/** createTelegraphAccount 用当前作者名申请匿名 Telegraph 账户。 */
export async function createTelegraphAccount(authorName: string): Promise<TelegraphAccountResult> {
  const response = await fetch('/api/settings/telegraph/create-account', {
    method: 'POST',
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ author_name: authorName }),
  })
  if (!response.ok) {
    let parsed = { message: '', fields: [] as string[] }
    try {
      parsed = errorResponse(await response.json())
    } catch {
      // 响应不是 JSON 时保留统一说明
    }
    throw new SettingsError(response.status, parsed.message || '申请账户失败，请稍后重试', parsed.fields)
  }
  return (await response.json()) as TelegraphAccountResult
}

/** updateAdminCredentials 校验当前密码后修改账号名或密码。 */
export async function updateAdminCredentials(input: AdminCredentialInput): Promise<AdminCredentialResult> {
  const response = await fetch('/api/settings/admin-credentials', {
    method: 'PUT',
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  })
  if (!response.ok) {
    let parsed = { message: '', fields: [] as string[] }
    try {
      parsed = errorResponse(await response.json())
    } catch {
      // 响应不是 JSON 时保留统一说明
    }
    throw new SettingsError(response.status, parsed.message || '修改账号失败，请稍后重试', parsed.fields)
  }
  return (await response.json()) as AdminCredentialResult
}

/** BotEntry 是池内一个 Bot 的展示信息，令牌只以掩码出现。 */
export interface BotEntry {
  bot_id: number
  alias: string
  priority: number
  status: string
  status_note: string
  masked_token: string
  usable: boolean
}

/** BotsView 是多 Bot 池的读取结果。 */
export interface BotsView {
  bots: BotEntry[]
  pool_enabled: boolean
}

/** BotInput 是保存时提交的一条 Bot，令牌留空表示沿用原值。 */
export interface BotInput {
  bot_id: number
  alias: string
  token: string
  priority: number
  /** status 允许人工停用（paused）或恢复（ok），空串表示沿用服务端状态。 */
  status?: string
}

// fetchBots 读取 Bot 池与健康状态。
export async function fetchBots(): Promise<BotsView> {
  return (await apiRequest('/api/settings/bots')).json() as Promise<BotsView>
}

// saveBots 覆盖保存 Bot 池；未填令牌的条目沿用服务端已有令牌。
export async function saveBots(bots: BotInput[]): Promise<BotsView> {
  const response = await apiRequest('/api/settings/bots', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ bots }),
  })
  return response.json() as Promise<BotsView>
}
