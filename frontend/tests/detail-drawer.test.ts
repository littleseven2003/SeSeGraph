import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import assert from 'node:assert/strict'

const source = readFileSync(new URL('../src/components/gallery/DetailDrawer.vue', import.meta.url), 'utf8')

test('媒体清单用固定列宽网格，桌面五列、窄屏两行', () => {
  assert.ok(
    source.includes('sm:grid-cols-[2.75rem_minmax(0,1fr)_5.25rem_3.75rem_3.25rem]'),
    '桌面五列固定宽度，外链与复制不再随元信息长度漂移',
  )
  assert.ok(source.includes('grid-cols-[2.75rem_minmax(0,1fr)]'), '窄屏两列，操作换到第二行')
  assert.ok(source.includes('h-11 w-11'), '缩略图 44px 热区')
})

test('每行前置缩略图只按需回源', () => {
  assert.ok(source.includes(':data-thumb-order="image.sort_order"'), '缩略图位带序号供观察器识别')
  assert.ok(source.includes('loading="lazy"'), '图片懒加载')
  assert.ok(source.includes('IntersectionObserver'), '只观察可视区附近的缩略图位')
  assert.ok(source.includes('pickEagerOrders('), '自动加载只挑可自动加载的项')
  assert.ok(source.includes('requestThumb(order, true)'), '悬停与聚焦时按需取图')
  assert.equal(source.includes('/i/'), false, '缩略图不能走公开网关')
})

test('旧封面下拉框已删除，改为行内设为封面', () => {
  assert.equal(source.includes('<select'), false, '不应再有封面下拉框')
  assert.equal(source.includes('onCoverChange'), false, '旧处理函数要一起删除')
  assert.ok(source.includes('当前封面'), '当前封面有只读胶囊')
  assert.ok(source.includes('设为封面'), '其余项提供行内按钮')
  assert.ok(source.includes('@click="setCover(image)"'), '点击直接设置封面')
  assert.ok(source.includes(':disabled="coverBusy || !canLoadThumb(image) || detail.pending_delete"'), '忙碌、未入库与待删除时禁用')
  assert.ok(source.includes("cause instanceof ApiError"), '接口错误按状态码给出说明')
  assert.ok(source.includes("'更换封面失败，请稍后重试'"), '失败显示可见提示')
  assert.equal(source.includes('coverError.value = \'\''), true)
})

test('换封面与缩略图都按捕获的帖子编号守卫旧响应', () => {
  const cover = source.slice(source.indexOf('async function setCover'), source.indexOf('onBeforeUnmount(() => {'))
  assert.ok(cover.includes('const postId = detail.id'), '先捕获帖子编号')
  assert.ok(cover.includes('if (props.detail?.id !== postId) return'), '切帖后丢弃旧响应')
  const queue = source.slice(source.indexOf('function ensureThumbs'), source.indexOf('function requestThumb'))
  assert.ok(queue.includes('if (props.detail?.id !== postId || thumbs === null) return'), '缩略图回调同样校验帖子编号')
  assert.ok(source.includes('signal'), '取图支持取消')
})

test('放大气泡用 Teleport 出裁切容器，左侧定位并受安全区约束', () => {
  assert.ok(source.includes('<Teleport to="body">'), '气泡必须挂到 body 之外')
  assert.ok(source.includes('pointer-events-none fixed z-50'), '不挡住列表交互')
  assert.ok(source.includes(':style="previewStyle"'), '按定位结果贴边或居中')
  assert.ok(source.includes("position.side === 'left'"), '桌面贴左侧，其余居中')
  assert.ok(source.includes('bubblePosition('), '定位集中到可测函数')
  assert.ok(source.includes("scroller.value?.addEventListener('scroll', onScrollerScroll"), '滚动时收起')
  assert.ok(source.includes("window.addEventListener('resize', onViewportResize)"), '窗口缩放时重新锚定')
  assert.ok(source.includes('@click="togglePreview(image.sort_order, $event)"'), '触控可点击预览')
  assert.ok(source.includes("teardownThumbs()"), '关闭或切帖释放缩略图与观察器')
})

test('窄屏点按预览在视口内居中，不再返回空', () => {
  assert.equal(source.includes('minViewport'), false, '不再用最小视口宽度直接放弃预览')
  assert.ok(source.includes("if (previewOrder.value !== 0) {\n      closePreview()"), 'Esc 先收气泡再关抽屉')
})

test('缩略图与换封面共用一个原媒体额度，取流与解码都有截止时间', () => {
  assert.ok(source.includes('new MediaBudget(1)'), '整个抽屉只开一条原始媒体通道')
  assert.ok(source.includes('budget: mediaBudget'), '缩略图队列使用同一额度')
  assert.ok(source.includes('mediaBudget.run('), '换封面取图也走同一额度')
  assert.ok(source.includes('MEDIA_FETCH_TIMEOUT_MS'), '取流有截止时间')
  assert.ok(source.includes('COVER_DECODE_TIMEOUT_MS'), '解码有截止时间')
  assert.ok(source.includes('读取原图超时'), '超时给出可重试提示')
})

test('切帖或关闭时取消在途取图并清掉忙碌状态', () => {
  const cover = source.slice(source.indexOf('async function setCover'), source.indexOf('onBeforeUnmount(() => {'))
  assert.ok(cover.includes('controller.abort()'), '切帖与关闭时中断取流')
  assert.ok(cover.includes("if (controller.signal.aborted && !timedOut) return"), '取消不弹错误')
  assert.ok(source.includes('function resetCover()'), '统一复位封面状态')
  assert.ok(source.includes('coverAbort?.abort()'), '复位时取消在途请求')
  assert.ok(source.includes('coverBusy.value = false'), '复位后按钮恢复可用')
})

test('缩略图取失败或超时会结束加载态并显示占位', () => {
  assert.ok(source.includes('onFail: (order) => {'), '队列失败要回调界面')
  assert.ok(source.includes('previewFailed.value = true'), '失败后停止转圈')
  assert.ok(source.includes('无法预览该媒体'), '失败显示占位文案')
})
