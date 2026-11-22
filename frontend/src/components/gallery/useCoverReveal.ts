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
  // event 区分鼠标、触屏与键盘；options.fromCover 标记这次点按直接落在封面上：
  // 只有鼠标直接点封面才走「离开后 2 秒」回遮，行内按钮一律走短时回遮。
  function activate(
    action: () => void,
    event?: { pointerType?: string; detail?: number },
    options: { fromCover?: boolean } = {},
  ): void {
    const onCover = options.fromCover === true && resolveRevealTrigger(event) === 'pointer'
    // 直接点封面时取消遗留的短时计时，避免指针还在封面内就被提前回遮。
    if (onCover) clearTimer()
    if (!shouldRevealOnTap({ blurred: isBlurred(), revealed: revealed.value })) {
      // 再次点按打开详情，已有的回遮计时保持不变。
      action()
      return
    }
    revealed.value = true
    if (onCover) {
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
