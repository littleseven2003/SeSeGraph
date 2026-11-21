<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { ApiError, login } from '../api/client'

const router = useRouter()
const username = ref('')
const password = ref('')
const pending = ref(false)
const errorMessage = ref('')

async function submit(): Promise<void> {
  if (pending.value) return
  errorMessage.value = ''
  pending.value = true
  try {
    await login(username.value.trim(), password.value)
    await router.replace({ name: 'home' })
  } catch (error) {
    errorMessage.value = error instanceof ApiError ? error.message : '登录失败，请稍后重试'
  } finally {
    pending.value = false
  }
}
</script>

<template>
  <main class="mx-auto flex w-full max-w-[440px] flex-col justify-center px-4 py-12 sm:py-20">
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

    <h1 class="text-2xl font-semibold tracking-tight">登录管理后台</h1>
    <p class="mt-2 text-sm text-muted">单管理员账户，登录后管理图册与发布记录。</p>

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
          name="password"
          type="password"
          autocomplete="current-password"
          required
          class="mt-2 h-11 w-full rounded-md border border-line bg-surface px-3 text-sm outline-none transition focus:border-brand focus:ring-2 focus:ring-brand/15"
        />
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
        :disabled="pending || username === '' || password === ''"
        class="mt-6 h-11 w-full rounded-md bg-brand text-sm font-medium text-brand-ink transition hover:bg-brand-strong disabled:cursor-not-allowed disabled:opacity-60"
      >
        {{ pending ? '正在登录…' : '登录' }}
      </button>
    </form>
  </main>
</template>
