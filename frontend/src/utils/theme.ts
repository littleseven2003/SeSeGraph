// 全站主题：跟随系统、浅色、深色三态，存在本机偏好里，与媒体遮罩开关彼此独立。
import { ref } from 'vue'

/** ThemeMode 是用户选择的三种模式。 */
export type ThemeMode = 'system' | 'light' | 'dark'

/** ResolvedTheme 是实际落到页面上的两种外观。 */
export type ResolvedTheme = 'light' | 'dark'

const STORAGE_KEY = 'sesegraph.theme'

// readThemeMode 只认浅色与深色，其余（含未设置）都按跟随系统处理。
export function readThemeMode(storage: Storage | undefined): ThemeMode {
  try {
    const raw = storage?.getItem(STORAGE_KEY) ?? ''
    return raw === 'light' || raw === 'dark' ? raw : 'system'
  } catch {
    return 'system'
  }
}

// saveThemeMode 写入偏好；隐私模式下写入失败也不影响本次生效。
export function saveThemeMode(storage: Storage | undefined, mode: ThemeMode): void {
  try {
    storage?.setItem(STORAGE_KEY, mode)
  } catch {
    // 存不下就只在本次会话生效
  }
}

// resolveTheme 把模式与系统偏好合成实际外观。
export function resolveTheme(mode: ThemeMode, prefersDark: boolean): ResolvedTheme {
  if (mode === 'dark') return 'dark'
  if (mode === 'light') return 'light'
  return prefersDark ? 'dark' : 'light'
}

// applyTheme 把外观写进根节点，颜色令牌随之切换。
export function applyTheme(theme: ResolvedTheme): void {
  if (typeof document === 'undefined') return
  document.documentElement.dataset.theme = theme
  document.documentElement.style.colorScheme = theme
}

const initialMode = readThemeMode(typeof window === 'undefined' ? undefined : window.localStorage)
const prefersDark =
  typeof window === 'undefined' || typeof window.matchMedia !== 'function'
    ? false
    : window.matchMedia('(prefers-color-scheme: dark)').matches

// themeMode 是顶栏与设置页共用的选择，默认跟随系统。
export const themeMode = ref<ThemeMode>(initialMode)
// resolvedTheme 是当前实际外观，用于图标与按钮文案。
export const resolvedTheme = ref<ResolvedTheme>(resolveTheme(initialMode, prefersDark))

// setThemeMode 切换模式：跟随系统时立刻按系统偏好落一次。
export function setThemeMode(mode: ThemeMode): void {
  themeMode.value = mode
  saveThemeMode(typeof window === 'undefined' ? undefined : window.localStorage, mode)
  syncTheme()
}

// toggleTheme 在顶栏一键切换：当前是深色就回到浅色，否则进深色。
export function toggleTheme(): void {
  setThemeMode(resolvedTheme.value === 'dark' ? 'light' : 'dark')
}

function systemPrefersDark(): boolean {
  return typeof window !== 'undefined' && typeof window.matchMedia === 'function'
    ? window.matchMedia('(prefers-color-scheme: dark)').matches
    : false
}

function syncTheme(): void {
  resolvedTheme.value = resolveTheme(themeMode.value, systemPrefersDark())
  applyTheme(resolvedTheme.value)
}

// startThemeSync 在启动时套用一次，并跟随系统外观变化；返回取消订阅。
export function startThemeSync(): () => void {
  syncTheme()
  if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') return () => {}
  const query = window.matchMedia('(prefers-color-scheme: dark)')
  const onChange = (): void => {
    if (themeMode.value === 'system') syncTheme()
  }
  query.addEventListener('change', onChange)
  return () => query.removeEventListener('change', onChange)
}
