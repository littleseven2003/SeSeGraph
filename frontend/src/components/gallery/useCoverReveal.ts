import { computed, onBeforeUnmount, ref, unref, watch, type Ref } from 'vue'
import {
  COVER_REMASK_MS,
  TOUCH_REVEAL_MS,
  resolveRevealTrigger,
  shouldRevealOnTap,
  shouldShowBlur,
} from '../../utils/gallery'

// 所有封面共用同一套遮罩规则：显式点按一次揭开当前封面，再点才执行动作。
export function useCoverReveal(blurred: Ref<boolean> | (() => boolean)) {
  const revealed = ref(false)
  let timer: ReturnType<typeof setTimeout> | undefined
  let leaveTimer: ReturnType<typeof setTimeout> | undefined
  // 指针是否停在封面内；只有鼠标路径会据此跳过回遮计时。
  let pointerInside = false

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
  function scheduleLeaveRemask(): void {
    clearLeaveTimer()
    leaveTimer = setTimeout(() => {
      revealed.value = false
      leaveTimer = undefined
    }, COVER_REMASK_MS)
  }

  // 触屏与键盘没有「离开」事件，改用固定短时回遮。
  function scheduleShortRemask(): void {
    clearTimer()
    timer = setTimeout(() => {
      revealed.value = false
      timer = undefined
    }, TOUCH_REVEAL_MS)
  }

  function onCoverLeave(): void {
    pointerInside = false
    if (!revealed.value || !isBlurred()) return
    scheduleLeaveRemask()
  }

  function onCoverEnter(): void {
    pointerInside = true
    clearLeaveTimer()
  }

  // activate 收到点按时先揭开遮罩，已揭开或未开启模糊则直接执行动作。
  // event 用于区分鼠标、触屏与键盘：鼠标停在封面内不启动短时计时，交由离开后的 2 秒计时。
  function activate(action: () => void, event?: { pointerType?: string; detail?: number }): void {
    const trigger = resolveRevealTrigger(event)
    // 鼠标操作取消键盘或触屏遗留的短时计时，避免指针还在封面内就被提前回遮。
    if (trigger === 'pointer') clearTimer()
    if (!shouldRevealOnTap({ blurred: isBlurred(), revealed: revealed.value })) {
      action()
      return
    }
    revealed.value = true
    if (trigger === 'pointer') {
      // 真实指针点击必然发生在封面内：不启动短时计时，交给离开后的 2 秒计时。
      pointerInside = true
      clearLeaveTimer()
      return
    }
    clearLeaveTimer()
    scheduleShortRemask()
  }

  // remask 手动重新遮罩，供解锁后的「重新遮罩」入口使用。
  function remask(): void {
    clearTimer()
    clearLeaveTimer()
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
