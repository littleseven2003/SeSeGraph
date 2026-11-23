import assert from 'node:assert/strict'
import test from 'node:test'
import type { SettingsView } from '../src/api/settings.ts'
import {
  credentialErrors,
  credentialFieldError,
  hasCredentialErrors,
  telegraphAccountDisabled,
  telegraphAccountHint,
  telegraphAccountState,
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
      token_set: false,
      token_source: 'none',
      author_name: 'SeSe',
      author_source: 'stored',
      api_base: '',
      api_base_source: 'env',
    },
    proxy: {
      mode: 'direct',
      mode_source: 'default',
      url_set: false,
      url_source: 'none',
      telegram_enabled: false,
      telegraph_enabled: false,
    },
    upload: { default_mode: 'photo', mode_source: 'default' },
    meta: { filenames: ['说明.txt'], parse_mode: 'first-line', source: 'default' },
    env_managed: [],
    ...overrides,
  }
}

test('未配置且未托管时可以申请账户', () => {
  const state = telegraphAccountState(view())
  assert.equal(state, 'ready')
  assert.equal(telegraphAccountDisabled(state), false)
  assert.match(telegraphAccountHint(state, false), /一键申请/)
  assert.match(telegraphAccountHint(state, true), /正在申请/)
})

test('已配置或环境托管时禁用申请', () => {
  const configured = view({ telegraph: { ...view().telegraph, token_set: true, token_source: 'stored' } })
  assert.equal(telegraphAccountState(configured), 'configured')
  assert.equal(telegraphAccountDisabled('configured'), true)
  assert.match(telegraphAccountHint('configured', false), /无需重复申请/)

  const envSource = view({ telegraph: { ...view().telegraph, token_source: 'env' } })
  assert.equal(telegraphAccountState(envSource), 'managed')

  const envManaged = view({ env_managed: ['telegraph.token'] })
  assert.equal(telegraphAccountState(envManaged), 'managed')
  assert.equal(telegraphAccountDisabled('managed'), true)
})

test('凭据表单校验字段级错误', () => {
  const missingCurrent = credentialErrors({
    currentPassword: '',
    username: 'ops',
    newPassword: '',
    confirmPassword: '',
  })
  assert.equal(missingCurrent.current_password, '请输入当前密码')

  const badName = credentialErrors({
    currentPassword: 'secret',
    username: 'bad name',
    newPassword: '',
    confirmPassword: '',
  })
  assert.ok(badName.username !== undefined)

  const shortPass = credentialErrors({
    currentPassword: 'secret',
    username: '',
    newPassword: 'too-short',
    confirmPassword: 'too-short',
  })
  assert.ok(shortPass.new_password !== undefined)

  const tooManyBytes = credentialErrors({
    currentPassword: 'secret',
    username: '',
    newPassword: '密'.repeat(30),
    confirmPassword: '密'.repeat(30),
  })
  assert.ok(tooManyBytes.new_password !== undefined)

  const mismatch = credentialErrors({
    currentPassword: 'secret',
    username: '',
    newPassword: 'new-password-123',
    confirmPassword: 'new-password-124',
  })
  assert.ok(mismatch.confirm_password !== undefined)

  const nothingChanged = credentialErrors({
    currentPassword: 'secret',
    username: '   ',
    newPassword: '',
    confirmPassword: '',
  })
  assert.equal(nothingChanged.username, '用户名和新密码至少修改一项')
})

test('合法表单没有错误', () => {
  const errors = credentialErrors({
    currentPassword: 'secret',
    username: 'ops.team-1',
    newPassword: 'new-password-123',
    confirmPassword: 'new-password-123',
  })
  assert.deepEqual(errors, {})
  assert.equal(hasCredentialErrors(errors), false)
})

test('只改用户名不需要填写新密码与确认', () => {
  const errors = credentialErrors({
    currentPassword: 'secret',
    username: 'ops.team',
    newPassword: '',
    confirmPassword: '',
  })
  assert.deepEqual(errors, {})
})

test('后端字段名映射到表单字段', () => {
  assert.equal(credentialFieldError('current_password'), 'current_password')
  assert.equal(credentialFieldError('username'), 'username')
  assert.equal(credentialFieldError('new_password'), 'new_password')
  assert.equal(credentialFieldError('password'), null)
})
