// 同时上传数的本地偏好：默认 2 路，可在设置页按 1–4 路调整，超出服务端入口限额时会自然受限。
export const UPLOAD_CONCURRENCY_DEFAULT = 2
export const UPLOAD_CONCURRENCY_MIN = 1
export const UPLOAD_CONCURRENCY_MAX = 4

const STORAGE_KEY = 'sesegraph.upload.concurrency'

// clampUploadConcurrency 把任意输入收敛到 1–4 的整数，非法值回落默认。
export function clampUploadConcurrency(value: unknown): number {
  // 空值按未设置处理，落到默认值而不是最小值。
  if (value === null || value === undefined) return UPLOAD_CONCURRENCY_DEFAULT
  if (typeof value === 'string' && value.trim() === '') return UPLOAD_CONCURRENCY_DEFAULT
  const parsed = typeof value === 'number' ? value : Number(value)
  if (!Number.isFinite(parsed)) return UPLOAD_CONCURRENCY_DEFAULT
  return Math.min(UPLOAD_CONCURRENCY_MAX, Math.max(UPLOAD_CONCURRENCY_MIN, Math.round(parsed)))
}

// readUploadConcurrency 读取本地偏好，缺省或越界时回到默认值。
export function readUploadConcurrency(storage: Storage | undefined): number {
  if (storage === undefined) return UPLOAD_CONCURRENCY_DEFAULT
  const raw = storage.getItem(STORAGE_KEY)
  if (raw === null || raw.trim() === '') return UPLOAD_CONCURRENCY_DEFAULT
  return clampUploadConcurrency(raw)
}

// saveUploadConcurrency 持久化偏好并返回收敛后的值。
export function saveUploadConcurrency(storage: Storage | undefined, value: number): number {
  const next = clampUploadConcurrency(value)
  storage?.setItem(STORAGE_KEY, String(next))
  return next
}
