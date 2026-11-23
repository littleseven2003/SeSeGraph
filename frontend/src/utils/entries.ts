// 目录拖拽读取：Chrome 的 readEntries 每次只返回一批，必须循环读到空数组为止。
// 这里把批次循环与逐条读取拆成纯函数，便于用假 reader 验证不会漏读。

export interface DirectoryReader {
  readEntries(success: (entries: FileSystemEntry[]) => void, error?: (reason: unknown) => void): void
}

export interface CollectedFiles {
  files: File[]
  /** 读不到内容的条目名称，用于向用户说明而不是静默丢弃 */
  failed: string[]
}

// readAllBatches 持续读取直到空数组；单次失败时重试一次，仍失败则结束。
export async function readAllBatches(reader: DirectoryReader): Promise<FileSystemEntry[]> {
  const all: FileSystemEntry[] = []
  let retried = false
  for (;;) {
    const batch = await new Promise<FileSystemEntry[]>((resolve, reject) => {
      reader.readEntries(
        (entries) => resolve(entries),
        (reason) => reject(reason),
      )
    }).catch(async () => {
      if (retried) return [] as FileSystemEntry[]
      retried = true
      return null
    })
    if (batch === null) continue
    if (batch.length === 0) return all
    all.push(...batch)
    retried = false
  }
}

// readEntryFile 读取单个文件的 Blob，失败时返回 null 由调用方记录名称。
function readEntryFile(entry: FileSystemFileEntry): Promise<File | null> {
  return new Promise((resolve) => {
    entry.file(
      (value) => resolve(value),
      () => resolve(null),
    )
  })
}

// collectEntryFiles 递归收集文件；目录缺失或条目读取失败都记入 failed。
export async function collectEntryFiles(entry: FileSystemEntry, result: CollectedFiles): Promise<void> {
  if (entry.isFile) {
    const file = await readEntryFile(entry as FileSystemFileEntry)
    if (file) result.files.push(file)
    else result.failed.push(entry.name)
    return
  }
  if (!entry.isDirectory) return
  const reader = (entry as FileSystemDirectoryEntry).createReader()
  const children = await readAllBatches(reader).catch(() => [])
  for (const child of children) {
    await collectEntryFiles(child, result)
  }
}
