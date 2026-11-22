// 复制文本：优先用剪贴板接口，被拒绝或不可用时回落到隐藏文本域。
export async function copyText(text: string): Promise<boolean> {
  if (text === '') return false
  try {
    if (navigator.clipboard !== undefined) {
      await navigator.clipboard.writeText(text)
      return true
    }
  } catch {
    // 权限被拒绝时继续走回落方案
  }
  try {
    const area = document.createElement('textarea')
    area.value = text
    area.setAttribute('readonly', '')
    area.style.position = 'fixed'
    area.style.opacity = '0'
    document.body.appendChild(area)
    area.select()
    const copied = document.execCommand('copy')
    document.body.removeChild(area)
    return copied
  } catch {
    return false
  }
}
