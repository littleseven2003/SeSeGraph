import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import assert from 'node:assert/strict'

function source(file: string): string {
  return readFileSync(new URL(`../src/${file}`, import.meta.url), 'utf8')
}

test('设置页分组锚点与就近字段错误', () => {
  const view = source('views/SettingsView.vue')
  const ids = ['upstream', 'defaults', 'check', 'ops']
  for (const id of ids) {
    assert.ok(view.includes(`id="${id}"`), `缺少分组锚点 ${id}`)
    assert.ok(view.includes(`{ id: '${id}'`), `导航缺少 ${id}`)
  }
  assert.ok(view.includes('scroll-mt-36'), '锚点跳转要为固定顶栏留出偏移')
  assert.ok(view.includes('fieldErrorText(FIELD_TELEGRAM_CHANNEL)'), '频道标识错误要就近提示')
  assert.ok(view.includes(':href="\'#\' + item.id"'), '锚点用原生链接，便于键盘与复制')
})

test('登录与初始化共用字标并支持密码显隐', () => {
  const mark = source('components/BrandMark.vue')
  assert.ok(mark.includes("'/sesegraph-wordmark.svg'"), '顶栏与登录共用完整艺术字标')
  assert.ok(mark.includes('184') && mark.includes('40'), '字标按原始比例缩放，避免拉伸')
  assert.ok(source('components/AppHeader.vue').includes('SeSe图册'), '全站顶栏保留中文副标')
  for (const file of ['views/LoginView.vue', 'views/SetupView.vue']) {
    assert.ok(source(file).includes('SeSe图册'), `${file} 保留中文副标`)
  }
  assert.equal(mark.includes('<svg'), false, '图形不在组件内重复维护')
  const html = readFileSync(new URL('../index.html', import.meta.url), 'utf8')
  assert.ok(html.includes('href="/sesegraph-mark.svg"'), 'favicon 指向同一份品牌源')

  for (const file of ['views/LoginView.vue', 'views/SetupView.vue']) {
    const view = source(file)
    assert.ok(view.includes(':type="showPassword ? \'text\' : \'password\'"'), `${file} 密码应可显隐`)
    assert.ok(view.includes(':aria-pressed="showPassword"'), `${file} 显隐按钮要暴露按压状态`)
    assert.ok(view.includes("type=\"button\""), `${file} 显隐按钮不能触发提交`)
  }

  const setup = source('views/SetupView.vue')
  assert.ok(setup.includes(':type="showToken ? \'text\' : \'password\'"'), '初始化凭证默认遮蔽且可显隐')
  assert.ok(setup.includes('autocomplete="new-password"'), '初始化页密码交给密码管理器')
})
