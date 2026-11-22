<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { ApiError, setupAccount } from '../api/client'
import BrandMark from '../components/BrandMark.vue'

const router = useRouter()
const username = ref('')
const password = ref('')
const confirmPassword = ref('')
const setupToken = ref('')
const pending = ref(false)
const errorMessage = ref('')
const showPassword = ref(false)
const showConfirm = ref(false)
const showToken = ref(false)

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
  <main class="flex min-h-full items-center justify-center px-5 py-12">
    <div class="w-full max-w-[400px]">
      <BrandMark :width="164" />
      <p class="mt-1.5 text-xs font-medium tracking-[0.04em] text-muted">SeSe图册</p>

      <h1 class="mt-8 text-xl font-semibold tracking-tight">布置你的第一间冲印室</h1>
      <p class="mt-1.5 text-sm text-muted">首次启动，创建一个管理员账号即可开始使用。</p>

      <form class="mt-6 overflow-hidden rounded-2xl bg-surface p-5 shadow-[0_1px_0_0_rgba(27,26,23,0.05),0_24px_60px_-40px_rgba(27,26,23,0.35)]" novalidate @submit.prevent="submit">
        <div>
          <label for="username" class="block text-xs font-medium text-muted">账号</label>
          <input
            id="username"
            v-model="username"
            name="username"
            type="text"
            autocomplete="username"
            required
            class="mt-1.5 h-11 w-full rounded-xl border border-line bg-canvas px-3 text-sm outline-none transition focus:border-ink/40 focus:bg-surface"
          />
        </div>

        <div class="mt-4">
          <label for="password" class="block text-xs font-medium text-muted">密码</label>
          <div class="relative mt-1.5">
            <input
              id="password"
              v-model="password"
              name="new-password"
              :type="showPassword ? 'text' : 'password'"
              autocomplete="new-password"
              required
              class="h-11 w-full rounded-xl border border-line bg-canvas pl-3 pr-14 text-sm outline-none transition focus:border-ink/40 focus:bg-surface"
            />
            <button
              type="button"
              class="absolute right-0.5 top-1/2 flex h-11 w-11 -translate-y-1/2 items-center justify-center rounded-full text-xs font-medium text-muted transition hover:bg-canvas hover:text-ink"
              :aria-pressed="showPassword"
              :aria-label="showPassword ? '隐藏密码' : '显示密码'"
              @click="showPassword = !showPassword"
            >
              {{ showPassword ? '隐藏' : '显示' }}
            </button>
          </div>
          <p class="mt-1.5 text-xs text-muted">至少 8 个字符，建议使用密码管理器生成。</p>
        </div>

        <div class="mt-4">
          <label for="confirm" class="block text-xs font-medium text-muted">确认密码</label>
          <div class="relative mt-1.5">
            <input
              id="confirm"
              v-model="confirmPassword"
              name="confirm-password"
              :type="showConfirm ? 'text' : 'password'"
              autocomplete="new-password"
              required
              class="h-11 w-full rounded-xl border border-line bg-canvas pl-3 pr-14 text-sm outline-none transition focus:border-ink/40 focus:bg-surface"
            />
            <button
              type="button"
              class="absolute right-0.5 top-1/2 flex h-11 w-11 -translate-y-1/2 items-center justify-center rounded-full text-xs font-medium text-muted transition hover:bg-canvas hover:text-ink"
              :aria-pressed="showConfirm"
              :aria-label="showConfirm ? '隐藏确认密码' : '显示确认密码'"
              @click="showConfirm = !showConfirm"
            >
              {{ showConfirm ? '隐藏' : '显示' }}
            </button>
          </div>
        </div>

        <div class="mt-4">
          <label for="setup-token" class="block text-xs font-medium text-muted">初始化凭证</label>
          <div class="relative mt-1.5">
            <input
              id="setup-token"
              v-model="setupToken"
              name="setup-token"
              :type="showToken ? 'text' : 'password'"
              autocomplete="off"
              required
              class="h-11 w-full rounded-xl border border-line bg-canvas pl-3 pr-14 text-sm outline-none transition focus:border-ink/40 focus:bg-surface"
            />
            <button
              type="button"
              class="absolute right-0.5 top-1/2 flex h-11 w-11 -translate-y-1/2 items-center justify-center rounded-full text-xs font-medium text-muted transition hover:bg-canvas hover:text-ink"
              :aria-pressed="showToken"
              :aria-label="showToken ? '隐藏初始化凭证' : '显示初始化凭证'"
              @click="showToken = !showToken"
            >
              {{ showToken ? '隐藏' : '显示' }}
            </button>
          </div>
          <p class="mt-1.5 text-xs text-muted">
            由部署时配置的 <code class="tnum">ADMIN_SETUP_TOKEN</code> 提供，仅用于本次初始化。
          </p>
        </div>

        <p
          v-if="errorMessage"
          role="alert"
          class="mt-4 rounded-xl bg-danger-soft px-3 py-2 text-sm text-danger"
        >
          {{ errorMessage }}
        </p>

        <button
          type="submit"
          :disabled="pending"
          class="mt-5 h-11 w-full rounded-full bg-brand text-sm font-medium text-brand-ink shadow-[0_2px_0_0_#9e2b12] transition hover:bg-brand-strong motion-safe:active:translate-y-[2px] active:shadow-none disabled:cursor-not-allowed disabled:bg-line disabled:text-muted disabled:shadow-none"
        >
          {{ pending ? '正在创建…' : '创建管理员' }}
        </button>
      </form>
    </div>
  </main>
</template>
