<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { fetchHealth } from '../api/client'

const version = ref('')
const commit = ref('')
const failed = ref(false)

onMounted(async () => {
  try {
    const health = await fetchHealth()
    version.value = health.version
    commit.value = health.commit
  } catch {
    failed.value = true
  }
})
</script>

<template>
  <footer class="site-footer border-t border-line bg-surface/70">
    <div
      class="mx-auto flex w-full max-w-[1100px] flex-col gap-1 px-4 py-4 text-xs text-muted sm:flex-row sm:items-center sm:justify-between"
    >
      <p>SeSeGraph · SeSe图册</p>
      <p v-if="failed" class="tnum">版本信息不可用</p>
      <p v-else-if="version" class="tnum">当前版本 {{ version }} · 提交 {{ commit }}</p>
      <p v-else class="tnum">正在读取版本…</p>
    </div>
  </footer>
</template>
