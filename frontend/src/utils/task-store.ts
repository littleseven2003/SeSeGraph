// 断点续传用的本地快照：任务编号与有序文件清单，关闭网页后仍可对照续传。
const STORAGE_KEY = 'sesegraph.workbench.task'

// 快照中的一个文件，用名称与体积确认身份。
export interface SnapshotFile {
  name: string
  size: number
}

// 任务快照，files 的顺序即服务端 sort_order 顺序。
export interface TaskSnapshot {
  postId: number
  imageCount: number
  files: SnapshotFile[]
  createdAt: number
}

// 最小化的存储接口，便于在测试中替换。
export interface StorageLike {
  getItem(key: string): string | null
  setItem(key: string, value: string): void
  removeItem(key: string): void
}

function fileKey(file: SnapshotFile): string {
  return `${file.name}\u0000${file.size}`
}

function isSnapshotFile(value: unknown): value is SnapshotFile {
  if (typeof value !== 'object' || value === null) return false
  const candidate = value as Partial<SnapshotFile>
  return typeof candidate.name === 'string' && candidate.name !== '' && typeof candidate.size === 'number' && candidate.size >= 0
}

function isSnapshot(value: unknown): value is TaskSnapshot {
  if (typeof value !== 'object' || value === null) return false
  const candidate = value as Partial<TaskSnapshot>
  return (
    typeof candidate.postId === 'number' &&
    candidate.postId > 0 &&
    typeof candidate.imageCount === 'number' &&
    candidate.imageCount > 0 &&
    Array.isArray(candidate.files) &&
    candidate.files.length === candidate.imageCount &&
    candidate.files.every(isSnapshotFile) &&
    typeof candidate.createdAt === 'number'
  )
}

// matchSnapshotFiles 用名称与体积确认本地文件属于上次任务，并给出对应序号。
// 返回 order 时，order[i] 表示快照第 i 个位置对应本地下标；据此可以把列表恢复成原顺序。
export function matchSnapshotFiles(
  expected: readonly SnapshotFile[],
  local: readonly SnapshotFile[],
): { order: number[] } | { error: string } {
  if (local.length !== expected.length) {
    return { error: `本次选择 ${local.length} 张，上次任务有 ${expected.length} 张，无法对应` }
  }

  const positions = new Map<string, number>()
  for (const [index, file] of local.entries()) {
    const key = fileKey(file)
    if (positions.has(key)) {
      return { error: `${file.name} 在本次选择中重复，请确认文件来源` }
    }
    positions.set(key, index)
  }

  const order: number[] = []
  const used = new Set<number>()
  for (const file of expected) {
    const index = positions.get(fileKey(file))
    if (index === undefined) {
      return { error: `本次选择里找不到 ${file.name}，与上次任务不是同一批文件` }
    }
    if (used.has(index)) {
      return { error: `${file.name} 对应到多张图片，请重新选择` }
    }
    used.add(index)
    order.push(index)
  }
  return { order }
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
