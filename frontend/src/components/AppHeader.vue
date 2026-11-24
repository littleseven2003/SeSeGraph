<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { logout } from '../api/client.ts'
import BrandMark from './BrandMark.vue'
import { blurEnabled, toggleBlur } from '../utils/blur.ts'

// 全站统一顶栏：左侧品牌，右侧防社死开关与三个入口；窄屏折成两行避免拥挤。
const router = useRouter()
const signingOut = ref(false)

const NAV = [
  { name: 'home', label: '图集' },
  { name: 'tasks', label: '任务' },
  { name: 'settings', label: '设置' },
]

async function signOut(): Promise<void> {
  if (signingOut.value) return
  signingOut.value = true
  try {
    await logout()
  } catch {
    // 登出失败也回登录页，避免停在已失效的界面
  }
  signingOut.value = false
  await router.replace({ name: 'login' })
}
</script>

<template>
  <header class="sticky top-0 z-30 border-b border-line/80 bg-surface/95 backdrop-blur-sm">
    <div class="mx-auto w-full max-w-[1240px] px-4 sm:px-5">
      <div class="flex items-center justify-between gap-2 py-2 sm:py-3">
        <router-link :to="{ name: 'home' }" class="flex min-w-0 items-center gap-2" aria-label="回到图集">
          <BrandMark :width="108" class="sm:hidden" />
          <BrandMark :width="124" class="hidden sm:block" />
          <span class="hidden h-4 w-px shrink-0 bg-line sm:block" aria-hidden="true"></span>
          <span class="hidden shrink-0 text-xs font-medium tracking-[0.04em] text-muted sm:inline">SeSe图册</span>
        </router-link>

        <div class="flex min-w-0 items-center gap-0.5 sm:gap-1">
          <button
            type="button"
            class="inline-flex h-11 shrink-0 items-center gap-2 rounded-full px-2 text-sm font-medium text-muted transition hover:bg-canvas hover:text-ink sm:px-3.5"
            :aria-pressed="blurEnabled"
            aria-label="切换防社死模糊"
            @click="toggleBlur"
          >
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" class="h-4 w-4 shrink-0" aria-hidden="true">
              <path d="M2.5 12S6 6.5 12 6.5 21.5 12 21.5 12 18 17.5 12 17.5 2.5 12 2.5 12Z" stroke-linecap="round" />
              <circle cx="12" cy="12" r="2.6" />
              <path v-if="blurEnabled" d="M4 20L20 4" stroke-linecap="round" />
            </svg>
            <span
              class="relative inline-flex h-4 w-7 shrink-0 items-center rounded-full transition motion-safe:active:scale-95"
              :class="blurEnabled ? 'bg-accent' : 'bg-line'"
              aria-hidden="true"
            >
              <span class="absolute h-3 w-3 rounded-full bg-white transition" :class="blurEnabled ? 'left-3.5' : 'left-0.5'"></span>
            </span>
            <span class="hidden lg:inline">防社死模糊</span>
          </button>

          <nav class="hidden items-center gap-0.5 sm:flex sm:gap-1" aria-label="全站导航">
            <router-link
              v-for="item in NAV"
              :key="item.name"
              :to="{ name: item.name }"
              class="inline-flex h-11 shrink-0 items-center rounded-full px-2.5 text-sm text-muted transition hover:bg-canvas hover:text-ink sm:px-3.5"
              active-class="bg-brand-soft font-medium text-brand-strong"
            >
              {{ item.label }}
            </router-link>
          </nav>

          <button
            type="button"
            class="inline-flex h-11 shrink-0 items-center rounded-full px-2.5 text-sm text-muted transition hover:bg-canvas hover:text-ink disabled:opacity-60 sm:px-3.5"
            :disabled="signingOut"
            @click="signOut"
          >
            <span class="hidden sm:inline">{{ signingOut ? '正在退出…' : '退出登录' }}</span>
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" class="h-4 w-4 sm:hidden" aria-label="退出登录">
              <path d="M15 5H7a1 1 0 0 0-1 1v12a1 1 0 0 0 1 1h8M18 12H10M14 8l4 4-4 4" stroke-linecap="round" stroke-linejoin="round" />
            </svg>
          </button>
        </div>
      </div>

      <nav class="flex gap-1 border-t border-line/60 py-1.5 sm:hidden" aria-label="全站导航（窄屏）">
        <router-link
          v-for="item in NAV"
          :key="'narrow-' + item.name"
          :to="{ name: item.name }"
          class="inline-flex h-10 flex-1 items-center justify-center rounded-full text-sm text-muted transition hover:bg-canvas hover:text-ink"
          active-class="bg-brand-soft font-medium text-brand-strong"
        >
          {{ item.label }}
        </router-link>
      </nav>
    </div>
  </header>
</template>
