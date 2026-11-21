<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { ApiError, setupAccount } from '../api/client'

const router = useRouter()
const username = ref('')
const password = ref('')
const confirmPassword = ref('')
const setupToken = ref('')
const pending = ref(false)
const errorMessage = ref('')

function validate(): string {
  if (username.value.trim() === '') {
    return '请填写管理员账号'
  }
  if (password.value.length < 8) {
    return '密码至少 8 个字符'
  }
  if (password.value !== confirmPassword.value) {
    return '两次输入的密码不一致'
  }
  if (setupToken.value === '') {
    return '请填写初始化凭证'
  }
  return ''
}

async function submit(): Promise<void> {
  if (pending.value) return
  errorMessage.value = validate()
  if (errorMessage.value !== '') {
    return
  }

  pending.value = true
  try {
    await setupAccount(username.value.trim(), password.value, setupToken.value)
    await router.replace({ name: 'home' })
  } catch (error) {
    errorMessage.value = error instanceof ApiError ? error.message : '初始化失败，请稍后重试'
  } finally {
    pending.value = false
  }
}
</script>

<template>
  <main class="mx-auto flex w-full max-w-[460px] flex-col justify-center px-4 py-12 sm:py-20">
    <div class="mb-8 flex items-center gap-3">
      <span
        class="flex h-10 w-10 items-center justify-center rounded-lg bg-brand text-base font-semibold text-brand-ink"
        aria-hidden="true"
        >S</span
      >
      <div>
        <p class="text-xs font-medium tracking-wide text-muted">SeSeGraph</p>
        <p class="text-sm font-semibold">SeSe图册</p>
      </div>
    </div>

    <h1 class="text-2xl font-semibold tracking-tight">初始化管理员</h1>
    <p class="mt-2 text-sm text-muted">这是首次启动，创建唯一的后台账户后即可使用。</p>

    <form class="mt-6 rounded-lg border border-line bg-surface p-6 shadow-sm" novalidate @submit.prevent="submit">
      <div>
        <label for="username" class="block text-sm font-medium">账号</label>
        <input
          id="username"
          v-model="username"
          name="username"
          type="text"
          autocomplete="username"
          required
          class="mt-2 h-11 w-full rounded-md border border-line bg-surface px-3 text-sm outline-none transition focus:border-brand focus:ring-2 focus:ring-brand/15"
        />
      </div>

      <div class="mt-4">
        <label for="password" class="block text-sm font-medium">密码</label>
        <input
          id="password"
          v-model="password"
          name="new-password"
          type="password"
          autocomplete="new-password"
          required
          class="mt-2 h-11 w-full rounded-md border border-line bg-surface px-3 text-sm outline-none transition focus:border-brand focus:ring-2 focus:ring-brand/15"
        />
        <p class="mt-1.5 text-xs text-muted">至少 8 个字符，建议使用密码管理器生成。</p>
      </div>

      <div class="mt-4">
        <label for="confirm" class="block text-sm font-medium">确认密码</label>
        <input
          id="confirm"
          v-model="confirmPassword"
          name="confirm-password"
          type="password"
          autocomplete="new-password"
          required
          class="mt-2 h-11 w-full rounded-md border border-line bg-surface px-3 text-sm outline-none transition focus:border-brand focus:ring-2 focus:ring-brand/15"
        />
      </div>

      <div class="mt-4">
        <label for="setup-token" class="block text-sm font-medium">初始化凭证</label>
        <input
          id="setup-token"
          v-model="setupToken"
          name="setup-token"
          type="password"
          autocomplete="off"
          required
          class="mt-2 h-11 w-full rounded-md border border-line bg-surface px-3 text-sm outline-none transition focus:border-brand focus:ring-2 focus:ring-brand/15"
        />
        <p class="mt-1.5 text-xs text-muted">由部署时配置的 <code class="tnum">ADMIN_SETUP_TOKEN</code> 提供，仅用于本次初始化。</p>
      </div>

      <p
        v-if="errorMessage"
        role="alert"
        class="mt-4 rounded-md border border-danger/20 bg-danger-soft px-3 py-2 text-sm text-danger"
      >
        {{ errorMessage }}
      </p>

      <button
        type="submit"
        :disabled="pending"
        class="mt-6 h-11 w-full rounded-md bg-brand text-sm font-medium text-brand-ink transition hover:bg-brand-strong disabled:cursor-not-allowed disabled:opacity-60"
      >
        {{ pending ? '正在创建…' : '创建管理员' }}
      </button>
    </form>
  </main>
</template>
