import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import { readThemeMode, resolveTheme, saveThemeMode } from '../src/utils/theme.ts'

function source(relative: string): string {
  return readFileSync(new URL('../src/' + relative, import.meta.url), 'utf8')
}

// 内存 Storage 替身，只实现主题需要的方法。
function memoryStorage(initial: Record<string, string> = {}): Storage {
  const data = new Map(Object.entries(initial))
  return {
    get length() {
      return data.size
    },
    clear: () => data.clear(),
    getItem: (key: string) => data.get(key) ?? null,
    key: (index: number) => [...data.keys()][index] ?? null,
    removeItem: (key: string) => data.delete(key),
    setItem: (key: string, value: string) => void data.set(key, value),
  } as Storage
}

test('主题偏好只认浅色与深色，其余按跟随系统', () => {
  const storage = memoryStorage()
  assert.equal(readThemeMode(storage), 'system')
  saveThemeMode(storage, 'dark')
  assert.equal(readThemeMode(storage), 'dark')
  saveThemeMode(storage, 'light')
  assert.equal(readThemeMode(storage), 'light')
  storage.setItem('sesegraph.theme', 'sepia')
  assert.equal(readThemeMode(storage), 'system', '未知取值回到跟随系统')
  assert.equal(readThemeMode(undefined), 'system', '没有存储时按跟随系统')
})

test('模式与系统偏好合成实际外观', () => {
  assert.equal(resolveTheme('light', true), 'light', '显式浅色忽略系统深色')
  assert.equal(resolveTheme('dark', false), 'dark', '显式深色忽略系统浅色')
  assert.equal(resolveTheme('system', true), 'dark')
  assert.equal(resolveTheme('system', false), 'light')
})

test('首帧在应用脚本前定色，避免闪白', () => {
  const html = readFileSync(new URL('../index.html', import.meta.url), 'utf8')
  const head = html.slice(0, html.indexOf('src="/src/main.ts"'))
  assert.ok(head.includes('sesegraph.theme'), '首帧读取同一份偏好')
  assert.ok(head.includes('prefers-color-scheme: dark'), '跟随系统时按系统偏好')
  assert.ok(head.includes("document.documentElement.dataset.theme"), '首帧把外观写到根节点')
  assert.ok(head.indexOf('sesegraph.theme') < html.indexOf('src="/src/main.ts"'), '定色早于应用入口')
  assert.ok(html.includes('content="light dark"'), '控件配色跟随主题')
})

test('深色令牌覆盖语义色并保留四阶段顺序', () => {
  const css = source('style.css')
  assert.ok(css.includes('html[data-theme="dark"]'), '深色由根节点属性触发')
  for (const [name, value] of [
    ['canvas', '#161513'],
    ['surface', '#1f1d1a'],
    ['surface-raised', '#25221e'],
    ['ink', '#efece6'],
    ['muted', '#a8a197'],
    ['line', '#322e29'],
  ]) {
    assert.ok(css.includes(`--color-${name}: ${value};`), `深色 ${name} 取 ${value}`)
  }
  for (const value of ['#f27960', '#e8b466', '#52c1b5', '#ee76ab']) {
    assert.ok(css.includes(value), `深色四阶段取 ${value}`)
  }
  assert.equal(css.includes('--color-brand: #d04223;\n  --color-brand-strong: #f27960;'), true, '实心主按钮沿用浅色珊瑚配白字')
  assert.ok(css.includes('color-scheme: dark'), '原生控件跟随深色')
  assert.ok(css.includes('--canvas-dot:'), '画布点阵随主题调色')
})

test('顶栏提供主题切换且不改动媒体遮罩', () => {
  const header = source('components/AppHeader.vue')
  assert.ok(header.includes('toggleTheme'), '顶栏一键切换')
  assert.ok(header.includes("resolvedTheme === 'dark' ? '切换到浅色主题' : '切换到深色主题'"), '可访问名称随状态切换')
  assert.ok(header.includes('inline-flex h-11 shrink-0 items-center'), '外层 44px 触控区')
  assert.ok(header.includes('h-8 w-8 items-center justify-center rounded-lg'), '内层 32px 图标块')
  assert.ok(header.includes('toggleBlur'), '媒体遮罩开关保持独立')
})

test('页面不再用硬编码白底，深色下不出现白块', () => {
  for (const name of ['components/gallery/PostCard.vue', 'components/gallery/PostRow.vue', 'components/gallery/FocusCarousel.vue']) {
    const view = source(name)
    assert.equal(view.includes('bg-white'), false, `${name} 改用语义底色`)
  }
  // 拨片圆点是实心白，与底色无关，保留。
  const header = source('components/AppHeader.vue')
  assert.ok(header.includes('rounded-full bg-white'), '开关圆点保持白色')
})
