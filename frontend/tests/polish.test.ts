import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import assert from 'node:assert/strict'

function source(relative: string): string {
  return readFileSync(new URL('../src/' + relative, import.meta.url), 'utf8')
}

test('分页图标收紧，窄屏保留热区', () => {
  const home = source('views/HomeView.vue')
  const tasks = source('views/TasksView.vue')
  for (const [name, view] of [
    ['图集页', home],
    ['任务页', tasks],
  ]) {
    assert.ok(view.includes('h-3.5 w-3.5'), `${name} 翻页箭头为 14px`)
    assert.ok(view.includes('aria-label="上一页"') && view.includes('aria-label="下一页"'), `${name} 图标按钮有可访问名称`)
    assert.ok(view.includes('rounded-lg'), `${name} 分页控件收敛为 8px 圆角`)
    assert.ok(/h-11[^"]*sm:h-8/.test(view.replace(/\n/g, ' ')), `${name} 窄屏保留 44px 热区，桌面收紧`)
  }
  const homeNav = home.slice(home.indexOf('aria-label="分页"'), home.indexOf('</nav>'))
  assert.equal(homeNav.includes('rounded-full'), false, '图集分页不再用胶囊外形')
  assert.ok(homeNav.includes('<span class="hidden sm:inline">上一页</span>'), '窄屏只留箭头，桌面保留文字')
  const tasksNavStart = tasks.indexOf('v-if="pages > 1"')
  const tasksNav = tasks.slice(tasksNavStart, tasks.indexOf('</main>', tasksNavStart))
  assert.equal(tasksNav.includes('rounded-full'), false, '任务分页不再用胶囊外形')
})

test('回到顶部只滚动窗口，不动筛选与页码', () => {
  const back = source('components/BackToTop.vue')
  assert.ok(back.includes('window.scrollY > 360'), '滚过首屏才出现')
  assert.ok(back.includes("addEventListener('scroll', onScroll, { passive: true })"), '监听滚动且不阻塞')
  assert.ok(back.includes("removeEventListener('scroll', onScroll)"), '离页移除监听')
  assert.ok(back.includes('prefers-reduced-motion: reduce'), '尊重减少动效偏好')
  assert.ok(back.includes("behavior: reduce ? 'auto' : 'smooth'"), '减少动效时直接跳转')
  assert.ok(back.includes('window.scrollTo('), '只改滚动位置')
  assert.ok(back.includes('aria-label="回到顶部"'), '图标按钮有可访问名称')
  assert.ok(back.includes('fixed right-4'), '固定在右侧')
  assert.ok(back.includes('liftOnNarrow'), '工作台窄屏可避开吸底栏')
  assert.equal(back.includes('fetch('), false, '回顶不重新拉取数据')
  assert.equal(back.includes('router'), false, '回顶不改路由与页码')

  for (const [file, extra] of [
    ['views/HomeView.vue', ''],
    ['views/TasksView.vue', ''],
    ['views/SettingsView.vue', ''],
    ['views/WorkbenchView.vue', ' lift-on-narrow'],
  ]) {
    const view = source(file)
    assert.ok(view.includes(`<BackToTop${extra} />`), `${file} 挂载回顶入口`)
  }
})

test('圆角分三档：卡片 16px、操作 12px、紧凑动作 8px', () => {
  const login = source('views/LoginView.vue')
  assert.ok(login.includes('rounded-xl bg-brand'), '登录提交为 12px 圆角')
  assert.equal(login.includes('rounded-full bg-brand'), false, '主操作不再用胶囊')

  const drawer = source('components/gallery/DetailDrawer.vue')
  assert.ok(drawer.includes('sm:h-8 sm:w-8'), '紧凑图标动作在桌面收紧为 8px 圆角尺寸')
  assert.ok(drawer.includes('h-11 rounded-xl bg-brand'), '抽屉主操作为 12px')

  assert.ok(source('components/gallery/FocusCarousel.vue').includes('rounded-2xl bg-surface'), '卡片容器保持 16px')
  const card = source('components/gallery/PostCard.vue')
  assert.ok(card.includes('whitespace-nowrap rounded-md border px-2 py-1'), '状态徽章收敛为 6px 圆角矩形')

  for (const file of ['views/HomeView.vue', 'views/TasksView.vue']) {
    const view = source(file)
    assert.ok(view.includes('rounded-xl border border-line/70 bg-canvas p-1'), `${file} 分段外框 12px、内项 8px`)
    assert.ok(view.includes('rounded-lg px-'), `${file} 分段内项收敛为 8px`)
  }
  assert.ok(source('utils/../components/tasks/TaskStageBar.vue').includes('overflow-hidden rounded-full bg-canvas'), '进度轨保持圆形')
  assert.ok(source('components/workbench/ModeSwitch.vue').includes('h-6 w-10 items-center rounded-full'), '开关拨片保持圆形')
})

test('面向用户的编号与卷号彻底移除，内部标识与标签保留', () => {
  const forbidden = [
    'views/TasksView.vue',
    'views/WorkbenchView.vue',
    'components/tasks/TaskProgressCard.vue',
    'components/tasks/TaskStageBar.vue',
    'components/gallery/PostCard.vue',
    'components/gallery/PostRow.vue',
  ]
  for (const file of forbidden) {
    const view = source(file)
    for (const pattern of ['task.id }}', 'postId }}', 'resumableHint.postId }}', '{{ volume }}', '#ID', '任务 #']) {
      assert.equal(view.includes(pattern), false, `${file} 不应出现面向用户的编号（${pattern}）`)
    }
    assert.equal(/#\$\{/.test(view), false, `${file} 文案不再拼接 # 编号`)
  }
  assert.ok(source('components/tasks/TaskStageBar.vue').includes('{{ item.index }}.'), '阶段显示为 1. 压缩')
  assert.ok(source('views/TasksView.vue').includes('{{ task.title }}'), '任务行用标题区分条目')
  assert.ok(source('components/gallery/PostCard.vue').includes('>未发布<'), '无发布日期时用文字说明')
  // 内部 ID、深链接与列表键继续可用。
  assert.ok(source('views/TasksView.vue').includes('query: { resume:'), '续传链接仍用内部 ID，入口在任务页')
  assert.ok(source('views/TasksView.vue').includes(':key="task.id"') || source('views/TasksView.vue').includes('task.id'), '列表键仍用内部 ID')
  // 内容标签的 # 语义保留。
  assert.ok(source('components/gallery/TagFilter.vue').includes('#{{ name }}'), '标签仍带 # 前缀')
})

test('顶栏防社死开关收紧尺寸但保留状态与热区', () => {
  const header = source('components/AppHeader.vue')
  assert.ok(header.includes('inline-flex h-11 shrink-0 items-center"'), '外层保留 44px 触控高度')
  assert.ok(header.includes('inline-flex h-8 items-center gap-1.5 rounded-lg border'), '内层描边芯片固定 32px 视觉高度')
  assert.ok(header.includes('h-3.5 w-3.5 shrink-0'), '眼睛图标为 14px')
  assert.ok(header.includes('h-3.5 w-6 shrink-0'), '拨片收窄')
  assert.ok(header.includes('h-2.5 w-2.5 rounded-full bg-white'), '拨片圆点同步缩小')
  assert.ok(header.includes(':aria-pressed="blurEnabled"'), '开关状态对读屏可见')
  assert.ok(header.includes("blurEnabled ? '关闭防社死模糊' : '开启防社死模糊'"), '可访问名称随状态切换')
  assert.ok(header.includes('hidden sm:inline">防社死<'), '桌面短标签')
  assert.ok(header.includes('toggleBlur'), '点击仍切换同一份全局偏好')
  assert.equal(header.includes('防社死模糊</span>'), false, '不再显示长标签')
})

test('四阶段按字标四色取色，槽位与进度条共用同一映射', () => {
  const css = readFileSync(new URL('../src/style.css', import.meta.url), 'utf8')
  for (const [name, hex] of [
    ['compress', '#d04223'],
    ['upload', '#d97706'],
    ['sync', '#0d7a72'],
    ['publish', '#c0266a'],
  ]) {
    assert.ok(css.includes(`--color-stage-${name}: ${hex}`), `${name} 取字标色 ${hex}`)
  }
  assert.ok(css.includes('.stage-slot-idle'), '不适用槽位有斜纹底')
  const bar = source('components/tasks/TaskStageBar.vue')
  for (const name of ['bg-stage-compress', 'bg-stage-upload', 'bg-stage-sync', 'bg-stage-publish']) {
    assert.ok(bar.includes(name), `展开的四段条使用 ${name}`)
  }
  assert.ok(bar.includes("if (item.tone === 'danger') return 'bg-danger'"), '失败段切红')
  assert.ok(bar.includes("item.tone === 'warn' ? '0.55' : '1'"), '暂停段降透明度')
  const view = source('views/TasksView.vue')
  for (const name of ['bg-stage-compress', 'bg-stage-upload', 'bg-stage-sync', 'bg-stage-publish']) {
    assert.ok(view.includes(name), `折叠总轨使用 ${name}`)
  }
  assert.ok(view.includes('stage-slot-idle'), '折叠总轨给不适用槽位斜纹')
  assert.ok(view.includes('aria-valuetext'), '总轨给读屏四段文字说明')
  assert.ok(view.includes('gap-[3px]'), '槽间隔 3px')
  assert.ok(view.includes('first:rounded-l-full last:rounded-r-full'), '总轨首尾圆角、中间小圆角')
})

test('窄屏交互控件保留 44px 热区与 36px 视觉块', () => {
  const header = source('components/AppHeader.vue')
  assert.ok(header.includes('inline-flex h-11 flex-1 items-center justify-center'), '窄屏导航外层 44px 热区')
  assert.ok(header.includes('inline-flex h-9 w-full items-center justify-center rounded-lg'), '内层导航块 36px 与 8px 圆角')
  assert.equal(header.includes('h-10 flex-1 items-center justify-center'), false, '不再用 40px 高度当热区')

  const tasks = source('views/TasksView.vue')
  const filterStart = tasks.indexOf('aria-label="按状态筛选任务"')
  const filter = tasks.slice(filterStart, tasks.indexOf('</div>', tasks.indexOf('TASK_FILTERS', filterStart)))
  assert.ok(filter.includes('inline-flex h-11 shrink-0 items-center -my-1'), '筛选外层 44px 热区且不撑高外框')
  assert.ok(filter.includes('inline-flex h-9 items-center rounded-lg px-3'), '筛选内层视觉块 36px')

  const tags = source('components/gallery/TagFilter.vue')
  assert.ok(tags.includes('ref="trigger"'), '全部标签仍是同一触发按钮')
  assert.ok(tags.includes('inline-flex h-11 shrink-0 items-center"'), '全部标签外层 44px 热区')
  assert.ok(
    tags.includes('inline-flex h-9 items-center gap-1.5 rounded-lg border border-dashed border-line px-2.5'),
    '全部标签内层 36px 视觉块',
  )
  // 标签行内外层同步，避免同一行高低不齐。
  const chipCount = (tags.match(/inline-flex h-11 min-w-0 max-w-full shrink-0 items-center/g) ?? []).length
  assert.ok(chipCount >= 2, `标签行外层热区数量不足：${chipCount}`)
  assert.ok(tags.includes('rounded-lg bg-accent-soft px-2.5 py-1'), '面板已选标签统一 8px 圆角')

  // 图集视图切换、设置锚点、工作台画质切换同样补齐 44px 热区。
  const home = source('views/HomeView.vue')
  const switchCount = (home.match(/inline-flex h-11 items-center -my-1/g) ?? []).length
  assert.ok(switchCount >= 2, `视图切换外层热区不足：${switchCount}`)
  assert.ok(home.includes('inline-flex h-9 items-center gap-1.5 rounded-lg px-2.5'), '视图切换内层 36px')

  const settings = source('views/SettingsView.vue')
  assert.ok(settings.includes('inline-flex h-11 shrink-0 items-center -my-1'), '设置锚点外层 44px 热区')
  assert.ok(settings.includes('inline-flex h-9 items-center rounded-lg border px-3'), '设置锚点内层 36px')

  const mode = source('components/workbench/ModeSwitch.vue')
  assert.equal(mode.includes('h-10'), false, '画质切换不再用 40px 当热区')
  const modeButtons = (mode.match(/inline-flex h-11 items-center -my-1 disabled:opacity-50/g) ?? []).length
  assert.ok(modeButtons === 2, `画质切换两处都要 44px 热区：${modeButtons}`)
  assert.ok(mode.includes('inline-flex h-9 items-center rounded-lg px-3'), '画质切换内层 36px')
})

test('阶段填充与悬停提示共用同一份整数换算', () => {
  const bar = source('components/tasks/TaskStageBar.vue')
  assert.ok(bar.includes('stagePercent(item.ratio)'), '展开的阶段条用共享换算')
  assert.equal(bar.includes('Math.round(item.ratio * 100)'), false, '不再四舍五入成满格')
  const view = source('views/TasksView.vue')
  assert.ok(view.includes('stagePercent(slot.ratio)'), '折叠总轨的宽度与提示同源')
  assert.equal(view.includes('Math.round(slot.ratio * 100)'), false, '199/200 不再显示 100%')
})
