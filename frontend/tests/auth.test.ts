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
  assert.equal(mark.includes('bg-brand'), false, '字标不再使用实心蓝底方块')
  assert.ok(mark.includes('border-line'), '字标改为细线边框')

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
