import assert from 'node:assert/strict'
import { test } from 'node:test'
import { mimeForName } from '../src/utils/image.ts'
import {
  COMPRESS_MAX_DIMENSION_SUM,
  evaluateMedia,
  isAnimatedImage,
  MAX_VIDEO_BYTES,
  checkImportFile,
  checkMediaFile,
  compressionSummary,
  fitScale,
  formatDuration,
  limitForMedia,
  mediaKind,
  needsCompression,
  selectionSummary,
  sizeCompare,
} from '../src/utils/media.ts'

test('checkMediaFile 区分视频、可压缩静态图与超限输入', () => {
  assert.equal(checkMediaFile({ name: 'clip.mp4', size: MAX_VIDEO_BYTES }, 'photo'), null)
  assert.match(checkMediaFile({ name: 'clip.mp4', size: MAX_VIDEO_BYTES + 1 }, 'photo') ?? '', /20MB/)
  // document 模式同样按 20,000,000 字节限制视频。
  assert.match(checkMediaFile({ name: 'clip.mp4', size: MAX_VIDEO_BYTES + 1 }, 'document') ?? '', /20MB/)
  // photo 模式下超 10MB 的静态图交给压缩流程，不直接拒绝。
  assert.equal(checkMediaFile({ name: 'big.jpg', size: 12_000_000 }, 'photo'), null)
  assert.match(checkMediaFile({ name: 'big.jpg', size: 70 * 1024 * 1024 }, 'photo') ?? '', /64MB/)
  assert.match(checkMediaFile({ name: 'big.jpg', size: 25 * 1024 * 1024 }, 'document') ?? '', /20MB/)
})

test('needsCompression 只针对 photo 模式的超大静态图', () => {
  assert.equal(needsCompression('photo', 'a.jpg', 10_000_001), true)
  assert.equal(needsCompression('photo', 'a.jpg', 10_000_000), false)
  assert.equal(needsCompression('document', 'a.jpg', 12_000_000), false)
  assert.equal(needsCompression('photo', 'a.mp4', 12_000_000), false)
})

test('limitForMedia 按视频与图片分别给出上限', () => {
  assert.equal(limitForMedia('photo', 'a.mp4'), MAX_VIDEO_BYTES)
  assert.equal(limitForMedia('document', 'a.mp4'), MAX_VIDEO_BYTES)
  assert.equal(limitForMedia('photo', 'a.jpg'), 10 * 1024 * 1024)
  assert.equal(limitForMedia('document', 'a.jpg'), 20 * 1024 * 1024)
})

test('mediaKind 区分视频与图片', () => {
  assert.equal(mediaKind('clip.mp4', ''), 'video')
  assert.equal(mediaKind('clip', 'video/mp4'), 'video')
  assert.equal(mediaKind('photo.jpg', 'image/jpeg'), 'image')
})

test('fitScale 遵守宽高之和与长宽比约束', () => {
  const scaled = fitScale(12000, 8000)
  assert.ok(scaled !== null)
  assert.ok(scaled.width + scaled.height <= COMPRESS_MAX_DIMENSION_SUM)
  assert.equal(fitScale(40000, 1000), null)
  assert.equal(fitScale(0, 100), null)
})

test('formatDuration 输出 m:ss', () => {
  assert.equal(formatDuration(18), '0:18')
  assert.equal(formatDuration(78.4), '1:18')
  assert.equal(formatDuration(0), '')
  assert.equal(formatDuration(Number.NaN), '')
})

test('压缩汇总与体积对比文案', () => {
  const summary = compressionSummary([
    { blob: new Blob(), width: 1, height: 1, fromSize: 18_400_000, toSize: 6_200_000 },
    { blob: new Blob(), width: 1, height: 1, fromSize: 12_000_000, toSize: 5_000_000 },
  ])
  assert.match(summary, /已自动压缩 2 张/)
  assert.match(summary, /→/)
  assert.equal(compressionSummary([]), '')
  assert.match(sizeCompare(18_400_000, 6_200_000), /^17\.5 MB → 5\.9 MB$/)
})

test('mimeForName 覆盖 MP4', () => {
  assert.equal(mimeForName('clip.mp4'), 'video/mp4')
  assert.equal(mimeForName('photo.jpg'), 'image/jpeg')
})

test('checkMediaFile 恢复图片扩展名白名单', () => {
  for (const name of ['a.txt', 'IMG_0001.HEIC', 'shot.dng', 'pic.avif']) {
    assert.match(checkMediaFile({ name, size: 1000 }, 'photo') ?? '', /不是支持的格式/)
  }
  assert.equal(checkMediaFile({ name: 'a.JPG', size: 1000 }, 'photo'), null)
  assert.equal(checkMediaFile({ name: 'a.webp', size: 1000 }, 'photo'), null)
  // 40 张正常 JPG 全部放行，不会被体积或格式规则拦下。
  const folder = Array.from({ length: 40 }, (_, index) => ({ name: `IMG_${index + 1}.jpg`, size: 8_000_000 }))
  assert.equal(folder.filter((file) => checkMediaFile(file, 'photo') !== null).length, 0)
})

test('selectionSummary 汇总读取、加入与跳过原因', () => {
  const text = selectionSummary({
    read: 42,
    added: 40,
    unread: [],
    skipped: [{ label: '超过 20MB', names: ['big.jpg'] }],
  })
  assert.match(text, /读取 42 个文件：加入 40 项/)
  assert.match(text, /跳过 1 个：超过 20MB 1 个（如 big\.jpg）/)
})

test('40+ 张 JPG 与混合格式的导入与跳过计数', () => {
  const files = [
    ...Array.from({ length: 42 }, (_, index) => ({ name: `IMG_${index + 1}.jpg`, size: 8_000_000 })),
    { name: 'notes.txt', size: 1000 },
    { name: 'IMG_0043.HEIC', size: 5_000_000 },
    { name: 'clip.mp4', size: 20_000_000 },
    { name: 'big.mp4', size: 20_000_001 },
    { name: 'edge.jpg', size: 49_999_999 },
    { name: 'over.jpg', size: 50_000_000 },
  ]
  const accepted: string[] = []
  const skipped: Record<string, number> = {}
  for (const file of files) {
    const problem = checkImportFile(file)
    if (problem === null) accepted.push(file.name)
    else skipped[problem.includes('50MB') ? '超过 50MB' : problem.includes('20MB') ? '超过 20MB' : '格式不支持'] =
      (skipped[problem.includes('50MB') ? '超过 50MB' : problem.includes('20MB') ? '超过 20MB' : '格式不支持'] ?? 0) + 1
  }
  assert.equal(accepted.length, 44)
  assert.equal(accepted.includes('IMG_0043.HEIC'), false)
  assert.equal(accepted.includes('notes.txt'), false)
  assert.equal(accepted.includes('clip.mp4'), true)
  assert.equal(accepted.includes('edge.jpg'), true)
  assert.equal(skipped['超过 50MB'], 1)
  assert.equal(skipped['超过 20MB'], 1)
  assert.equal(skipped['格式不支持'], 2)
})

test('动图先于开关判定', () => {
  // 超限动图无论开关都标为不可压缩，交给界面显示 GIF 超限/动图超限。
  assert.equal(evaluateMedia({ kind: 'image', size: 12_000_000, compressed: false, animated: true }, 'photo', true).state, 'blocked')
  assert.equal(evaluateMedia({ kind: 'image', size: 12_000_000, compressed: false, animated: true }, 'photo', false).state, 'blocked')
  assert.equal(evaluateMedia({ kind: 'image', size: 12_000_000, compressed: false }, 'document', true).label, '满足')
})

test('模式与开关切换后按原图重算', () => {
  const large = { kind: 'image' as const, size: 12_000_000, compressed: false }
  assert.equal(evaluateMedia(large, 'photo', true).state, 'pending')
  assert.equal(evaluateMedia(large, 'photo', false).state, 'blocked')
  assert.equal(evaluateMedia(large, 'document', false).label, '满足')
  assert.equal(evaluateMedia({ ...large, compressed: true }, 'photo', true).state, 'compressed')
})

test('十进制边界与动图角标文案', () => {
  assert.equal(evaluateMedia({ kind: 'image', size: 10_000_000, compressed: false }, 'photo', true).label, '满足')
  assert.equal(evaluateMedia({ kind: 'image', size: 10_000_001, compressed: false }, 'photo', true).state, 'pending')
  assert.equal(evaluateMedia({ kind: 'image', size: 20_000_000, compressed: false }, 'document', true).label, '满足')
  assert.equal(evaluateMedia({ kind: 'image', size: 20_000_001, compressed: false }, 'document', true).state, 'pending')
  assert.equal(evaluateMedia({ kind: 'image', size: 10_000_001, compressed: false, animated: true, animatedKind: 'gif' }, 'photo', true).label, 'GIF 超限')
  assert.equal(evaluateMedia({ kind: 'image', size: 10_000_001, compressed: false, animated: true, animatedKind: 'other' }, 'photo', false).label, '动图超限')
  assert.equal(evaluateMedia({ kind: 'image', size: 10_000_001, compressed: false }, 'photo', false).label, '超 10MB')
  assert.equal(evaluateMedia({ kind: 'image', size: 20_000_001, compressed: false }, 'document', false).label, '超 20MB')
})

function pngChunk(type: string, data: Uint8Array): Uint8Array {
  const chunk = new Uint8Array(12 + data.length)
  const length = data.length
  chunk[0] = (length >>> 24) & 0xff
  chunk[1] = (length >>> 16) & 0xff
  chunk[2] = (length >>> 8) & 0xff
  chunk[3] = length & 0xff
  for (let i = 0; i < 4; i += 1) chunk[4 + i] = type.charCodeAt(i)
  chunk.set(data, 8)
  return chunk
}

function pngOf(...chunks: Uint8Array[]): Blob {
  const signature = new Uint8Array([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a])
  return new Blob([signature, ...chunks] as BlobPart[], { type: 'image/png' })
}

test('PNG 动图按块判定，不误读 tEXt 文本', async () => {
  const textual = new TextEncoder().encode('acTL 只是正文里的字样')
  const staticPng = pngOf(pngChunk('IHDR', new Uint8Array(13)), pngChunk('tEXt', textual), pngChunk('IDAT', new Uint8Array(4)))
  assert.equal(await isAnimatedImage(staticPng, 'a.png'), false)

  const bigText = new Uint8Array(1_100_000)
  const apng = pngOf(pngChunk('IHDR', new Uint8Array(13)), pngChunk('tEXt', bigText), pngChunk('acTL', new Uint8Array(8)), pngChunk('IDAT', new Uint8Array(4)))
  assert.equal(await isAnimatedImage(apng, 'b.png'), true)
})

test('WebP 按 VP8X 动画位判定', async () => {
  const build = (flag: number): Blob => {
    const riff = new Uint8Array(12)
    riff.set([0x52, 0x49, 0x46, 0x46], 0)
    riff.set([0x57, 0x45, 0x42, 0x50], 8)
    const chunk = new Uint8Array(9)
    chunk.set([0x56, 0x50, 0x38, 0x58, 1, 0, 0, 0, flag], 0)
    return new Blob([riff, chunk] as BlobPart[], { type: 'image/webp' })
  }
  assert.equal(await isAnimatedImage(build(0x02), 'a.webp'), true)
  assert.equal(await isAnimatedImage(build(0x00), 'b.webp'), false)
  assert.equal(await isAnimatedImage(new Blob([new Uint8Array(1)]), 'c.gif'), true)
})
