<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { fetchHealth } from '../api/client'

const version = ref('')
const failed = ref(false)
const rssURL = ref('')

onMounted(async () => {
  try {
    version.value = (await fetchHealth()).version
  } catch {
    failed.value = true
  }
  // 订阅入口跟随后端启用状态，地址统一由 /api/site 提供，兼容子路径部署。
  try {
    const response = await fetch('/api/site')
    if (response.ok) {
      const site = (await response.json()) as { rss_url?: string }
      rssURL.value = typeof site.rss_url === 'string' ? site.rss_url : ''
    }
  } catch {
    rssURL.value = ''
  }
})
</script>

<template>
  <footer class="site-footer border-t border-line bg-surface/80">
    <div
      class="mx-auto flex w-full max-w-[1120px] flex-col gap-2.5 px-5 py-4 text-xs text-muted sm:flex-row sm:items-center sm:justify-between"
    >
      <p class="flex items-center gap-1.5">
        <span class="font-semibold tracking-tight text-ink">SeSeGraph</span>
        <span aria-hidden="true">·</span>
        <span>SeSe图册</span>
      </p>

      <div class="flex flex-wrap items-center gap-x-3.5 gap-y-2">
        <p v-if="failed" class="tnum">当前版本：不可用</p>
        <p v-else-if="version" class="tnum">当前版本：{{ version }}</p>
        <p v-else class="tnum">当前版本：读取中…</p>

        <span class="hidden h-3 w-px bg-line sm:inline-block" aria-hidden="true"></span>

        <a
          href="https://github.com/littleseven2003/SeSeGraph"
          target="_blank"
          rel="noreferrer noopener"
          class="inline-flex items-center gap-1.5 transition hover:text-ink"
        >
          <svg viewBox="0 0 16 16" fill="currentColor" class="h-4 w-4" aria-hidden="true">
            <path
              d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27s1.36.09 2 .27c1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.01 8.01 0 0 0 16 8c0-4.42-3.58-8-8-8Z"
            />
          </svg>
          <span>littleseven2003/SeSeGraph</span>
        </a>

        <a
          v-if="rssURL !== ''"
          :href="rssURL"
          target="_blank"
          rel="noreferrer noopener"
          class="inline-flex items-center gap-1 rounded-md border border-line bg-canvas/80 px-2.5 py-0.5 text-xs transition hover:border-brand/40 hover:text-brand-strong"
        >
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" class="h-3.5 w-3.5" aria-hidden="true">
            <path d="M4 11a9 9 0 0 1 9 9" stroke-linecap="round" />
            <path d="M4 4a16 16 0 0 1 16 16" stroke-linecap="round" />
            <circle cx="5" cy="19" r="1.5" fill="currentColor" stroke="none" />
          </svg>
          <span>RSS</span>
        </a>
      </div>
    </div>
  </footer>
</template>
