import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import assert from 'node:assert/strict'

function source(file: string): string {
  return readFileSync(new URL(`../src/${file}`, import.meta.url), 'utf8')
}

test('设置页分组锚点与就近字段错误', () => {
  const view = source('views/SettingsView.vue')
  // 原有分区仍在页面内，旧地址的分区锚点经映射表落到新的四个分组。
  for (const id of ['upstream', 'defaults', 'check', 'ops', 'account']) {
    assert.ok(view.includes(`id="${id}"`), `缺少分区 ${id}`)
  }
  for (const id of ['channel', 'media', 'storage', 'appearance']) {
    assert.ok(view.includes(`{ id: '${id}'`), `导航缺少 ${id}`)
  }
  assert.ok(view.includes('LEGACY_ANCHORS'), '旧锚点有映射表')
  assert.ok(view.includes('scroll-mt-36'), '锚点跳转要为固定顶栏留出偏移')
  assert.ok(view.includes('fieldErrorText(FIELD_TELEGRAM_CHANNEL)'), '频道标识错误要就近提示')
  assert.ok(view.includes(':href="\'#\' + item.id"'), '锚点用原生链接，便于键盘与复制')
})

test('登录与初始化共用字标并支持密码显隐', () => {
  const mark = source('components/BrandMark.vue')
  assert.ok(mark.includes("sesegraph-wordmark.svg?raw"), '顶栏与登录共用同一份艺术字标')
  assert.ok(mark.includes('184') && mark.includes('40'), '字标按原始比例缩放，避免拉伸')
  assert.ok(mark.includes('.replace(\'fill="#1B1A17"\', \'fill="currentColor"\')'), 'Graph 跟随文字色，深色下不发光')
  for (const token of ['--color-stage-compress', '--color-stage-upload', '--color-stage-sync', '--color-stage-publish']) {
    assert.ok(mark.includes(token), `跳色字母绑定 ${token}`)
  }
  assert.ok(source('components/AppHeader.vue').includes('SeSe图册'), '全站顶栏保留中文副标')
  const toggle = source('components/PasswordVisibilityToggle.vue')
  for (const file of ['views/LoginView.vue', 'views/SetupView.vue']) {
    const view = source(file)
    assert.ok(view.includes(':type="showPassword ? \'text\' : \'password\'"'), `${file} 密码应可显隐`)
    assert.ok(view.includes('<PasswordVisibilityToggle'), `${file} 显隐交给共用图标组件`)
    assert.ok(view.includes(':shown="showPassword"'), `${file} 图标按钮跟随显隐状态`)
    assert.ok(view.includes('pr-11'), `${file} 输入框为图标让出右侧留白`)
    assert.equal(view.includes("'隐藏' : '显示'"), false, `${file} 不再使用文字切换`)
  }

  assert.ok(toggle.includes('type="button"'), '显隐按钮不能触发提交')
  assert.ok(toggle.includes(':aria-pressed="shown"'), '显隐按钮要暴露按压状态')
  assert.ok(toggle.includes('aria-label'), '显隐按钮要有可访问名称')
  assert.ok(toggle.includes('`隐藏${label}`') && toggle.includes('`显示${label}`'), '名称随状态切换且带字段名')
  assert.ok(toggle.includes('@mousedown.prevent'), '点击图标不夺走输入框焦点')
  assert.ok(toggle.includes('class="h-4 w-4"'), '线性眼睛图标为 16px')
  assert.ok(toggle.includes('aria-hidden="true"'), '图标本身不重复播报')
  assert.ok(toggle.includes('viewBox="0 0 24 24"'), '图标用同一套线条规格')

  const setup = source('views/SetupView.vue')
  assert.ok(setup.includes(':type="showToken ? \'text\' : \'password\'"'), '初始化凭证默认遮蔽且可显隐')
  assert.ok(setup.includes('label="初始化凭证"'), '初始化凭证显隐带字段名')
  assert.ok(setup.includes(':type="showConfirm ? \'text\' : \'password\'"'), '确认密码可独立显隐')
  assert.ok(setup.includes('autocomplete="new-password"'), '初始化页密码交给密码管理器')
})
