import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import {
  BLUR_STORAGE_KEY,
  DEFAULT_SHARE_TEMPLATE,
  buildGalleryQuery,
  formatFileSize,
  formatPublishedAt,
  formatViews,
  formatViewsExact,
  showsViews,
  createLatestGuard,
  focusStep,
  imageShareUrl,
  metadataFromDetail,
  normalizeShareTemplate,
  pageWindow,
  postShareUrl,
  shouldFeatureFirst,
  readShareTemplate,
  resolveRevealTrigger,
  saveShareTemplate,
  shouldRevealOnTap,
  shouldShowBlur,
  toggleActionLabel,
  toggleConfirmText,
  toggleHint,
  updateFailureText,
  validateMetadata,
  readBlurPreference,
  readViewPreference,
  saveViewPreference,
  stepIndex,
  renderShareText,
  saveBlurPreference,
  statusLabel,
} from '../src/utils/gallery.ts'

test('buildGalleryQuery 去掉空关键词并保留多标签', () => {
  const query = buildGalleryQuery({ q: ' 海边 ', tags: ['写真', '', '海边'], page: 2, pageSize: 12 })
  const params = new URLSearchParams(query)
  assert.equal(params.get('q'), '海边')
  assert.deepEqual(params.getAll('tag'), ['写真', '海边'])
  assert.equal(params.get('page'), '2')
  assert.equal(params.get('page_size'), '12')
})

test('buildGalleryQuery 空关键词不下发 q', () => {
  const params = new URLSearchParams(buildGalleryQuery({ q: '   ' }))
  assert.equal(params.has('q'), false)
  assert.equal(params.get('page'), '1')
  assert.equal(params.get('page_size'), '12')
})

test('formatPublishedAt 输出本地时间并对空值返回空串', () => {
  assert.equal(formatPublishedAt(''), '')
  assert.equal(formatPublishedAt('不是时间'), '')
  const formatted = formatPublishedAt('2026-11-21T21:43:42Z')
  assert.match(formatted, /^\d{4}-\d{2}-\d{2} \d{2}:\d{2}$/)
})

test('statusLabel 撤回优先且覆盖发布各态', () => {
  assert.deepEqual(statusLabel('completed', 'published', true), { label: '已撤回', tone: 'error' })
  assert.deepEqual(statusLabel('completed', 'published'), { label: '已发布', tone: 'ok' })
  assert.deepEqual(statusLabel('completed', 'failed'), { label: '发布失败', tone: 'error' })
  assert.deepEqual(statusLabel('completed', 'pending'), { label: '待发布', tone: 'progress' })
  assert.deepEqual(statusLabel('publishing', 'idle'), { label: '存图中', tone: 'progress' })
  assert.deepEqual(statusLabel('completed', 'idle'), { label: '待发布', tone: 'idle' })
  assert.deepEqual(statusLabel('failed', 'idle'), { label: '存图失败', tone: 'error' })
  assert.deepEqual(statusLabel('uploading', 'idle'), { label: '上传中', tone: 'progress' })
})

test('renderShareText 替换全部占位符', () => {
  const text = renderShareText(DEFAULT_SHARE_TEMPLATE, {
    title: '海边写真',
    description: '夏日海滩',
    url: 'https://telegra.ph/Page-1',
    count: 12,
    tags: ['写真', '海边'],
  })
  assert.equal(text, '海边写真 · 12 张图\nhttps://telegra.ph/Page-1\n#写真 #海边')
})

test('renderShareText 保留未知占位符', () => {
  const text = renderShareText('{{title}} / {{unknown}}', {
    title: '标题',
    description: '',
    url: '',
    count: 0,
    tags: [],
  })
  assert.equal(text, '标题 / {{unknown}}')
})

test('postShareUrl 只在发布完成且互链就绪时给出链接', () => {
  const published = {
    publish_state: 'published',
    public: true,
    disabled: false,
    telegraph_urls: ['https://telegra.ph/Page-1'],
  }
  assert.equal(postShareUrl(published), 'https://telegra.ph/Page-1')
  assert.equal(postShareUrl({ ...published, publish_state: 'pending' }), '')
  assert.equal(postShareUrl({ ...published, public: false }), '')
  assert.equal(postShareUrl({ ...published, disabled: true }), '')
  assert.equal(postShareUrl({ ...published, telegraph_urls: [] }), '')
  assert.equal(postShareUrl({ ...published, telegraph_urls: ['  '] }), '')
})

test('imageShareUrl 按公开状态、存图状态与公开地址拼接', () => {
  const post = { public: true, disabled: false }
  const stored = { short_code: 'abc12345', status: 'stored' }
  assert.equal(imageShareUrl(post, stored, 'https://img.example.com/'), 'https://img.example.com/i/abc12345')
  assert.equal(imageShareUrl(post, stored, 'https://img.example.com/base'), 'https://img.example.com/base/i/abc12345')
  assert.equal(imageShareUrl({ ...post, disabled: true }, stored, 'https://img.example.com'), '')
  assert.equal(imageShareUrl({ ...post, public: false }, stored, 'https://img.example.com'), '')
  assert.equal(imageShareUrl(post, { short_code: 'abc12345', status: 'uploaded' }, 'https://img.example.com'), '')
  assert.equal(imageShareUrl(post, { short_code: '', status: 'stored' }, 'https://img.example.com'), '')
  assert.equal(imageShareUrl(post, stored, ''), '')
})

test('createLatestGuard 丢弃迟到的旧响应', async () => {
  const guard = createLatestGuard()
  const first = guard.begin()
  const second = guard.begin()
  assert.equal(guard.isCurrent(first), false)
  assert.equal(guard.isCurrent(second), true)

  // 模拟 A 慢、B 快：B 的结果生效后 A 才返回，A 必须被丢弃。
  const order: string[] = []
  const respond = (token: number, name: string, delay: number): Promise<void> =>
    new Promise((resolve) =>
      setTimeout(() => {
        if (guard.isCurrent(token)) order.push(name)
        resolve()
      }, delay),
    )
  const slow = respond(guard.begin(), 'A', 20)
  const fast = respond(guard.begin(), 'B', 1)
  await Promise.all([slow, fast])
  assert.deepEqual(order, ['B'])
})

test('formatFileSize 覆盖字节档位', () => {
  assert.equal(formatFileSize(0), '0 KB')
  assert.equal(formatFileSize(2048), '2 KB')
  assert.equal(formatFileSize(3 * 1024 * 1024), '3.0 MB')
})

test('模糊偏好默认开启并能持久化关闭', () => {
  const store = new Map<string, string>()
  const storage = {
    getItem: (key: string) => store.get(key) ?? null,
    setItem: (key: string, value: string) => void store.set(key, value),
  }
  assert.equal(readBlurPreference(storage), true)
  saveBlurPreference(storage, false)
  assert.equal(store.get(BLUR_STORAGE_KEY), 'off')
  assert.equal(readBlurPreference(storage), false)
  assert.equal(readBlurPreference(undefined), true)
})

test('formatPublishedAt 容忍空值与缺字段', () => {
  assert.equal(formatPublishedAt(''), '')
  assert.equal(formatPublishedAt(null), '')
  assert.equal(formatPublishedAt(undefined), '')
  assert.equal(formatPublishedAt('不是时间'), '')
  assert.match(formatPublishedAt('2026-11-22T09:18:20Z'), /^2026-11-22 \d{2}:\d{2}$/)
})

test('三处界面只在已发布且同步后展示浏览量，详情提供刷新入口', () => {
  const card = readFileSync(new URL('../src/components/gallery/PostCard.vue', import.meta.url), 'utf8')
  assert.ok(card.includes('showsViews(props.item)'), '卡片按同一条可见性规则展示')
  assert.ok(card.includes('formatViews(props.item.views_count)'), '卡片用紧凑写法')

  const row = readFileSync(new URL('../src/components/gallery/PostRow.vue', import.meta.url), 'utf8')
  assert.ok(row.includes('showsViews(props.item)'), '列表行共用同一条规则')
  assert.ok(row.includes('hidden shrink-0 sm:inline'), '窄屏让出日期与张数，避免压到操作区')
  assert.ok(row.includes('items-center gap-2 overflow-hidden'), '元信息行不外溢到按钮上')

  const drawer = readFileSync(new URL('../src/components/gallery/DetailDrawer.vue', import.meta.url), 'utf8')
  assert.ok(drawer.includes('formatViewsExact(props.detail.views_count)'), '详情展示精确值')
  assert.ok(drawer.includes('尚未统计'), '未同步的已发布帖给出说明')
  assert.ok(drawer.includes('aria-label="刷新浏览量"'), '刷新按钮有可读名称')
  assert.ok(drawer.includes(':disabled="viewsRefreshing"'), '刷新中禁用防连点')
  assert.ok(drawer.includes("emit('refreshViews')"), '按钮交由外层发起刷新')
  assert.ok(drawer.includes('aria-live="polite"'), '刷新反馈可被读屏感知')

  const home = readFileSync(new URL('../src/views/HomeView.vue', import.meta.url), 'utf8')
  assert.ok(home.includes('refreshPostViews(id)'), '刷新走单帖接口')
  assert.ok(home.includes("'暂未更新'"), '失败后的冷却命中不谎称已更新')
  assert.ok(home.includes('detail.value?.id === id'), '切帖后旧响应不回写')
  assert.ok(home.includes('views_count: result.views_count'), '成功后回写详情')
})

test('浏览量按千进位压缩，精确值保留千分位', () => {
  assert.equal(formatViews(0), '0')
  assert.equal(formatViews(428), '428')
  assert.equal(formatViews(999), '999')
  assert.equal(formatViews(1000), '1k')
  assert.equal(formatViews(1234), '1.2k')
  assert.equal(formatViews(12345), '12.3k')
  assert.equal(formatViews(999999), '999.9k')
  assert.equal(formatViews(1_000_000), '1M')
  assert.equal(formatViews(1_234_567), '1.2M')
  assert.equal(formatViews(-5), '0')

  assert.equal(formatViewsExact(0), '0')
  assert.equal(formatViewsExact(1234), '1,234')
  assert.equal(formatViewsExact(1_234_567), '1,234,567')
})

test('只有已发布且同步过的图集展示浏览量', () => {
  const base = { publish_state: 'published', disabled: false, views_updated_at: '2026-11-22T08:00:00Z' }
  assert.equal(showsViews(base), true)
  assert.equal(showsViews({ ...base, views_updated_at: null }), false)
  assert.equal(showsViews({ ...base, publish_state: 'idle' }), false)
  assert.equal(showsViews({ ...base, disabled: true }), false)
  assert.equal(showsViews({ ...base, views_updated_at: '' }), false)
})

test('按激活事件区分鼠标、触屏与键盘三种回遮路径', () => {
  // 鼠标点击：离开封面后才由 2 秒计时回遮。
  assert.equal(resolveRevealTrigger({ pointerType: 'mouse', detail: 1 }), 'pointer')
  assert.equal(resolveRevealTrigger({ detail: 1 }), 'pointer')
  // 触屏点按：沿用固定短时回遮。
  assert.equal(resolveRevealTrigger({ pointerType: 'touch', detail: 1 }), 'touch')
  // 键盘触发的点击 detail 为 0，即使指针停在封面内也走短时回遮。
  assert.equal(resolveRevealTrigger({ pointerType: '', detail: 0 }), 'key')
  assert.equal(resolveRevealTrigger({ detail: 0 }), 'key')
  // 无事件时按鼠标处理，避免误用短时计时。
  assert.equal(resolveRevealTrigger(), 'pointer')

  const source = readFileSync(
    new URL('../src/components/gallery/useCoverReveal.ts', import.meta.url),
    'utf8',
  )
  assert.ok(source.includes('resolveRevealTrigger(event)'), '回遮路径按实际激活事件选择')
  assert.ok(source.includes('scheduleShortRemask'), '键盘与触屏走短时回遮')
  assert.ok(source.includes('pointerRevealed'), '鼠标揭开单独标记，离开即回遮')
})

test('只有指针直接点封面才由离开回遮，行内「打开」保留短时回遮', () => {
  const row = readFileSync(new URL('../src/components/gallery/PostRow.vue', import.meta.url), 'utf8')
  assert.ok(row.includes('@click="onCoverOpen"'), '缩略图点击单独处理')
  assert.ok(row.includes('@click="onRowOpen"'), '行内打开按钮单独处理')
  const start = row.indexOf('function onRowOpen')
  const rowHandler = row.slice(start, row.indexOf('}', start))
  assert.equal(rowHandler.includes('fromCover'), false, '行内按钮不交给离开回遮')

  const hook = readFileSync(new URL('../src/components/gallery/useCoverReveal.ts', import.meta.url), 'utf8')
  assert.ok(hook.includes('options.fromCover === true'), '离开回遮只由封面点按触发')
  assert.ok(hook.includes('scheduleShortRemask()'), '行内按钮走短时回遮')
  assert.equal(hook.includes('@mouseenter'), false, '封面不再需要移入取消计时')
})

test('少量标签时「全部标签」入口常驻，Esc 有焦点可归还', () => {
  const source = readFileSync(new URL('../src/components/gallery/TagFilter.vue', import.meta.url), 'utf8')
  assert.equal(source.includes('v-if="hiddenCount > 0"'), false, '入口不得随隐藏数量卸载')
  assert.ok(source.includes('ref="trigger"'), '面板触发器始终挂载')
  assert.ok(source.includes('trigger.value?.focus()'), '关闭面板归还焦点')
})

test('鼠标离开封面立即回遮，遮罩切换不做渐变', () => {
  const hook = readFileSync(new URL('../src/components/gallery/useCoverReveal.ts', import.meta.url), 'utf8')
  assert.ok(hook.includes('function onCoverLeave'), '封面离开事件仍在')
  assert.ok(hook.includes('pointerRevealed = false'), '离开后清掉指针揭开标记')
  assert.equal(hook.includes('COVER_REMASK_MS'), false, '不再保留延迟回遮')
  assert.equal(hook.includes('scheduleLeaveRemask'), false, '不再有延迟计时')

  const card = readFileSync(new URL('../src/components/gallery/PostCard.vue', import.meta.url), 'utf8')
  assert.ok(card.includes('transition-[transform]'), '封面只对缩放做过渡')
  assert.equal(/:hover\) img/.test(card), false)
})

test('卡片标题用原生按钮打开详情且不揭开封面', () => {
  const card = readFileSync(new URL('../src/components/gallery/PostCard.vue', import.meta.url), 'utf8')
  const start = card.indexOf('class="line-clamp-2 w-full')
  const block = card.slice(start - 60, start + 420)
  assert.ok(block.includes('<button'), '标题是原生按钮，Enter 与 Space 都能触发')
  assert.ok(block.includes('@click="emit(\'open\', item.id)"'), '点击直接打开详情')
  assert.equal(block.includes('activate'), false, '标题不揭开封面')
  assert.ok(block.includes('line-clamp-2'), '两行截断落在按钮自身')
  assert.ok(block.includes('w-full'), '按钮不超过卡片宽度')
  assert.ok(block.includes('[overflow-wrap:anywhere]'), '无空格长标题也按字符断行')
  assert.ok(block.includes('focus-visible:underline'), '键盘聚焦可见')

  const drawer = readFileSync(new URL('../src/components/gallery/DetailDrawer.vue', import.meta.url), 'utf8')
  assert.ok(drawer.includes('previousFocus'), '关闭后焦点归还触发元素')
})

test('焦点区不再有手动暂停控件，保留悬停与聚焦暂停', () => {
  const source = readFileSync(new URL('../src/components/gallery/FocusCarousel.vue', import.meta.url), 'utf8')
  assert.equal(source.includes('manualPaused'), false, '手动暂停状态已清除')
  assert.equal(source.includes('暂停自动切换'), false, '暂停按钮已移除')
  assert.ok(source.includes('hoverPaused'), '悬停暂停保留')
  assert.ok(source.includes('focusPaused'), '区内焦点暂停保留')
  assert.ok(source.includes('reducedMotion'), '减少动效规则保留')
})

test('主页布局偏好默认画廊并持久化', () => {
  const store = new Map<string, string>()
  const storage = {
    getItem: (key: string) => store.get(key) ?? null,
    setItem: (key: string, value: string) => void store.set(key, value),
  }
  assert.equal(readViewPreference(storage), 'gallery')
  assert.equal(saveViewPreference(storage, 'list'), undefined)
  assert.equal(readViewPreference(storage), 'list')
  assert.equal(readViewPreference(undefined), 'gallery')
  store.set('sesegraph.gallery.view', '其它值')
  assert.equal(readViewPreference(storage), 'gallery')
})

test('stepIndex 循环切换焦点条目', () => {
  assert.equal(stepIndex(0, 5, 1), 1)
  assert.equal(stepIndex(4, 5, 1), 0)
  assert.equal(stepIndex(0, 5, -1), 4)
  assert.equal(stepIndex(2, 0, 1), 0)
})

test('pageWindow 最多五页并夹在当前页附近', () => {
  assert.deepEqual(pageWindow(1, 12, 0), [1])
  assert.deepEqual(pageWindow(1, 12, 60), [1, 2, 3, 4, 5])
  assert.deepEqual(pageWindow(5, 12, 240), [3, 4, 5, 6, 7])
  assert.deepEqual(pageWindow(20, 12, 240), [16, 17, 18, 19, 20])
})

test('pageWindow 窄屏收窄到三页并保持当前页居中', () => {
  assert.deepEqual(pageWindow(1, 12, 240, 3), [1, 2, 3])
  assert.deepEqual(pageWindow(10, 12, 240, 3), [9, 10, 11])
  assert.deepEqual(pageWindow(20, 12, 240, 3), [18, 19, 20])
  assert.deepEqual(pageWindow(1, 12, 0, 3), [1])
})

test('shouldFeatureFirst 只在未筛选首页且首帖未撤回时启用精选位', () => {
  assert.equal(shouldFeatureFirst({ filtered: false, page: 1, count: 4, firstDisabled: false }), true)
  assert.equal(shouldFeatureFirst({ filtered: true, page: 1, count: 4, firstDisabled: false }), false)
  assert.equal(shouldFeatureFirst({ filtered: false, page: 2, count: 4, firstDisabled: false }), false)
  assert.equal(shouldFeatureFirst({ filtered: false, page: 1, count: 1, firstDisabled: false }), false)
  assert.equal(shouldFeatureFirst({ filtered: false, page: 1, count: 4, firstDisabled: true }), false)
})

test('分享模板默认值、持久化与恢复默认', () => {
  const store = new Map<string, string>()
  const storage = {
    getItem: (key: string) => store.get(key) ?? null,
    setItem: (key: string, value: string) => void store.set(key, value),
  }
  assert.equal(readShareTemplate(storage), DEFAULT_SHARE_TEMPLATE)

  const custom = '{{title}}（{{count}} 张）\n{{url}}'
  assert.equal(saveShareTemplate(storage, custom), custom)
  assert.equal(readShareTemplate(storage), custom)

  assert.equal(normalizeShareTemplate('   '), DEFAULT_SHARE_TEMPLATE)
  assert.equal(saveShareTemplate(storage, '  '), DEFAULT_SHARE_TEMPLATE)
  assert.equal(readShareTemplate(storage), DEFAULT_SHARE_TEMPLATE)

  const broken = {
    getItem: () => {
      throw new Error('blocked')
    },
    setItem: () => {
      throw new Error('blocked')
    },
  }
  assert.equal(readShareTemplate(broken), DEFAULT_SHARE_TEMPLATE)
  assert.equal(saveShareTemplate(broken, custom), custom)
})

test('focusStep 在首尾循环并处理无焦点元素', () => {
  assert.equal(focusStep(0, 3, true), 2)
  assert.equal(focusStep(2, 3, false), 0)
  assert.equal(focusStep(1, 3, false), 2)
  assert.equal(focusStep(1, 3, true), 0)
  assert.equal(focusStep(-1, 3, false), 0)
  assert.equal(focusStep(-1, 3, true), 2)
  assert.equal(focusStep(0, 0, false), -1)
})

test('点按一次揭开、再点打开详情，桌面与触屏一致', () => {
  // 第一次点按：临时去模糊，不打开详情。
  assert.equal(shouldRevealOnTap({ blurred: true, revealed: false }), true)
  assert.equal(shouldShowBlur(true, true), false)

  // 计时到期：revealed 复位，模糊重新生效。
  assert.equal(shouldShowBlur(true, false), true)

  // 已经揭开后再次点按直接打开详情。
  assert.equal(shouldRevealOnTap({ blurred: true, revealed: true }), false)

  // 关闭模糊时不拦截点按。
  assert.equal(shouldRevealOnTap({ blurred: false, revealed: false }), false)
  assert.equal(shouldShowBlur(false, false), false)
})

test('悬停与键盘聚焦都不揭开模糊，只有显式点按解锁', () => {
  const source = readFileSync(new URL('../src/components/gallery/PostCard.vue', import.meta.url), 'utf8')
  assert.equal(source.includes(':hover) img'), false, '悬停不得揭开模糊')
  assert.equal(source.includes(':focus-visible) img'), false, '键盘聚焦不得揭开模糊')
  assert.equal(source.includes(':focus-within'), false, '不应使用 focus-within 触发去模糊')
  assert.ok(source.includes('data-reveal="cover"'), '封面按钮标注揭开口，复制按钮不触发揭开')
  assert.ok(source.includes(":class=\"showBlur ? 'is-blurred' : ''\""), '模糊类挂在卡片上')
  assert.ok(source.includes('点击查看'), '模糊封面悬停给出克制的点击提示')
})

test('metadataFromDetail 填充编辑草稿', () => {
  const draft = metadataFromDetail({ title: '标题', description: '说明', author_name: 'SeSe' })
  assert.deepEqual(draft, { title: '标题', description: '说明', authorName: 'SeSe' })
})

test('validateMetadata 校验长度与空标题', () => {
  assert.equal(validateMetadata({ title: '', description: '', authorName: '' }), '标题不能为空')
  assert.equal(validateMetadata({ title: '   ', description: '', authorName: '' }), '标题不能为空')
  assert.equal(validateMetadata({ title: '标'.repeat(200), description: '', authorName: '' }), '')
  assert.equal(validateMetadata({ title: '标'.repeat(201), description: '', authorName: '' }), '标题不超过 200 字')
  assert.equal(validateMetadata({ title: '标题', description: '说'.repeat(5000), authorName: '' }), '')
  assert.equal(validateMetadata({ title: '标题', description: '说'.repeat(5001), authorName: '' }), '说明不超过 5000 字')
  assert.equal(validateMetadata({ title: '标题', description: '', authorName: '作'.repeat(100) }), '')
  assert.equal(validateMetadata({ title: '标题', description: '', authorName: '作'.repeat(101) }), '作者名不超过 100 字')
})

test('updateFailureText 提示分页变化与其他编辑错误', () => {
  assert.equal(
    updateFailureText(409, '修改后分页数量会变化，请调整说明长度'),
    '修改后分页数量会变化，请调整说明长度；请增减说明长度后重试',
  )
  assert.equal(updateFailureText(409, '帖子正在发布流程中，请稍后再编辑'), '帖子正在发布流程中，请稍后再编辑')
  assert.equal(updateFailureText(401, '未登录'), '登录已失效，请重新登录后再编辑')
  assert.equal(updateFailureText(503, 'Telegraph 未配置'), 'Telegraph 未配置，已发布帖暂不能编辑')
  assert.equal(updateFailureText(502, '更新失败'), '更新 Telegraph 页面失败，本地记录未改动')
  assert.equal(updateFailureText(400, '标题不能为空'), '标题不能为空')
  assert.equal(updateFailureText(500, ''), '保存失败，请稍后重试')
})

test('撤回与恢复的按钮、提示与确认文案', () => {
  assert.equal(toggleActionLabel(false), '撤回')
  assert.equal(toggleActionLabel(true), '恢复访问')
  assert.match(toggleHint(false), /立即返回 404/)
  assert.equal(toggleHint(true).includes('已撤回：'), false, '说明条不再重复状态词，避免与状态胶囊重复')
  assert.match(toggleHint(true), /缓存/)
  assert.match(toggleConfirmText(false), /确认撤回/)
  assert.match(toggleConfirmText(false), /源站短码立即 404/)
  assert.match(toggleConfirmText(false), /已缓存的副本可能仍显示/)
  assert.match(toggleConfirmText(true), /恢复/)
})

test('卡片分享入口只在可分享时出现且复用整帖资格判断', () => {
  const source = readFileSync(new URL('../src/components/gallery/PostCard.vue', import.meta.url), 'utf8')
  assert.ok(source.includes('postShareUrl(props.item)'), '复制入口应复用整帖分享资格判断')
  assert.ok(source.includes('v-if="shareUrl !== \'\'"'), '仅在拿到分享链接时显示复制入口')
  assert.equal(source.includes('navigator.clipboard'), false, '复制走共用工具，不重复实现')
})

test('详情抽屉把复制入口放到正文并给出按钮级反馈', () => {
  const source = readFileSync(new URL('../src/components/gallery/DetailDrawer.vue', import.meta.url), 'utf8')
  assert.ok(source.includes("copy(shareUrl, '链接', 'link')"), '复制整帖链接要带上按钮级反馈标识')
  assert.ok(source.includes("copiedAction === 'link' ? '已复制链接' : '复制链接'"), '复制成功后按钮文字立即反馈')
  assert.equal(source.includes('<footer'), false, '复制入口不再固定在抽屉底部栏')
  const action = source.indexOf('复制链接')
  const pages = source.indexOf('分页链接')
  assert.ok(action >= 0 && action < pages, '复制入口应排在分页链接等次级信息之前')
})

test('打开详情抽屉不产生新的图片请求', () => {
  const source = readFileSync(new URL('../src/components/gallery/DetailDrawer.vue', import.meta.url), 'utf8')
  assert.equal(source.includes('<img'), false, '抽屉不渲染图片，避免打开即回源取图')
  assert.ok(source.includes('imageShareUrl'), '单图信息沿用列表已有的外链判断')
  assert.ok(source.includes('已撤回访问，恢复后可分享整帖'), '撤回态提示恢复后才能分享')
  assert.ok(source.includes('发布完成并生成链接后可分享整帖'), '未发布时提示先发布')
})
