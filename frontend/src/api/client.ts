export interface HealthInfo {
  status: string
  version: string
  commit: string
}

export interface AuthStatus {
  initialized: boolean
  authenticated: boolean
}

export class ApiError extends Error {
  readonly status: number

  constructor(status: number, message: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
  }
}

async function messageOf(response: Response): Promise<string> {
  try {
    const body = (await response.json()) as { error?: unknown }
    if (typeof body.error === 'string' && body.error.trim() !== '') {
      return body.error
    }
  } catch {
    // 响应不是 JSON 时使用统一说明
  }
  return '请求失败，请稍后重试'
}

// apiRequest 发起同源请求，失败时抛出带状态码与后端说明的错误。
export async function apiRequest(path: string, init?: RequestInit): Promise<Response> {
  const response = await fetch(path, { credentials: 'same-origin', ...init })
  if (!response.ok) {
    throw new ApiError(response.status, await messageOf(response))
  }
  return response
}

export async function fetchHealth(): Promise<HealthInfo> {
  return (await apiRequest('/api/health')).json() as Promise<HealthInfo>
}

export async function fetchStatus(): Promise<AuthStatus> {
  return (await apiRequest('/api/auth/status')).json() as Promise<AuthStatus>
}

export async function login(username: string, password: string): Promise<void> {
  await apiRequest('/api/auth/login', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username, password }),
  })
}

export async function setupAccount(username: string, password: string, setupToken: string): Promise<void> {
  await apiRequest('/api/auth/setup', {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      'X-Setup-Token': setupToken,
    },
    body: JSON.stringify({ username, password }),
  })
}

export async function logout(): Promise<void> {
  await apiRequest('/api/auth/logout', { method: 'POST' })
}
