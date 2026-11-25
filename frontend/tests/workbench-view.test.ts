import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import assert from 'node:assert/strict'

const source = readFileSync(new URL('../src/views/WorkbenchView.vue', import.meta.url), 'utf8')

test('四阶段进度卡不依赖已选图片，刷新后可继续发布或重试', () => {
  assert.ok(source.indexOf('<TaskProgressCard') > 0, '缺少四阶段进度卡')
  // 有图时是左栏第一块，没图时在空态上方单独渲染：两种状态下都能继续发布或重试。
  assert.ok(source.includes('v-if="tiles.length === 0 && serverProgress"'), '无图任务也要能看进度与控制')
  assert.ok(source.includes('已选媒体'), '图片区标题与左栏顺序一致')
  assert.ok(source.includes('v-if="showStages"'), '未开始时进度卡不占高度')
  assert.ok(
    source.includes("serverProgress.value !== null || phase.value !== 'idle' || submitting.value || compressDone.value !== null"),
    '压缩或提交开始后才展示进度卡',
  )
  const leftColumn = source.indexOf('<div class="min-w-0 space-y-4">')
  const card = source.indexOf('<TaskProgressCard', leftColumn)
  const meta = source.indexOf('<MetaPanel', leftColumn)
  const images = source.indexOf('已选媒体', leftColumn)
  assert.ok(card > leftColumn && card < meta, '进度卡排在元信息之前')
  assert.ok(meta < images, '元信息排在图片之前')
})

test('桌面右栏用栏位自身吸顶，长列表滚动时仍可操作', () => {
  assert.ok(source.includes('lg:sticky lg:top-[76px]'), '吸顶要挂在栅格项（aside）上')
  assert.equal(source.includes('<div class="sticky top-[76px] space-y-3">'), false, '内容高度撑不满时，内层吸顶不会生效')
})

test('提交前压缩按模式上限判定，无收益或失败时保留原图', () => {
  // 压缩是否采用产物、失败能否回落都走同一套纯函数，避免界面与提交各判一次。
  assert.ok(source.includes('keepOriginalAfterCompress('), '需要按原图合规性与产物体积决定是否保留原图')
  assert.ok(source.includes('compressFallback('), '压缩失败时按原图是否超限决定保留还是阻断')
  assert.ok(source.includes('restoreSource('), '保留原图要换回 sourceBlob 与原始体积')
  const fallback = source.indexOf('compressFallback(current.sourceSize ?? current.size, limit)')
  assert.ok(fallback > 0, '失败回落按当前策略的上限判定')
  const compressTile = source.slice(source.indexOf('async function compressTile'), source.indexOf('function queueLocalCompress'))
  assert.ok(compressTile.includes('const limit = strategyMode ===') , '上限取自发起时的策略，换模式后重新排队')
  // 提交时先冻结模式与开关，再决定阈值，切到别的模式不会改变本轮判定。
  const frozen = source.indexOf('const compression = await compressForSubmit(frozen.mode, frozen.autoCompress)')
  assert.ok(frozen > 0, '压缩必须使用冻结后的模式与开关')
  assert.ok(source.includes('压缩无收益'), '汇总文案需要说明保留原图的数量')
})


test('小图调度按稳定 ID 去重并在生成前后确认有效性', () => {
  assert.ok(source.includes('new LocalThumbScheduler('), '小图走统一调度器')
  assert.ok(source.includes('isAlive: (id) => tiles.value.some((tile) => tile.id === id)'), '按稳定 ID 判断有效性')
  assert.ok(source.includes('localThumbs.reset()'), '清空后作废在途结果')
  assert.ok(source.includes('localThumbs.dispose()'), '离页后丢弃迟到结果')
  assert.ok(source.includes('disposeArchiveWorker()'), '离页后关闭解压线程')
})

test('新建图集默认干净新建态，只在显式续传时加载任务', () => {
  assert.ok(source.includes("targetID > 0 ? loadTaskFor(storage, targetID) : null"), '默认不自动续传')
  assert.ok(source.includes('resumableHint.value = targetID > 0 ? null : loadTask(storage)'), '非续传时只给轻提示')
  assert.ok(source.includes('前往续传'), '提示里提供续传入口')
  assert.ok(source.includes('不影响直接拖入新素材'), '提示不阻断新建')
  assert.ok(source.includes('readUploadConcurrency(window.localStorage)'), '每轮读取同时上传数')
  assert.ok(
    source.includes('effectiveUploadConcurrency(') && source.includes('rules.value.uploadConcurrency'),
    '派发宽度按服务端上限收敛，不多占排队名额',
  )
})

test('空态不显示数量与清空，续传提示放在拖放区下方', () => {
  assert.equal(source.includes('mb-3 flex items-center justify-end gap-2'), false, '空态顶部不再留孤立工具栏')
  const empty = source.indexOf('v-if="tiles.length === 0" class="mx-auto w-full max-w-[720px]"')
  const hint = source.indexOf('前往续传')
  assert.ok(empty > 0 && hint > empty, '续传提示在空态容器内')
  const dropzone = source.indexOf('<DropZone :disabled="!ready || working || locked"', empty)
  assert.ok(dropzone > empty && dropzone < hint, '提示排在拖放区下方')
  assert.ok(source.includes('与 720px 容器同宽'), '说明同宽口径')
  // 数量与清空收进「已选媒体」标题行，清空是紧凑动作。
  const media = source.indexOf('已选媒体')
  const count = source.indexOf('{{ imageCount }} 项 · {{ formatBytes(totalSize) }}', media)
  const clear = source.indexOf('清空', media)
  assert.ok(count > media && clear > count, '标题行右侧依次是数量与清空')
  assert.ok(source.slice(clear - 260, clear).includes('rounded-xl'), '清空按钮收敛为 12px 圆角的次级动作')
})

test('提交时明确选择发布意图，续传沿用原任务', () => {
  assert.ok(source.includes("publish_intent: intent === 'auto' ? 'auto' : undefined"), '新任务随请求上报发布意图')
  assert.ok(source.includes("async function publish(intent: 'auto' | 'manual' = 'manual')"), '提交入口带意图参数')
  assert.ok(source.includes("return newDraft.value ? '提交并发布' : '提交存图'"), '新草稿默认提交并发布')
  assert.ok(source.includes('仅存图（稍后手动发布）'), '桌面端保留仅存图入口')
  assert.ok(source.includes('仅存图'), '移动端同样可选仅存图')
  assert.ok(source.includes("publish(newDraft ? 'auto' : 'manual')"), '续传不会改写原任务的发布权限')
  assert.ok(source.includes('const newDraft = computed'), '只在没有任务时提供选择')
})

test('导入即压缩，提交等同一队列并复用结果', () => {
  assert.ok(source.includes('new LocalCompressScheduler('), '压缩走独立调度')
  assert.ok(source.includes('queueLocalCompress(tile)'), '导入后排队压缩')
  assert.ok(source.includes('localCompress.reset()'), '清空或换模式后作废在途结果')
  assert.ok(source.includes('localCompress.dispose()'), '离页后不再压缩')
  assert.ok(source.includes('await compressTile(id)'), '压缩结果直接写回卡片')
  // 提交只等导入时排队的同一条串行队列，不再另开循环，避免同一张原图被并行解码。
  const submit = source.slice(source.indexOf('async function compressForSubmit'), source.indexOf('// snapshotFiles'))
  assert.ok(submit.includes('await localCompress.idle()'), '提交前等共享队列清空')
  assert.equal(submit.includes('localCompress.reset()'), false, '提交不再作废后台队列')
  assert.equal(submit.includes('compressImage('), false, '提交不再自己解码原图')
  assert.ok(submit.includes("compressed: tile.compressedFrom !== undefined"), '已压好的项按结果判定')
  assert.ok(submit.includes('localCompress.request(tile.id)'), '仍待压的项只补排队')
  // 换模式与开关会先回到原图，旧结果不会带到新策略。
  const setMode = source.slice(source.indexOf('async function setMode'), source.indexOf('function onAutoCompress'))
  assert.ok(setMode.indexOf('resetToSource()') < setMode.indexOf('localCompress.reset()'), '换模式先丢弃旧结果')
})

test('压缩包逐项交接成 Blob，不再累积展开字节', () => {
  assert.ok(source.includes('const entries: { name: string; blob: Blob }[] = []'), '按项收集 Blob')
  assert.ok(source.includes('(image) => {'), '使用逐项回调')
  assert.ok(source.includes('const next = await prepareTiles(entries, problems)'), '只处理逐项交接的 Blob')
  const zip = source.slice(source.indexOf('async function onZip'), source.indexOf('async function onImages'))
  assert.equal(zip.includes('result.images'), false, '不再读取整包图片数组')
})

test('同页切换续传会重新初始化，旧响应不覆盖新页面', () => {
  assert.ok(source.includes('watch(') && source.includes('route.query.resume'), '监听续传参数变化')
  assert.ok(source.includes('async function applyRoute('), '初始化可重复执行')
  assert.ok(source.includes('const seq = (initSeq += 1)'), '每次初始化取序号')
  assert.ok(source.includes('if (seq !== initSeq) return'), '旧响应直接丢弃')
  assert.ok(source.includes('function resumeIdFromRoute()'), '只认正整数编号')
  assert.ok(source.includes('function resetForRoute()'), '切路由前清掉上一份状态')
  assert.ok(source.includes('localThumbs.reset()') && source.includes('localCompress.reset()'), '旧任务的小图与压缩一起作废')
  assert.ok(source.includes("message.value = '网络异常，未能读取任务进度；续传信息已保留，可稍后刷新重试'"), '网络失败保留快照')
  assert.ok(source.includes('clearTask(storage, saved.postId)'), '只有确认不存在才清快照')
})

test('路由重置只清页面状态，最近任务提示不丢', () => {
  const reset = source.slice(source.indexOf('function resetForRoute()'), source.indexOf('async function applyRoute'))
  assert.equal(reset.includes('resetProgress()'), false, '切路由不能删本机快照')
  assert.ok(reset.includes('resetProgressView()'), '只清页面状态')
  const view = source.slice(source.indexOf('function resetProgressView()'), source.indexOf('// resetProgress 清展示'))
  assert.equal(view.includes('clearTask'), false, '页面重置不碰本机存储')
  assert.ok(view.includes('stopPolling()'), '展示清掉时轮询一并停止')
  // 只有服务端明确 404 才清快照，网络失败仍保留。
  assert.ok(source.includes('init.progressMissing'), '按明确的 404 分支处理')
  assert.ok(source.includes('已不存在，本机续传记录已清除'), '清掉后给出说明')
})

test('发布意图按提交轮次下传，被拦下的提交不会改写上一轮', () => {
  assert.equal(source.includes('let pendingIntent'), false, '不再用模块级变量传递意图')
  assert.ok(source.includes("async function publish(intent: 'auto' | 'manual' = 'manual')"), '意图从入口参数进入')
  const publish = source.slice(source.indexOf('async function publish('), source.indexOf('// runUpload 建任务'))
  const lock = publish.indexOf('submitBlocked(')
  const pass = publish.indexOf('await runUpload(intent, routeRound)')
  assert.ok(lock > 0 && pass > lock, '先过提交锁，再把本轮意图交给上传')
  assert.ok(source.includes("async function runUpload(intent: 'auto' | 'manual', routeRound = initSeq)"), '上传沿用本轮意图')
  assert.ok(source.includes("async function ensureTask(intent: 'auto' | 'manual'"), '建任务沿用本轮意图')
  assert.ok(source.includes("publish_intent: intent === 'auto' ? 'auto' : undefined"), '请求体用局部参数')
})
test('压缩结果绑定发起时的策略版本', () => {
  assert.ok(source.includes('let strategyVersion = 0'), '策略有独立版本')
  const tile = source.slice(source.indexOf('async function compressTile'), source.indexOf('function queueLocalCompress'))
  assert.ok(tile.indexOf('const version = strategyVersion') < tile.indexOf('compressImage('), '发起时固定版本')
  assert.ok(tile.includes('version !== strategyVersion'), '回写前核对版本')
  assert.ok(tile.includes('strategyMode'), '判定与目标都用发起时的策略')
  assert.ok(tile.includes('compressionGate('), '解码前先过门禁')
  assert.ok(tile.includes('await readImageSize('), '尺寸从文件头读')
  const reset = source.slice(source.indexOf('function resetToSource'), source.indexOf('function restoreSource'))
  assert.ok(reset.includes('bumpStrategy()'), '换模式或开关立即升版')
  const clear = source.slice(source.indexOf('function clearAll'), source.indexOf('async function ensureTask'))
  assert.ok(clear.includes('bumpStrategy()'), '清空同样升版')
})

test('清理只作用于当前任务，别的任务续传入口保留', () => {
  const reset = source.slice(source.indexOf('function resetProgress'), source.indexOf('function discardSnapshot'))
  assert.ok(reset.includes('if (abandoned !== undefined && abandoned > 0)'), '没有任务编号时不碰存储')
  assert.ok(
    source.includes('resetProgress(snapshot.value?.postId ?? (postId.value > 0 ? postId.value : undefined))'),
    '清空按当前任务编号清理',
  )
  assert.ok(source.includes('resumableHint.value = loadTask(storage)'), '清空后按剩余快照重读提示')
})
test('切换任务后隔离在途请求', () => {
  const poll = source.slice(source.indexOf('async function refreshProgress'), source.indexOf('// 轮询服务端进度'))
  assert.ok(poll.includes('round !== initSeq || postId.value !== id'), '旧轮询结果按编号与轮次丢弃')
  const start = source.slice(source.indexOf('function startPolling'), source.indexOf('// 管理员确认发布'))
  assert.ok(start.includes('const round = initSeq'), '轮询绑定发起时的路由轮次')
  const cover = source.slice(source.indexOf('function onSelectCover'), source.indexOf('function onSortByName'))
  assert.ok(cover.includes('const target = postId.value'), '换封面锁定发起时的任务编号')
  assert.ok(cover.includes('seq !== coverSelectSeq || round !== initSeq || postId.value !== target'), '生成后仍一致才提交')
  const reset = source.slice(source.indexOf('function resetForRoute'), source.indexOf('async function applyRoute'))
  assert.ok(reset.includes('uploadAbort.abort()'), '切路由中止在途上传')
  const runUploadAt = source.indexOf('async function runUpload')
  const upload = source.slice(runUploadAt, source.indexOf('\n});', runUploadAt))
  assert.ok(upload.includes("runUpload(intent: 'auto' | 'manual', routeRound = initSeq)"), '上传沿用提交轮次')
  assert.ok(upload.includes('if (routeRound !== initSeq) return'), '切走后不再写回状态')
  const publishAt = source.indexOf('async function publish(')
  const publish = source.slice(publishAt, source.indexOf('async function runUpload'))
  assert.ok(publish.indexOf('const routeRound = initSeq') < publish.indexOf('compressForSubmit('), '轮次在等待压缩之前固定')
  assert.ok(source.includes('updateSnapshotStrategy(storage, id, mode.value, autoCompress.value)'), '收尾按任务编号写快照')
})

test('迟到的刷新与上传分支在写入前校验发起时的任务', () => {
  const refresh = source.slice(source.indexOf('async function refreshServerProgress'), source.indexOf('const controlBusy'))
  assert.ok(refresh.includes('round = initSeq'), '刷新带发起时的轮次')
  assert.ok(refresh.indexOf('round !== initSeq || postId.value !== target') < refresh.indexOf('applyServerProgress(progress)'), '应用进度前校验')
  const upload = source.slice(source.indexOf('async function runUpload'), source.indexOf('onBeforeUnmount', source.indexOf('async function runUpload')))
  assert.ok(upload.includes('if (routeRound === initSeq && postId.value === id)'), '失败分支写入前校验')
  assert.ok(upload.includes("await refreshServerProgress(id, routeRound)"), '取消分支按本轮编号刷新')
  const cancel = upload.slice(upload.indexOf('signal.aborted === true'), upload.indexOf('statuses.value = restored'))
  assert.ok(cancel.includes('if (routeRound !== initSeq || postId.value !== id) return'), '取消分支写状态前校验')
})

test('封面生成按代次与卡片绑定，提交只带同一选择的结果', () => {
  assert.ok(source.includes('let coverSeq = 0'), '封面有独立代次')
  assert.ok(source.includes('const coverOwnerId = ref(\'\')'), '记录缩略图所属卡片')
  const refresh = source.slice(source.indexOf('async function refreshCover'), source.indexOf('function applyTiles'))
  assert.ok(refresh.includes('const token = (coverSeq += 1)'), '发起时取代次')
  assert.ok(refresh.includes('token !== coverSeq || coverTile.value?.id !== tile.id'), '回写前校验代次与卡片')
  assert.ok(source.includes('invalidateCover()'), '清空与切路由作废在途封面')
  const ensure = source.slice(source.indexOf('async function ensureTask'), source.indexOf('// 发布：建任务'))
  assert.ok(ensure.includes("cover_thumbnail: coverReady ? coverDataUrl.value : ''"), '只提交同一选择的缩略图')
  assert.ok(ensure.includes('cover_sort_order: coverReady ? chosenOrder : 0'), '缩略图与序号成对提交')
  assert.ok(source.includes('封面缩略图还在生成或生成失败'), '未就绪时提示重试而不是混用旧图')
})

test('同一任务换封面串行，后选覆盖且只有当前选择能回滚', () => {
  const cover = source.slice(source.indexOf('function onSelectCover'), source.indexOf('function onSortByName'))
  assert.ok(source.includes('let coverJob: Promise<void> = Promise.resolve()'), '写请求走串行链')
  assert.ok(cover.includes('coverJob = coverJob'), '每次选择接在链尾')
  assert.ok(cover.includes('if (seq !== coverSelectSeq) return'), '被取代的选择不再发请求')
  assert.ok(cover.includes('seq !== coverSelectSeq || round !== initSeq'), '回写与回滚都核对当前选择')
})

test('换封面成功时记录服务端真实封面，回滚不会退回更早的选择', () => {
  const cover = source.slice(source.indexOf('function onSelectCover'), source.indexOf('function onSortByName'))
  const success = cover.slice(cover.indexOf('await changeCover'), cover.indexOf('message.value = \'封面已更新\''))
  assert.ok(success.includes('cover_sort_order: tile.sortOrder'), '成功后记录服务端封面')
  assert.ok(success.indexOf('cover_sort_order: tile.sortOrder') < success.indexOf('seq !== coverSelectSeq'), '记录先于代次判断，被取代也要记')
})
