import { reactive } from 'vue'

/** DraftImage 是一张待编辑的本地图片：原始文件与已生成的小图地址。 */
export interface DraftImage {
  /** key 在同一次导入内唯一，用于列表渲染与重排。 */
  key: string
  file: File
  /** thumbBlob 是导入时生成的 512px 小图；草稿页自己建对象地址并在离页时释放。 */
  thumbBlob: Blob
  name: string
  /** size 是原始字节数，展示与上限提示用。 */
  size: number
}

/** DraftMeta 是草稿页要编辑的元信息，与提交接口字段一一对应。 */
export interface DraftMeta {
  title: string
  description: string
  authorName: string
  uploadMode: 'photo' | 'document'
  publishIntent: '' | 'auto'
  tags: string
  /** coverKey 指向封面图片的 key，空串表示沿用第一张。 */
  coverKey: string
}

/** draftState 是跨页共享的草稿：导入页写入，草稿页读取与编辑，提交成功后清空。 */
export const draftState = reactive<{ images: DraftImage[]; meta: DraftMeta; restorable: boolean }>({
  images: [],
  meta: emptyMeta(),
  restorable: false,
})

function emptyMeta(): DraftMeta {
  return {
    title: '',
    description: '',
    authorName: '',
    uploadMode: 'photo',
    publishIntent: '',
    tags: '',
    coverKey: '',
  }
}

/** startDraft 用导入结果初始化草稿，并保留已填过的元信息。 */
export function startDraft(images: DraftImage[]): void {
  draftState.images = images
  draftState.restorable = images.length > 0
  if (draftState.meta.coverKey !== '' && !images.some((image) => image.key === draftState.meta.coverKey)) {
    draftState.meta.coverKey = ''
  }
}

/** draftSummary 返回草稿的张数与总字节，供页面概要展示。 */
export function draftSummary(): { count: number; bytes: number } {
  let bytes = 0
  for (const image of draftState.images) bytes += image.size
  return { count: draftState.images.length, bytes }
}

/** clearDraft 清空图片与元信息，提交成功或主动放弃时调用。 */
export function clearDraft(): void {
  draftState.images = []
  draftState.meta = emptyMeta()
  draftState.restorable = false
}

/** hasDraft 判断是否还有可编辑的草稿。 */
export function hasDraft(): boolean {
  return draftState.images.length > 0
}
