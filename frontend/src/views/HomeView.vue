<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { logout } from '../api/client'

const router = useRouter()
const pending = ref(false)

async function signOut(): Promise<void> {
  if (pending.value) return
  pending.value = true
  try {
    await logout()
  } catch {
    // 登出失败也回到登录页，避免停留在已失效的界面
  }
  pending.value = false
  await router.replace({ name: 'login' })
}
</script>

<template>
  <div class="min-h-full">
    <header class="border-b border-line bg-surface">
      <div class="mx-auto flex w-full max-w-[1100px] items-center justify-between gap-4 px-4 py-3">
        <div class="flex items-center gap-3">
          <span
            class="flex h-9 w-9 items-center justify-center rounded-lg bg-brand text-sm font-semibold text-brand-ink"
            aria-hidden="true"
            >S</span
          >
          <div>
            <p class="text-xs font-medium tracking-wide text-muted">SeSeGraph</p>
            <p class="text-sm font-semibold">管理后台</p>
          </div>
        </div>

        <div class="flex items-center gap-3">
          <span class="hidden items-center gap-2 text-xs text-muted sm:flex">
            <span class="h-1.5 w-1.5 rounded-full bg-accent" aria-hidden="true"></span>
            已登录
          </span>
          <button
            type="button"
            :disabled="pending"
            class="h-9 rounded-md border border-line px-3 text-sm font-medium transition hover:border-muted/40 hover:bg-canvas disabled:opacity-60"
            @click="signOut"
          >
            {{ pending ? '正在退出…' : '退出登录' }}
          </button>
        </div>
      </div>
    </header>

    <main class="mx-auto w-full max-w-[1100px] px-4 py-8">
      <div class="flex flex-wrap items-end justify-between gap-4">
        <div>
          <h1 class="text-xl font-semibold tracking-tight">图集</h1>
          <p class="mt-1 text-sm text-muted">发布记录与封面会显示在这里。</p>
        </div>
        <router-link
          :to="{ name: 'workbench' }"
          class="inline-flex h-10 items-center rounded-md bg-brand px-4 text-sm font-medium text-brand-ink transition hover:bg-brand-strong"
        >
          新建图集
        </router-link>
      </div>

      <section class="mt-6 rounded-lg border border-dashed border-line bg-surface px-6 py-14 text-center">
        <div class="mx-auto flex h-12 w-12 items-center justify-center rounded-lg bg-canvas" aria-hidden="true">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" class="h-6 w-6 text-muted">
            <rect x="3" y="4" width="18" height="16" rx="2" />
            <path d="M3 15l5-4 4 3 3-2 6 4" stroke-linecap="round" stroke-linejoin="round" />
            <circle cx="9" cy="9" r="1.5" />
          </svg>
        </div>
        <h2 class="mt-4 text-base font-semibold">还没有图集</h2>
        <p class="mx-auto mt-2 max-w-[40ch] text-sm text-muted">
          点右上角「新建图集」进入发布工作台，逐张上传会在后续版本接入，完成后图集会出现在这里。
        </p>
      </section>
    </main>
  </div>
</template>
