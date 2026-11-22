<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { ApiError, login } from '../api/client'
import BrandMark from '../components/BrandMark.vue'

const router = useRouter()
const username = ref('')
const password = ref('')
const pending = ref(false)
const errorMessage = ref('')
const showPassword = ref(false)

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
  <main class="flex min-h-full items-center justify-center px-5 py-12">
    <div class="w-full max-w-[400px]">
      <div class="flex items-center gap-2.5">
        <BrandMark />
        <div class="min-w-0">
          <p class="text-sm font-semibold tracking-tight">SeSeGraph</p>
          <p class="mt-0.5 text-xs text-muted">SeSe图册</p>
        </div>
      </div>

      <h1 class="mt-8 text-xl font-semibold tracking-tight">回到你的私人图册</h1>
      <p class="mt-1.5 text-sm text-muted">输入管理员账号和密码，打开冲印台与发布记录。</p>

      <form class="mt-6 overflow-hidden rounded-md border border-line bg-surface p-5" novalidate @submit.prevent="submit">
        <div class="sprocket -mx-5 -mt-5 mb-5 h-3" aria-hidden="true"></div>
        <div>
          <label for="username" class="block text-xs font-medium text-muted">账号</label>
          <input
            id="username"
            v-model="username"
            name="username"
            type="text"
            autocomplete="username"
            required
            class="mt-1.5 h-11 w-full rounded-md border border-line bg-canvas px-3 text-sm outline-none transition focus:border-ink/40 focus:bg-surface"
          />
        </div>

        <div class="mt-4">
          <label for="password" class="block text-xs font-medium text-muted">密码</label>
          <div class="relative mt-1.5">
            <input
              id="password"
              v-model="password"
              name="password"
              :type="showPassword ? 'text' : 'password'"
              autocomplete="current-password"
              required
              class="h-11 w-full rounded-md border border-line bg-canvas pl-3 pr-14 text-sm outline-none transition focus:border-ink/40 focus:bg-surface"
            />
            <button
              type="button"
              class="absolute right-0.5 top-1/2 flex h-11 w-11 -translate-y-1/2 items-center justify-center rounded text-xs font-medium text-muted transition hover:bg-canvas hover:text-ink"
              :aria-pressed="showPassword"
              :aria-label="showPassword ? '隐藏密码' : '显示密码'"
              @click="showPassword = !showPassword"
            >
              {{ showPassword ? '隐藏' : '显示' }}
            </button>
          </div>
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
          class="mt-5 h-11 w-full rounded-md bg-brand text-sm font-medium text-brand-ink shadow-[0_2px_0_0_#9e2b12] transition hover:bg-brand-strong motion-safe:active:translate-y-[2px] active:shadow-none disabled:cursor-not-allowed disabled:bg-line disabled:text-muted disabled:shadow-none"
        >
          {{ pending ? '正在登录…' : '登录' }}
        </button>
      </form>
    </div>
  </main>
</template>
