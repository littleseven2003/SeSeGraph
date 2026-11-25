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
