// 设置页的展示与表单逻辑，保持为纯函数便于单元测试。
import type { ConnectionResult, ConnectionTarget, SettingsPatch, SettingsView } from '../api/settings.ts'

/** 环境变量托管字段的统一说明。 */
export const ENV_MANAGED_HINT = '由环境变量提供，界面不可修改'

/** 设置页可编辑字段与后端字段名的对应关系。 */
export const FIELD_TELEGRAM_TOKEN = 'telegram.token'
export const FIELD_TELEGRAM_CHANNEL = 'telegram.channel_id'
export const FIELD_TELEGRAPH_AUTHOR = 'telegraph.author_name'

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

/** changedPatch 只提交改动且非环境变量托管的字段。 */
export function changedPatch(
  view: SettingsView,
  form: { token: string; channelId: string; authorName: string },
): SettingsPatch {
  const patch: SettingsPatch = {}
  const telegram: { token?: string; channel_id?: string } = {}
  const telegraph: { author_name?: string } = {}

  if (form.token !== '' && !view.env_managed.includes(FIELD_TELEGRAM_TOKEN)) {
    telegram.token = form.token
  }
  if (form.channelId !== view.telegram.channel_id && !view.env_managed.includes(FIELD_TELEGRAM_CHANNEL)) {
    telegram.channel_id = form.channelId
  }
  if (form.authorName !== view.telegraph.author_name && !view.env_managed.includes(FIELD_TELEGRAPH_AUTHOR)) {
    telegraph.author_name = form.authorName
  }
  if (Object.keys(telegram).length > 0) patch.telegram = telegram
  if (Object.keys(telegraph).length > 0) patch.telegraph = telegraph
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
