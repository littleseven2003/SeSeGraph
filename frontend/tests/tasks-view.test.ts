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
  assert.ok(header.includes('max-w-[1240px]'), '顶栏内宽与全站一致')
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
  assert.ok(source.includes('fetchTasks(page.value, pageSize, filter.value)'), '请求必须带当前筛选')
})

test('卡片结构、空态与控件热区', () => {
  assert.ok(source.includes('rounded-2xl bg-surface p-4'), '任务卡片用轻卡片')
  assert.ok(source.includes('whitespace-nowrap rounded-full px-2.5 py-1'), '状态胶囊单行')
  assert.ok(source.includes('grid-cols-1 gap-2 text-xs sm:grid-cols-3'), '390px 三阶段纵向堆叠')
  assert.ok(source.includes('sm:flex-nowrap') === false, '窄屏动作区允许换行')
  // 所有操作按钮与分页控件使用 h-11（44px）热区。
  const controls = source.match(/inline-flex h-11/g) ?? []
  assert.ok(controls.length >= 6, `44px 控件数量不足：${controls.length}`)
  assert.ok(source.includes("filtered ? '当前筛选没有任务' : '暂无任务'"), '全空与筛选空分开说明')
  assert.ok(source.includes('查看全部'), '筛选空提供回到全部')
  assert.ok(source.includes('新建图集'), '空态提供新建入口')
})

test('任务页复用同一套阶段与控制判定', () => {
  assert.ok(source.includes('from \'../utils/tasks.ts\''), '阶段规则集中在 utils/tasks.ts')
  assert.ok(source.includes('canRetryStore(task)'), '频道失败走重试入口')
  assert.ok(source.includes('重试存图'), '重试文案与补传分开')
  for (const name of ['uploadStage', 'channelStage', 'publishStage', 'badgeOf', 'canPause', 'canResume', 'canRetryStore', 'canStop']) {
    assert.ok(source.includes(name), `缺少 ${name}`)
  }
  assert.equal(source.includes('statusLabel('), false, '不再用画廊聚合状态冒充单段状态')
})
