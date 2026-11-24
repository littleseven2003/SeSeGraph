// 全局防社死开关：四个页面共用同一份状态，切换后立即生效并持久化。
import { ref } from 'vue'
import { readBlurPreference, saveBlurPreference } from './gallery.ts'

// blurEnabled 是所有页面共用的开关，默认开启。
export const blurEnabled = ref(readBlurPreference(typeof window === 'undefined' ? undefined : window.localStorage))

// setBlurEnabled 写入开关状态并持久化，供顶栏与各页面直接调用。
export function setBlurEnabled(enabled: boolean): void {
  blurEnabled.value = enabled
  saveBlurPreference(typeof window === 'undefined' ? undefined : window.localStorage, enabled)
}

// toggleBlur 在顶栏切换开关。
export function toggleBlur(): void {
  setBlurEnabled(!blurEnabled.value)
}
