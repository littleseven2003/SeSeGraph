import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  COMPRESS_MAX_DIMENSION_SUM,
  MAX_VIDEO_BYTES,
  checkMediaFile,
  compressionSummary,
  fitScale,
  formatDuration,
  limitForMedia,
  mediaKind,
  needsCompression,
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
