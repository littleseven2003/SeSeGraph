import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import type { SettingsView, TrafficStats } from '../src/api/settings.ts'
import {
  backupLatestText,
  backupScheduleText,
  changedPatch,
  channelInvalid,
  connectionLabel,
  connectionLatency,
  connectionSummary,
  connectionTone,
  endpointText,
  ENV_MANAGED_HINT,
  formatTrafficBytes,
  hasPatch,
  PATH_MODES,
  pathModeLabel,
  PROXY_MODES,
  proxyCustomNeedsURL,
  proxyModeLabel,
  proxyURLHint,
  proxyURLInvalid,
  proxyURLText,
  saveFailureText,
  tokenPlaceholder,
  trafficState,
  trafficStateText,
  trafficSummary,
  UPLOAD_MODES,
  bytesToGB,
  gbToBytes,
  backupTimeInvalid,
  uploadModeLabel,
} from '../src/utils/settings.ts'

function view(overrides: Partial<SettingsView> = {}): SettingsView {
  return {
    public_url: 'https://sese.example.com',
    telegram: {
      token_set: true,
      token_source: 'stored',
      channel_id: '@sese_channel',
      channel_source: 'stored',
      api_base: '',
      api_base_source: 'env',
    },
    telegraph: {
      token_set: true,
      token_source: 'env',
      author_name: 'SeSe',
      author_source: 'stored',
      api_base: '',
      api_base_source: 'env',
      path_mode: 'title',
      path_mode_source: 'default',
    },
    proxy: {
      mode: 'direct',
      mode_source: 'default',
      url_set: false,
      url_source: 'none',
      telegram_enabled: false,
      telegraph_enabled: false,
    },
    traffic: {
      alert_bytes: 0,
      alert_source: 'default',
      limit_bytes: 0,
      limit_source: 'default',
    },
    backup: {
      enabled: false,
      enabled_source: 'default',
      time: '03:00',
      time_source: 'default',
      keep: 7,
      keep_source: 'default',
    },
    upload: {
      default_mode: 'photo',
      mode_source: 'default',
    },
    media_thumbs: {
      limit_bytes: 2 * 1024 * 1024 * 1024,
      limit_source: 'default',
      used_bytes: 0,
      count: 0,
    },
    meta: {
      filenames: ['info.txt', 'meta.txt', 'readme.txt'],
      parse_mode: 'first-line',
      source: 'default',
    },
    env_managed: [],
    ...overrides,
  }
}

const baseForm = {
  thumbGB: '2',
  alertGB: '',
  limitGB: '',
  backupEnabled: false,
  backupTime: '03:00',
  backupKeep: '7',
  metaFilenames: 'info.txt, meta.txt, readme.txt',
  metaParseMode: 'first-line',
  token: '',
  channelId: '@sese_channel',
  authorName: 'SeSe',
  pathMode: 'title',
  defaultMode: 'photo',
  proxyMode: 'direct',
  proxyUrl: '',
  proxyTelegram: false,
  proxyTelegraph: false,
}

test('缩略图缓存上限按 GB 换算并校验区间', () => {
  const current = view()
  // 未改动时不提交。
  assert.equal(changedPatch(current, { ...baseForm }).media_thumbs, undefined)
  // 调到 1 GiB。
  assert.deepEqual(changedPatch(current, { ...baseForm, thumbGB: '1' }).media_thumbs, {
    limit_bytes: 1024 * 1024 * 1024,
  })
  // 低于 64 MiB 与高于 50 GiB 不接受。
  assert.equal(changedPatch(current, { ...baseForm, thumbGB: '0.05' }).media_thumbs, undefined)
  assert.equal(changedPatch(current, { ...baseForm, thumbGB: '60' }).media_thumbs, undefined)
  // 由环境变量托管时界面改动被忽略。
  const managed = view({ env_managed: ['media_thumbs.limit_bytes'] })
  assert.equal(changedPatch(managed, { ...baseForm, thumbGB: '1' }).media_thumbs, undefined)
})

test('令牌占位文案区分已设置与未设置', () => {
  assert.equal(tokenPlaceholder(true), '已设置，留空不修改')
  assert.equal(tokenPlaceholder(false), '填入 Bot Token')
})

test('上游地址只按来源描述，不回显地址', () => {
  assert.equal(endpointText('default'), '官方默认地址')
  assert.equal(endpointText('env'), '由环境变量提供，地址不回显')
  assert.equal(endpointText('none'), '未配置')
  assert.equal(ENV_MANAGED_HINT, '由环境变量提供，界面不可修改')
})

test('自检状态与耗时格式化', () => {
  assert.equal(connectionLabel('ok'), '可用')
  assert.equal(connectionLabel('failed'), '失败')
  assert.equal(connectionLabel('unconfigured'), '未配置')
  assert.equal(connectionTone('ok'), 'text-accent')
  assert.equal(connectionTone('failed'), 'text-danger')
  assert.equal(connectionLatency({ status: 'ok', latency_ms: 12, detail: '' }), '12 ms')
  assert.equal(connectionLatency({ status: 'unconfigured', latency_ms: 0, detail: '' }), '')
  assert.equal(connectionLatency(undefined), '')
})

test('自检结论汇总两种目标', () => {
  assert.equal(connectionSummary({
    checked_at: '2026-11-22T00:00:00Z',
    telegram: { status: 'ok', latency_ms: 3, detail: '' },
    telegraph: { status: 'ok', latency_ms: 4, detail: '' },
  }), '两项检测均通过')
  assert.equal(connectionSummary({
    checked_at: '2026-11-22T00:00:00Z',
    telegram: { status: 'failed', latency_ms: 3, detail: '' },
    telegraph: { status: 'unconfigured', latency_ms: 0, detail: '' },
  }), '有 1 项检测失败，请检查配置')
  assert.equal(connectionSummary({
    checked_at: '2026-11-22T00:00:00Z',
    telegram: { status: 'unconfigured', latency_ms: 0, detail: '' },
    telegraph: { status: 'unconfigured', latency_ms: 0, detail: '' },
  }), '部分目标尚未配置')
})

test('补丁只提交改动项', () => {
  const current = view()
  assert.equal(hasPatch(changedPatch(current, { ...baseForm })), false)

  const metaPatch = changedPatch(current, { ...baseForm, metaFilenames: 'Info.TXT, notes.md', metaParseMode: 'key-value' })
  assert.deepEqual(metaPatch, { meta: { filenames: ['Info.TXT', 'notes.md'], parse_mode: 'key-value' } })

  const patch = changedPatch(current, { ...baseForm, token: '123:new', channelId: '@other_channel' })
  assert.deepEqual(patch, { telegram: { token: '123:new', channel_id: '@other_channel' } })
  assert.equal(hasPatch(patch), true)
})

test('环境变量托管字段不进入补丁', () => {
  const current = view({
    telegram: {
      token_set: true,
      token_source: 'env',
      channel_id: '@env_channel',
      channel_source: 'env',
      api_base: '',
      api_base_source: 'env',
    },
    env_managed: ['telegram.token', 'telegram.channel_id'],
  })
  const patch = changedPatch(current, { ...baseForm, token: '123:new', channelId: '@other', authorName: 'SeSe2' })
  assert.deepEqual(patch, { telegraph: { author_name: 'SeSe2' } })
})

test('代理配置只提交改动项', () => {
  const current = view()
  assert.equal(
    hasPatch(changedPatch(current, { ...baseForm, proxyMode: 'direct', proxyTelegram: false })),
    false,
  )

  const patch = changedPatch(current, {
    ...baseForm,
    proxyMode: 'custom',
    proxyUrl: 'socks5://ops:pass@10.8.0.9:1080',
    proxyTelegram: true,
  })
  assert.deepEqual(patch, {
    proxy: {
      mode: 'custom',
      url: 'socks5://ops:pass@10.8.0.9:1080',
      telegram_enabled: true,
    },
  })

  // 已有配置的页面只改开关时不下发地址。
  const saved = view({
    proxy: {
      mode: 'system',
      mode_source: 'stored',
      url_set: true,
      url_source: 'stored',
      telegram_enabled: false,
      telegraph_enabled: false,
    },
  })
  assert.deepEqual(
    changedPatch(saved, { ...baseForm, proxyMode: 'system', proxyTelegraph: true }),
    { proxy: { telegraph_enabled: true } },
  )
})

test('代理地址校验与提示', () => {
  assert.equal(proxyURLInvalid(''), false)
  assert.equal(proxyURLInvalid('http://127.0.0.1:7890'), false)
  assert.equal(proxyURLInvalid('https://user:pass@proxy.test:8443'), false)
  assert.equal(proxyURLInvalid('socks5://127.0.0.1:1080'), false)
  assert.equal(proxyURLInvalid('ftp://127.0.0.1:21'), true)
  assert.equal(proxyURLInvalid('127.0.0.1:7890'), true)
  assert.equal(proxyURLInvalid('http://a b'), true)

  assert.equal(proxyURLHint(true, 'custom'), '已设置，留空不修改')
  assert.equal(proxyURLHint(false, 'custom'), 'http://、https:// 或 socks5:// 地址')
  assert.equal(proxyURLHint(false, 'direct'), '仅在自定义模式使用')
  assert.equal(proxyURLText(false, 'none'), '未设置')
  assert.equal(proxyURLText(true, 'stored'), '已保存在服务端，不回显')
  assert.equal(proxyModeLabel('direct'), '直连')
  assert.equal(proxyModeLabel('system'), '跟随系统环境变量')
  assert.equal(proxyModeLabel('custom'), '自定义代理')
  assert.equal(proxyCustomNeedsURL('custom', false, ''), true)
  assert.equal(proxyCustomNeedsURL('custom', true, ''), false)
  assert.equal(proxyCustomNeedsURL('custom', false, 'http://127.0.0.1:7890'), false)
  assert.equal(proxyCustomNeedsURL('direct', false, ''), false)
})

test('频道标识格式校验', () => {
  assert.equal(channelInvalid(''), false)
  assert.equal(channelInvalid('@sese_channel'), false)
  assert.equal(channelInvalid('-1001234567890'), false)
  assert.equal(channelInvalid('not a channel'), true)
  assert.equal(channelInvalid('@x'), true)
})

test('保存失败提示区分登录失效与字段错误', () => {
  assert.equal(saveFailureText(401, '未登录', []), '登录已失效，请重新登录后再修改')
  assert.equal(
    saveFailureText(409, '以下配置由环境变量提供，无法在界面修改', ['telegram.token']),
    '以下配置由环境变量提供，无法在界面修改（telegram.token）',
  )
  assert.equal(saveFailureText(500, '', []), '保存失败，请稍后重试')
})

test('流量统计格式化与状态', () => {
  const base: TrafficStats = {
    month: '2026-11',
    bytes_out: 0,
    request_count: 0,
    alert_sent: false,
    alert_bytes: 3 * 1024 ** 3,
    limit_bytes: 5 * 1024 ** 3,
  }
  assert.equal(formatTrafficBytes(0), '0 B')
  assert.equal(formatTrafficBytes(512), '512 B')
  assert.equal(formatTrafficBytes(2048), '2.00 KB')
  assert.equal(formatTrafficBytes(1.5 * 1024 ** 3), '1.50 GB')

  assert.equal(trafficState(base), 'ok')
  assert.equal(trafficState({ ...base, bytes_out: 3 * 1024 ** 3 }), 'alert')
  assert.equal(trafficState({ ...base, bytes_out: 5 * 1024 ** 3 }), 'over')
  assert.equal(trafficStateText({ ...base, bytes_out: 5 * 1024 ** 3 }), '已达上限，图片网关返回 503')
  assert.equal(trafficSummary({ ...base, request_count: 7 }), '预警 3.00 GB · 上限 5.00 GB · 请求 7 次')
  assert.equal(trafficSummary({ ...base, alert_bytes: 0, limit_bytes: 0 }), '未设置预警 · 未设置上限 · 请求 0 次')
})

test('备份状态文案', () => {
  assert.equal(
    backupScheduleText({ schedule: '每天 03:00', enabled: true, latest: null }),
    '定时备份：每天 03:00',
  )
  assert.equal(
    backupScheduleText({ schedule: '', enabled: false, latest: null }),
    '未启用定时备份（设置 BACKUP_ENABLED=true 后按 BACKUP_CRON 执行）',
  )
  assert.equal(backupLatestText({ schedule: '每天 03:00', enabled: true, latest: null }), '还没有备份')

  const text = backupLatestText({
    schedule: '每天 03:00',
    enabled: true,
    latest: { name: 'sesegraph-20261122-030000.zip', size: 2 * 1024 * 1024, created_at: '2026-11-22T03:00:00Z' },
  })
  assert.match(text, /sesegraph-20261122-030000\.zip · 2\.00 MB · /)
})

test('链接生成方式与默认画质进入补丁', () => {
  const current = view()
  const patch = changedPatch(current, { ...baseForm, pathMode: 'slug', defaultMode: 'document' })
  assert.deepEqual(patch, { telegraph: { path_mode: 'slug' }, upload: { mode: 'document' } })
  assert.equal(pathModeLabel('slug'), '自动生成无中文链接')
  assert.equal(pathModeLabel('title'), '按标题生成')
  assert.equal(uploadModeLabel('photo'), 'photo 省流量')
  assert.equal(uploadModeLabel('document'), 'document 原图')
  assert.equal(PATH_MODES.length, 2)
  assert.equal(UPLOAD_MODES.length, 2)
})

test('链接生成方式与默认画质受环境变量托管时不提交', () => {
  const current = view({ env_managed: ['telegraph.path_mode', 'post.upload_mode'] })
  const patch = changedPatch(current, { ...baseForm, pathMode: 'slug', defaultMode: 'document' })
  assert.equal(hasPatch(patch), false)
})

test('GB 与字节互转以及备份时刻校验', () => {
  assert.equal(gbToBytes(''), 0)
  assert.equal(gbToBytes('1.5'), 1610612736)
  assert.equal(gbToBytes('-1'), null)
  assert.equal(gbToBytes('abc'), null)
  // <input type="number"> 直接给出数字，解析必须照常工作，不能抛错。
  assert.equal(gbToBytes(1.5), 1610612736)
  assert.equal(gbToBytes(0), 0)
  assert.equal(gbToBytes(Number.NaN), null)
  assert.equal(bytesToGB(0), '')
  assert.equal(bytesToGB(1610612736), '1.5')
  assert.equal(backupTimeInvalid('03:00'), false)
  assert.equal(backupTimeInvalid('3:5'), true)
  assert.equal(backupTimeInvalid('24:00'), true)
})

test('流量与备份改动进入补丁', () => {
  const current = view()
  const patch = changedPatch(current, {
    ...baseForm,
    alertGB: '2',
    limitGB: '5',
    backupEnabled: true,
    backupTime: '04:30',
    backupKeep: '10',
  })
  assert.deepEqual(patch.traffic, { alert_bytes: 2147483648, limit_bytes: 5368709120 })
  assert.deepEqual(patch.backup, { keep: 10, enabled: true, time: '04:30' })
})

test('流量与备份受环境变量托管时不提交', () => {
  const current = view({
    env_managed: ['traffic.alert_bytes', 'traffic.limit_bytes', 'backup.enabled', 'backup.time', 'backup.keep'],
  })
  const patch = changedPatch(current, {
    ...baseForm,
    alertGB: '2',
    limitGB: '5',
    backupEnabled: true,
    backupTime: '04:30',
    backupKeep: '10',
  })
  assert.equal(patch.traffic, undefined)
  assert.equal(patch.backup, undefined)
})

test('设置页四组十项常驻渲染，旧锚点静默映射', () => {
  const view = readFileSync(new URL('../src/views/SettingsView.vue', import.meta.url), 'utf8')
  for (const [id, label] of [
    ['telegram', 'Telegram 图床'],
    ['telegraph', 'Telegraph 发布'],
    ['proxy', '网络代理'],
    ['check', '连通性自检'],
    ['meta', '元信息解析'],
    ['upload', '上传默认'],
    ['traffic', '流量看护'],
    ['maintenance', '备份与缓存'],
    ['account', '账号安全'],
    ['theme', '颜色模式'],
  ]) {
    assert.ok(view.includes(`{ id: '${id}', label: '${label}'`), `分组 ${label}`)
  }
  // 全页平铺：所有区块常驻，滚动只改高亮。
  assert.equal(view.includes('v-show="activeSection'), false, '不再互斥隐藏')
  assert.ok(view.includes('scrollIntoView({ behavior: \'smooth\''), '点菜单平滑定位')
  assert.ok(view.includes('function syncActiveFromScroll'), '滚动更新高亮')
  // 点击菜单写历史，后退回到上一个区块；窄屏药丸跟随高亮。
  assert.ok(view.includes("window.history.pushState(null, '', '#' + id)"), '点菜单写入历史记录')
  assert.equal(view.includes('history.replaceState'), false, '不再覆盖当前历史')
  assert.ok(view.includes('function revealActiveNav'), '当前项滚进药丸可视区')
  assert.ok(view.includes('watch(activeSection, revealActiveNav)'), '高亮变化时同步药丸位置')
  // 正文顺序必须与导航一致：Telegraph 在连接与发布内，账号安全在颜色模式之前。
  const body = view.slice(view.indexOf('<template>'))
  const order = ['id="telegram"', 'id="telegraph"', 'id="proxy"', 'id="check"', 'id="meta"', 'id="upload"', 'id="traffic"', 'id="backup"', 'id="cache"', 'id="account"', 'id="theme"']
  let at = -1
  for (const id of order) {
    const next = body.indexOf(id)
    assert.ok(next > at, `正文顺序不符：${id}`)
    at = next
  }
  assert.ok(view.includes('const offset = 152'), '偏移与 scroll-mt-36 对齐，锚点定位后即认作当前项')
  assert.ok(view.includes('function holdJump'), '主动跳转期间按住选中项')
  assert.ok(view.includes('window.setTimeout(releaseJump, 200)'), '滚动静止后再交还高亮')
  assert.ok(view.includes('LEGACY_ANCHORS'), '旧锚点映射表')
  // 四组顺序：连接与发布、上传配置、运维与安全、外观配置。
  const navStart = view.indexOf('const SETTING_NAV')
  const nav = view.slice(navStart, view.indexOf('// LEGACY_ANCHORS'))
  const groups = ["group: '连接与发布'", "group: '上传配置'", "group: '运维与安全'", "group: '外观配置'"]
  let last = -1
  for (const group of groups) {
    const at = nav.indexOf(group)
    assert.ok(at > last, `分组顺序不符：${group}`)
    last = at
  }
  assert.ok(nav.includes("{ id: 'meta', label: '元信息解析', group: '上传配置'"), '元信息解析归入上传配置')
  assert.ok(nav.includes("{ id: 'theme', label: '颜色模式', group: '外观配置'"), '颜色模式归入外观配置')
  for (const [old, target] of [['upstream', 'telegram'], ['channel', 'telegram'], ['network', 'proxy'], ['check', 'check'], ['defaults', 'upload'], ['media', 'upload'], ['ops', 'traffic'], ['storage', 'traffic'], ['account', 'account'], ['security', 'account'], ['appearance', 'theme']]) {
    assert.ok(view.includes(`${old}: '${target}'`), `旧锚点 ${old} 映射到 ${target}`)
  }
  assert.ok(view.includes('aria-label="主题模式"'), '外观与安全内有主题三态')
})

test('设置页挂载即加载，保存范围只由按钮决定', () => {
  const view = readFileSync(new URL('../src/views/SettingsView.vue', import.meta.url), 'utf8')
  // 挂载后必须真的去读设置，否则页面一直停在「正在读取设置…」。
  assert.ok(view.includes('onMounted(() => {'), '有挂载入口')
  assert.ok(view.includes('void load().then('), '挂载后立即读取设置')
  assert.ok(view.includes("window.addEventListener('scroll', onScroll"), '滚动跟随挂载即生效')
  assert.ok(view.includes("window.addEventListener('hashchange', followHash)"), '前进后退跟随挂载即生效')
  // 提交范围来自按钮传入的字段组，与滚动高亮无关。
  assert.equal(view.includes('MENU_PATCH_KEYS'), false, '不再按高亮项挑接口')
  assert.ok(view.includes('async function save(keys: SettingsPatchKey[])'), '保存只接受显式字段组')
  assert.ok(view.includes('const telegramScope = keys.includes(\'telegram\')'), 'Telegram 原子提交按字段组判定')
  assert.ok(view.includes("await save(['telegram'])"), 'Bot 列表与频道同一次提交')
  assert.ok(view.includes('await save()') === false, '不再有匿名字段组的保存入口')
})

test('清除代理与缓存指标互不影响其他卡片草稿', () => {
  const view = readFileSync(new URL('../src/views/SettingsView.vue', import.meta.url), 'utf8')
  const start = view.indexOf('async function clearProxy')
  const body = view.slice(start, start + 900)
  assert.ok(body.includes('const pending = { ...form }'), '先留其他卡片草稿')
  assert.ok(body.includes('restorePendingDraft(pending, { proxy: { url: \'\' } })'), '只重置代理地址')
  // 「当前上限」显示已生效值，草稿留在输入框里。
  assert.ok(view.includes('formatTrafficBytes(view?.media_thumbs.limit_bytes ?? 0)'), '当前上限取已生效值')
})

