// 断点续传用的本地快照：任务编号与文件清单，关闭网页后仍可继续补传。
const STORAGE_KEY = 'sesegraph.workbench.task'

// 任务快照。
export interface TaskSnapshot {
  postId: number
  imageCount: number
  fileNames: string[]
  createdAt: number
}

// 最小化的存储接口，便于在测试中替换。
export interface StorageLike {
  getItem(key: string): string | null
  setItem(key: string, value: string): void
  removeItem(key: string): void
}

function isSnapshot(value: unknown): value is TaskSnapshot {
  if (typeof value !== 'object' || value === null) return false
  const candidate = value as Partial<TaskSnapshot>
  return (
    typeof candidate.postId === 'number' &&
    candidate.postId > 0 &&
    typeof candidate.imageCount === 'number' &&
    candidate.imageCount > 0 &&
    Array.isArray(candidate.fileNames) &&
    candidate.fileNames.every((name) => typeof name === 'string') &&
    typeof candidate.createdAt === 'number'
  )
}

// loadTask 读取快照，内容不合法时返回 null。
export function loadTask(storage: StorageLike | null): TaskSnapshot | null {
  if (!storage) return null
  let raw: string | null = null
  try {
    raw = storage.getItem(STORAGE_KEY)
  } catch {
    return null
  }
  if (!raw) return null
  try {
    const parsed: unknown = JSON.parse(raw)
    return isSnapshot(parsed) ? parsed : null
  } catch {
    return null
  }
}

// saveTask 写入快照，存储不可用时静默跳过。
export function saveTask(storage: StorageLike | null, snapshot: TaskSnapshot): void {
  if (!storage) return
  try {
    storage.setItem(STORAGE_KEY, JSON.stringify(snapshot))
  } catch {
    // 隐私模式下写入可能被拒绝，此时仅失去续传提示
  }
}

// clearTask 清除快照。
export function clearTask(storage: StorageLike | null): void {
  if (!storage) return
  try {
    storage.removeItem(STORAGE_KEY)
  } catch {
    // 忽略清理失败
  }
}

// browserStorage 返回可用的 localStorage，禁用隐私模式时返回 null。
export function browserStorage(): StorageLike | null {
  try {
    return typeof localStorage === 'undefined' ? null : localStorage
  } catch {
    return null
  }
}
