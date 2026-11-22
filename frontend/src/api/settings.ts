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

export interface SettingsView {
  public_url: string
  telegram: TelegramSettings
  telegraph: TelegraphSettings
  proxy: ProxySettings
  env_managed: string[]
}

export interface SettingsPatch {
  telegram?: { token?: string; channel_id?: string }
  telegraph?: { author_name?: string }
  proxy?: {
    mode?: string
    url?: string
    telegram_enabled?: boolean
    telegraph_enabled?: boolean
  }
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

// testConnection 触发并行自检，成功与失败都返回两项结果。
export async function testConnection(): Promise<ConnectionResult> {
  return (await settingsRequest('/api/settings/test-connection', { method: 'POST' })).json() as Promise<ConnectionResult>
}
