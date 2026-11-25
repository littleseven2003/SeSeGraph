<script setup lang="ts">
import { ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { logout } from '../api/client.ts'
import BrandMark from './BrandMark.vue'
import { blurEnabled, toggleBlur } from '../utils/blur.ts'
import { resolvedTheme, toggleTheme } from '../utils/theme.ts'

// 全站统一顶栏：左侧品牌，右侧防社死开关与三个入口；窄屏折成两行避免拥挤。
const router = useRouter()
const route = useRoute()
const signingOut = ref(false)

// 当前页导航项用互斥类：静态态只带 text-muted，避免与高亮色同时挂在同一节点上被覆盖。
function navClass(name: string): string {
  const active = route.name === name
  return active
    ? 'inline-flex h-10 shrink-0 items-center rounded-lg bg-brand-soft px-3 text-sm font-medium text-brand-strong transition sm:h-9'
    : 'inline-flex h-10 shrink-0 items-center rounded-lg px-3 text-sm text-muted transition hover:bg-canvas hover:text-ink sm:h-9'
}

// 窄屏导航外层留 44px 热区，内层视觉块保持 36px 与 8px 圆角。
function narrowNavClass(name: string): string {
  const active = route.name === name
  return active
    ? 'inline-flex h-9 w-full items-center justify-center rounded-lg bg-brand-soft text-sm font-medium text-brand-strong transition'
    : 'inline-flex h-9 w-full items-center justify-center rounded-lg text-sm text-muted transition hover:bg-canvas hover:text-ink'
}

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
    <div class="mx-auto w-full max-w-[1120px] px-4 sm:px-5">
      <div class="flex items-center justify-between gap-2 py-2 sm:py-3">
        <router-link :to="{ name: 'home' }" class="flex min-w-0 items-center gap-2" aria-label="回到图集">
          <BrandMark :width="108" class="sm:hidden" />
          <BrandMark :width="124" class="hidden sm:block" />
          <span class="hidden h-4 w-px shrink-0 bg-line sm:block" aria-hidden="true"></span>
          <span class="hidden shrink-0 text-xs font-medium tracking-[0.04em] text-muted sm:inline">SeSe图册</span>
        </router-link>

        <div class="flex min-w-0 items-center gap-0.5 sm:gap-1">
          <!-- 主题切换：外层 44px 触控区，内层 32px 图标块，与防社死芯片同高。 -->
          <button
            type="button"
            class="inline-flex h-11 shrink-0 items-center"
            :aria-label="resolvedTheme === 'dark' ? '切换到浅色主题' : '切换到深色主题'"
            :title="resolvedTheme === 'dark' ? '当前深色，点击切到浅色' : '当前浅色，点击切到深色'"
            @click="toggleTheme"
          >
            <span class="inline-flex h-8 w-8 items-center justify-center rounded-lg border border-line bg-canvas/60 text-muted transition hover:border-brand/40 hover:text-ink">
              <svg v-if="resolvedTheme === 'dark'" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" class="h-4 w-4" aria-hidden="true">
                <circle cx="12" cy="12" r="4" />
                <path d="M12 3v2M12 19v2M3 12h2M19 12h2M5.6 5.6l1.4 1.4M17 17l1.4 1.4M18.4 5.6L17 7M7 17l-1.4 1.4" stroke-linecap="round" />
              </svg>
              <svg v-else viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" class="h-4 w-4" aria-hidden="true">
                <path d="M20 14.5A8 8 0 0 1 9.5 4a8.2 8.2 0 1 0 10.5 10.5Z" stroke-linecap="round" stroke-linejoin="round" />
              </svg>
            </span>
          </button>

          <!-- 外层保留 44px 触控区，内层描边芯片固定 32px 视觉高度。 -->
          <button
            type="button"
            class="inline-flex h-11 shrink-0 items-center"
            :aria-pressed="blurEnabled"
            :aria-label="blurEnabled ? '关闭防社死模糊' : '开启防社死模糊'"
            :title="blurEnabled ? '防社死已开启' : '防社死已关闭'"
            @click="toggleBlur"
          >
            <span
              class="inline-flex h-8 items-center gap-1.5 rounded-lg border px-2 text-xs font-medium transition sm:px-2.5"
              :class="blurEnabled ? 'border-accent/30 bg-accent/10 text-accent' : 'border-line bg-canvas/60 text-muted'"
            >
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" class="h-3.5 w-3.5 shrink-0" aria-hidden="true">
                <path d="M2.5 12S6 6.5 12 6.5 21.5 12 21.5 12 18 17.5 12 17.5 2.5 12 2.5 12Z" stroke-linecap="round" />
                <circle cx="12" cy="12" r="2.6" />
                <path v-if="blurEnabled" d="M4 20L20 4" stroke-linecap="round" />
              </svg>
              <span
                class="relative inline-flex h-3.5 w-6 shrink-0 items-center rounded-full transition motion-safe:active:scale-95"
                :class="blurEnabled ? 'bg-accent' : 'bg-line'"
                aria-hidden="true"
              >
                <span class="absolute h-2.5 w-2.5 rounded-full bg-white transition" :class="blurEnabled ? 'left-3' : 'left-0.5'"></span>
              </span>
              <span class="hidden sm:inline">防社死</span>
            </span>
          </button>

          <nav class="hidden items-center gap-0.5 sm:flex sm:gap-1" aria-label="全站导航">
            <router-link
              v-for="item in NAV"
              :key="item.name"
              :to="{ name: item.name }"
              :class="navClass(item.name)"
            >
              {{ item.label }}
            </router-link>
          </nav>

          <button
            type="button"
            class="inline-flex h-11 shrink-0 items-center justify-center rounded-lg px-2.5 text-sm text-muted transition hover:bg-canvas hover:text-ink disabled:opacity-60 sm:h-9 sm:px-3"
            :aria-label="signingOut ? '正在退出…' : '退出登录'"
            :disabled="signingOut"
            @click="signOut"
          >
            <span class="hidden sm:inline">{{ signingOut ? '正在退出…' : '退出登录' }}</span>
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" class="h-4 w-4 sm:hidden" aria-hidden="true">
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
          class="inline-flex h-11 flex-1 items-center justify-center"
        >
          <span :class="narrowNavClass(item.name)">{{ item.label }}</span>
        </router-link>
      </nav>
    </div>
  </header>
</template>
