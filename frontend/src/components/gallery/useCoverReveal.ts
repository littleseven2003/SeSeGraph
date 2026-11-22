import { computed, onBeforeUnmount, ref, unref, watch, type Ref } from 'vue'
import { COVER_REMASK_MS, TOUCH_REVEAL_MS, shouldRevealOnTap, shouldShowBlur } from '../../utils/gallery'

// 所有封面共用同一套遮罩规则：显式点按一次揭开当前封面，再点才执行动作。
export function useCoverReveal(blurred: Ref<boolean> | (() => boolean)) {
  const revealed = ref(false)
  let timer: ReturnType<typeof setTimeout> | undefined
  let leaveTimer: ReturnType<typeof setTimeout> | undefined
  // 悬停在封面内的指针设备不启动短时计时，只由离开后的 2 秒计时负责回遮。
  let pointerInside = false
  const hoverCapable =
    typeof window !== 'undefined' && typeof window.matchMedia === 'function'
      ? window.matchMedia('(hover: hover)').matches
      : false

  const isBlurred = typeof blurred === 'function' ? blurred : () => unref(blurred)
  const showBlur = computed(() => shouldShowBlur(isBlurred(), revealed.value))

  function clearTimer(): void {
    if (timer !== undefined) {
      clearTimeout(timer)
      timer = undefined
    }
  }

  function clearLeaveTimer(): void {
    if (leaveTimer !== undefined) {
      clearTimeout(leaveTimer)
      leaveTimer = undefined
    }
  }

  // 鼠标离开已解锁封面满 2 秒自动回遮；2 秒内移回则取消计时保持清晰。
  function onCoverLeave(): void {
    pointerInside = false
    if (!revealed.value || !isBlurred()) return
    clearLeaveTimer()
    leaveTimer = setTimeout(() => {
      revealed.value = false
      leaveTimer = undefined
    }, COVER_REMASK_MS)
  }

  function onCoverEnter(): void {
    pointerInside = true
    clearLeaveTimer()
  }

  // activate 收到点按时先揭开遮罩，已揭开或未开启模糊则直接执行动作。
  function activate(action: () => void): void {
    if (shouldRevealOnTap({ blurred: isBlurred(), revealed: revealed.value })) {
      revealed.value = true
      clearTimer()
      // 鼠标停在封面内时保持清晰，离开后才由 2 秒计时回遮。
      if (!(hoverCapable && pointerInside)) {
        timer = setTimeout(() => {
          revealed.value = false
          timer = undefined
        }, TOUCH_REVEAL_MS)
      }
      return
    }
    action()
  }

  // remask 手动重新遮罩，供解锁后的「重新遮罩」入口使用。
  function remask(): void {
    clearTimer()
    clearLeaveTimer()
    pointerInside = false
    revealed.value = false
  }

  // 重新开启全局模糊时清掉临时解锁，避免旧的揭开状态被带回来。
  watch(isBlurred, (value) => {
    if (value) {
      clearTimer()
      clearLeaveTimer()
      revealed.value = false
    }
  })

  onBeforeUnmount(() => {
    clearTimer()
    clearLeaveTimer()
  })

  return { revealed, showBlur, activate, remask, onCoverEnter, onCoverLeave }
}
