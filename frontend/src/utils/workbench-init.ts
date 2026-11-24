// 工作台初始化：在开放文件入口前把本次要用的规则确定下来。
import { DEFAULT_META_FILENAMES } from './zip.ts'
import type { ParseMode } from './meta.ts'
import type { UploadMode } from './image.ts'
import type { SettingsView } from '../api/settings.ts'
import type { ServerProgress } from '../api/posts.ts'
import type { TaskSnapshot } from './task-store.ts'
import { discardBlocked, type WorkbenchPhase } from './workbench.ts'
import { UPLOAD_CONCURRENCY_DEFAULT, serverUploadConcurrency } from './upload-pref.ts'

/** WorkbenchDefaults 是导入与校验前必须确定的规则。 */
export interface WorkbenchDefaults {
  metaNames: string[]
  parseMode: ParseMode
  defaultMode: UploadMode
  /** 服务端同时接收上传的上限，派发宽度不会超过它 */
  uploadConcurrency: number
}

/** FALLBACK_DEFAULTS 是设置读取失败时使用的内置规则。 */
export function fallbackDefaults(): WorkbenchDefaults {
  return {
    metaNames: [...DEFAULT_META_FILENAMES],
    parseMode: 'first-line',
    defaultMode: 'photo',
    uploadConcurrency: UPLOAD_CONCURRENCY_DEFAULT,
  }
}

/** defaultsFromSettings 把设置视图转成本次使用的规则，空值回落内置默认。 */
export function defaultsFromSettings(view: SettingsView | null): WorkbenchDefaults {
  if (view === null) return fallbackDefaults()
  const names = view.meta.filenames.filter((name) => typeof name === 'string' && name.trim() !== '')
  return {
    metaNames: names.length > 0 ? names : [...DEFAULT_META_FILENAMES],
    parseMode: view.meta.parse_mode === 'key-value' ? 'key-value' : 'first-line',
    defaultMode: view.upload.default_mode === 'document' ? 'document' : 'photo',
    uploadConcurrency: serverUploadConcurrency(view.upload.concurrency),
  }
}

export interface InitializationInput {
  /** 读取设置页保存的发帖默认值 */
  loadSettings: () => Promise<SettingsView>
  /** 读取续传任务的服务端进度 */
  loadProgress: (postId: number) => Promise<ServerProgress>
  /** 本地保存的续传快照，没有则为 null */
  snapshot: TaskSnapshot | null
}

export interface InitializationResult {
  defaults: WorkbenchDefaults
  /** 续传任务的服务端进度；没有快照或读取失败时为 null */
  progress: ServerProgress | null
  /** 续传任务原来的画质；没有快照或读取失败时为 null */
  resumeMode: UploadMode | null
}

/**
 * loadWorkbenchInitialization 按固定顺序完成初始化：
 * 先读设置（失败回落内置），再读续传进度。函数返回前不改动界面，
 * 调用方等它完成后再开放文件入口，保证导入与校验用的是同一套规则。
 */
export async function loadWorkbenchInitialization(input: InitializationInput): Promise<InitializationResult> {
  let view: SettingsView | null = null
  try {
    view = await input.loadSettings()
  } catch {
    view = null
  }
  const defaults = defaultsFromSettings(view)

  if (input.snapshot === null) {
    return { defaults, progress: null, resumeMode: null }
  }

  try {
    const progress = await input.loadProgress(input.snapshot.postId)
    const resumeMode: UploadMode | null =
      progress.upload_mode === 'document' || progress.upload_mode === 'photo' ? progress.upload_mode : null
    return { defaults, progress, resumeMode }
  } catch {
    return { defaults, progress: null, resumeMode: null }
  }
}

/**
 * canLeaveResume 报告现在能否放弃续传。
 * 初始化完成前不能离开：此时进度还没回来，提前清掉快照会让稍后返回的旧任务重新占回工作台。
 */
export function canLeaveResume(ready: boolean, phase: WorkbenchPhase): boolean {
  if (!ready) return false
  return !discardBlocked(phase)
}
