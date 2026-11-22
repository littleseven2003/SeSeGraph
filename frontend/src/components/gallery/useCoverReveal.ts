import { computed, onBeforeUnmount, ref, unref, type Ref } from 'vue'
import { TOUCH_REVEAL_MS, shouldRevealOnTap, shouldShowBlur } from '../../utils/gallery'

// 所有封面共用同一套遮罩规则：显式点按一次揭开当前封面，再点才执行动作。
export function useCoverReveal(blurred: Ref<boolean> | (() => boolean)) {
  const revealed = ref(false)
  let timer: ReturnType<typeof setTimeout> | undefined

  const isBlurred = typeof blurred === 'function' ? blurred : () => unref(blurred)
  const showBlur = computed(() => shouldShowBlur(isBlurred(), revealed.value))

  function clearTimer(): void {
    if (timer !== undefined) {
      clearTimeout(timer)
      timer = undefined
    }
  }

  // activate 收到点按时先揭开遮罩，已揭开或未开启模糊则直接执行动作。
  function activate(action: () => void): void {
    if (shouldRevealOnTap({ blurred: isBlurred(), revealed: revealed.value })) {
      revealed.value = true
      clearTimer()
      timer = setTimeout(() => {
        revealed.value = false
        timer = undefined
      }, TOUCH_REVEAL_MS)
      return
    }
    action()
  }

  // remask 手动重新遮罩，供解锁后的「重新遮罩」入口使用。
  function remask(): void {
    clearTimer()
    revealed.value = false
  }

  onBeforeUnmount(clearTimer)

  return { revealed, showBlur, activate, remask }
}
