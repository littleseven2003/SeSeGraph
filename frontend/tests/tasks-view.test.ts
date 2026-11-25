import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import assert from 'node:assert/strict'

const source = readFileSync(new URL('../src/views/TasksView.vue', import.meta.url), 'utf8')
// sourceFile 读取仓库内其它源文件，用于核对共享组件的结构。
const sourceFile = (relative: string): string => readFileSync(new URL('../src/' + relative, import.meta.url), 'utf8')

test('任务页使用全站统一顶栏并移除手动刷新', () => {
  assert.ok(source.includes('<AppHeader />'), '改用全站统一顶栏')
  const header = sourceFile('components/AppHeader.vue')
  assert.ok(header.includes('sticky top-0'), '顶栏要吸顶')
  assert.ok(header.includes('max-w-[1120px]'), '顶栏内宽与图集版心一致')
  assert.ok(source.includes('max-w-[960px]'), '主内容收束，避免大屏单列过宽')
  assert.equal(source.includes('每 10 秒刷新一次'), false, '不再展示定时刷新说明')
  assert.equal(source.includes('刷新中…'), false, '不再提供手动刷新按钮')
  assert.ok(source.includes('POLL_MS = 2000'), '可见时按 2 秒轮询')
  assert.ok(source.includes("addEventListener('visibilitychange'"), '隐藏停轮询、恢复立即补拉')
})

test('筛选在窄屏局部横滑且不影响整页', () => {
  assert.ok(source.includes('max-w-full overflow-x-auto'), '筛选条只局部横滑')
  assert.ok(source.includes('min-w-max'), '筛选按钮不换行挤压')
  assert.ok(source.includes(':aria-pressed="filter === item.value"'), '选中态对读屏与键盘可见')
  assert.ok(source.includes('setFilter('), '切换筛选走统一入口')
  // 换筛选必须先回到第一页，再发请求。
  const setter = source.slice(source.indexOf('function setFilter'), source.indexOf('async function control'))
  assert.ok(setter.indexOf('filter.value = next') < setter.indexOf('page.value = 1'))
  assert.ok(setter.indexOf('page.value = 1') < setter.indexOf('void requestLoad(false)'), '回第一页后按新条件加载')
})

test('刷新入口共用一个在途请求', () => {
  assert.ok(source.includes('function requestLoad('), '统一调度函数')
  assert.ok(source.includes('if (inFlight !== null)'), '在途时只记补跑，不叠加请求')
  assert.ok(source.includes("addEventListener('visibilitychange'"), '恢复可见走同一调度')
  assert.ok(source.includes('requestLoad(true)'), '后台轮询用静默刷新')
})

test('控制后的刷新与隐藏、卸载都走同一调度', () => {
  const control = source.slice(source.indexOf('async function control'), source.indexOf('function updatedText'))
  assert.ok(control.includes('await requestLoad(false)'), '暂停、继续、停止后按新状态刷新')
  assert.equal(control.includes('await load()'), false, '不再直调 load 叠加请求')
  const visibility = source.slice(source.indexOf('function onVisibility'), source.indexOf('function setFilter'))
  assert.ok(visibility.includes('dropQueued()'), '隐藏时丢弃排队补跑')
  assert.ok(source.includes('function dropQueued()'), '补跑可以整体清空')
  assert.ok(source.includes('document.visibilityState === \'visible\''), '隐藏期间不再补跑')
  const unmount = source.slice(source.indexOf('onBeforeUnmount('))
  assert.ok(unmount.includes('disposed = true') && unmount.includes('dropQueued()'), '卸载后不再发请求')
})

test('任务列表为可续传任务提供入口', () => {
  assert.ok(source.includes("name: 'workbench', query: { resume: task.id }"), '续传跳转带任务编号')
  assert.ok(source.includes('canContinueUpload(task)'), '只为可续传任务显示')
})

test('筛选后命中变少时回到有效页，不留假空态', () => {
  assert.ok(source.includes('result.items.length === 0 && result.total > 0'), '空页要先判断是否越界')
  assert.ok(source.includes('Math.ceil(result.total / pageSize)'), '越界时回到最后一页')
  assert.ok(source.includes('if (result.page !== page.value) page.value = result.page'), '采用服务端钳制后的页码')
})

test('旧轮询不会覆盖新筛选结果', () => {
  assert.ok(source.includes('const seq = (requestSeq += 1)'), '每次加载取序号')
  assert.ok(source.includes('if (seq !== requestSeq) return'), '过期响应直接丢弃')
  assert.ok(source.includes('fetchTasks(requestedPage, pageSize, requestedFilter)'), '请求带发起时的条件')
  // 慢请求期间切筛选或翻页，旧条件的响应不能改回当前页码。
  assert.ok(source.includes('requestedFilter !== filter.value || requestedPage !== page.value'), '条件变化后整份结果丢弃')
  const loadStart = source.indexOf('async function load(')
  const load = source.slice(loadStart, source.indexOf('// requestLoad', loadStart))
  assert.ok(load.indexOf('requestedPage !== page.value') < load.indexOf('page.value = Math.max'), '先判断再改页码')
})

test('卡片结构、空态与控件热区', () => {
  assert.ok(source.includes('rounded-2xl bg-surface shadow'), '任务卡片用轻卡片')
  assert.ok(source.includes('whitespace-nowrap rounded-md px-2.5 py-1'), '状态徽章单行且为圆角矩形')
  assert.ok(source.includes('sm:flex-nowrap') === false, '窄屏动作区允许换行')
  // 所有操作按钮与分页控件使用 h-11（44px）热区。
  const controls = source.match(/inline-flex h-11/g) ?? []
  assert.ok(controls.length >= 6, `44px 控件数量不足：${controls.length}`)
  assert.ok(source.includes("filtered ? '当前筛选没有任务' : '暂无任务'"), '全空与筛选空分开说明')
  assert.ok(source.includes('查看全部'), '筛选空提供回到全部')
  assert.ok(source.includes('新建图集'), '空态提供新建入口')
})

test('任务页复用同一套四阶段与控制判定', () => {
  assert.ok(source.includes('from \'../utils/tasks.ts\''), '控制规则集中在 utils/tasks.ts')
  assert.ok(source.includes('from \'../utils/stages.ts\''), '四阶段口径集中在 utils/stages.ts')
  assert.ok(source.includes('canRetryStore(task)'), '频道失败走重试入口')
  assert.ok(source.includes('重试存图'), '重试文案与补传分开')
  for (const name of ['badgeOf', 'canPause', 'canResume', 'canRetryStore', 'canStop']) {
    assert.ok(source.includes(name), `缺少 ${name}`)
  }
  for (const name of ['taskStages', 'stageInputOf', 'stageSummary', 'stageOverall']) {
    assert.ok(source.includes(name), `缺少 ${name}`)
  }
  assert.equal(source.includes('statusLabel('), false, '不再用画廊聚合状态冒充单段状态')
})

test('任务默认只有一行，展开后才出现四阶段进度与操作', () => {
  assert.ok(source.includes('<TaskStageBar :stages="stagesOf(task)" />'), '展开区复用四阶段组件')
  assert.ok(source.includes('v-if="isExpanded(task.id)"'), '默认收起')
  assert.ok(source.includes(':aria-expanded="isExpanded(task.id)"'), '展开状态对读屏可见')
  assert.ok(source.includes('toggleExpanded(task.id)'), '整行可点开')
  // 操作按钮放在展开区里，收起时只留一行摘要。
  const row = source.slice(source.indexOf('v-for="task in tasks"'), source.indexOf('</ul>'))
  assert.ok(row.indexOf('isExpanded(task.id)') < row.indexOf('canResume(task)'), '操作区在展开之后')
  assert.ok(row.includes('role="progressbar"'), '收起时用进度轨表达总进度')
  assert.equal(row.includes('{{ overallOf(task) }}%'), false, '进度轨不显示百分号')
  assert.ok(row.includes(':aria-valuenow="overallOf(task)"'), '读屏仍能取得整数百分比')
  assert.ok(source.includes('<TaskStageBar'), '工作台与任务页共用同一组件')
})

test('窄屏更新时间收进可点行内，卡片圆角不被悬停背景溢出', () => {
  assert.ok(source.includes('overflow-hidden rounded-2xl bg-surface'), '卡片裁掉悬停圆角溢出')
  const narrow = source.indexOf('sm:hidden">')
  assert.ok(narrow > 0, '窄屏次行仍在按钮内部')
  const narrowBlock = source.slice(narrow, source.indexOf('</button>', narrow))
  assert.ok(narrowBlock.includes('更新于'), '窄屏次行保留更新时间')
  assert.ok(narrowBlock.includes('role="progressbar"'), '窄屏进度轨与更新时间并排')
  assert.ok(source.includes('sm:block sm:w-24'), '桌面另有一处更宽的进度轨')
  // 顶行右侧提供与画廊对称的新建入口。
  assert.ok(source.includes('bg-brand px-4 text-sm font-medium text-brand-ink'), '新建图集主胶囊')
})
