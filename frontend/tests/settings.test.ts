import assert from 'node:assert/strict'
import test from 'node:test'
import type { SettingsView } from '../src/api/settings.ts'
import {
  changedPatch,
  channelInvalid,
  connectionLabel,
  connectionLatency,
  connectionSummary,
  connectionTone,
  endpointText,
  ENV_MANAGED_HINT,
  hasPatch,
  saveFailureText,
  tokenPlaceholder,
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
    },
    env_managed: [],
    ...overrides,
  }
}

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
  assert.equal(hasPatch(changedPatch(current, { token: '', channelId: '@sese_channel', authorName: 'SeSe' })), false)

  const patch = changedPatch(current, { token: '123:new', channelId: '@other_channel', authorName: 'SeSe' })
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
  const patch = changedPatch(current, { token: '123:new', channelId: '@other', authorName: 'SeSe2' })
  assert.deepEqual(patch, { telegraph: { author_name: 'SeSe2' } })
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
