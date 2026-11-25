import { computed, onBeforeUnmount, ref, unref, watch, type Ref } from 'vue'
import { TOUCH_REVEAL_MS, resolveRevealTrigger, shouldRevealOnTap, shouldShowBlur } from '../../utils/gallery'

// 所有封面共用同一套遮罩规则：显式点按一次揭开当前封面，再点才执行动作。
export function useCoverReveal(blurred: Ref<boolean> | (() => boolean)) {
  const revealed = ref(false)
  let timer: ReturnType<typeof setTimeout> | undefined
  // pointerRevealed 记录这次揭开来自鼠标点在封面上；鼠标一离开封面就立即回遮。
  let pointerRevealed = false

  const isBlurred = typeof blurred === 'function' ? blurred : () => unref(blurred)
  const showBlur = computed(() => shouldShowBlur(isBlurred(), revealed.value))

  function clearTimer(): void {
    if (timer !== undefined) {
      clearTimeout(timer)
      timer = undefined
    }
  }

  // 触屏与键盘没有「离开」事件，改用固定短时回遮。
  function scheduleShortRemask(): void {
    clearTimer()
    timer = setTimeout(() => {
      revealed.value = false
      timer = undefined
    }, TOUCH_REVEAL_MS)
  }

  // onCoverLeave 让鼠标离开封面时立即重新遮罩；其它路径的揭开不受影响。
  function onCoverLeave(): void {
    if (!pointerRevealed) return
    pointerRevealed = false
    clearTimer()
    if (isBlurred()) revealed.value = false
  }

  // activate 收到点按时先揭开遮罩，已揭开或未开启模糊则直接执行动作。
  // event 区分鼠标、触屏与键盘；options.fromCover 标记这次点按直接落在封面上。
  function activate(
    action: () => void,
    event?: { pointerType?: string; detail?: number },
    options: { fromCover?: boolean } = {},
  ): void {
    const onCover = options.fromCover === true && resolveRevealTrigger(event) === 'pointer'
    // 直接点封面时取消遗留的短时计时，改由鼠标离开负责回遮。
    if (onCover) clearTimer()
    if (!shouldRevealOnTap({ blurred: isBlurred(), revealed: revealed.value })) {
      // 再次点按打开详情。若首击来自键盘或行内按钮，这次鼠标点封面沿用离开回遮，
      // 避免短时计时被清掉后没有任何回遮路径。
      if (onCover) pointerRevealed = true
      action()
      return
    }
    revealed.value = true
    if (onCover) {
      pointerRevealed = true
      return
    }
    // 行内按钮、触屏与键盘只做短时揭开，鼠标离开不影响它们。
    pointerRevealed = false
    scheduleShortRemask()
  }

  // remask 供轮播换项与全局开关复位时立刻回遮。
  function remask(): void {
    clearTimer()
    pointerRevealed = false
    revealed.value = false
  }

  // 重新开启全局模糊时清掉临时解锁，避免旧的揭开状态被带回来。
  watch(isBlurred, (value) => {
    if (value) {
      clearTimer()
      pointerRevealed = false
      revealed.value = false
    }
  })

  onBeforeUnmount(() => {
    clearTimer()
  })

  return { revealed, showBlur, activate, remask, onCoverLeave }
}
