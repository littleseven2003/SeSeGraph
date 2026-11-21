// 文件名自然排序：把连续数字按数值比较，使 2.jpg 排在 10.jpg 之前。

function isDigit(ch: string): boolean {
  return ch >= '0' && ch <= '9'
}

// 去掉数字前导零，避免 "007" 与 "7" 长度不同被误判。
function stripLeadingZeros(digits: string): string {
  let index = 0
  while (index < digits.length - 1 && digits[index] === '0') {
    index += 1
  }
  return digits.slice(index)
}

function compareNumeric(a: string, b: string): number {
  if (a.length !== b.length) {
    return a.length - b.length
  }
  if (a === b) {
    return 0
  }
  return a < b ? -1 : 1
}

// naturalCompare 返回负数表示 a 在前、正数表示 b 在前、0 表示等价。
export function naturalCompare(a: string, b: string): number {
  let i = 0
  let j = 0
  while (i < a.length && j < b.length) {
    const left = a[i]
    const right = b[j]
    if (isDigit(left) && isDigit(right)) {
      const startLeft = i
      const startRight = j
      while (i < a.length && isDigit(a[i])) i += 1
      while (j < b.length && isDigit(b[j])) j += 1
      const diff = compareNumeric(
        stripLeadingZeros(a.slice(startLeft, i)),
        stripLeadingZeros(b.slice(startRight, j)),
      )
      if (diff !== 0) return diff
      continue
    }
    // 忽略大小写比较，避免同一字母因大小写排序不一致。
    const lowered = left.toLowerCase()
    const otherLowered = right.toLowerCase()
    if (lowered !== otherLowered) return lowered < otherLowered ? -1 : 1
    i += 1
    j += 1
  }
  return a.length - i - (b.length - j)
}

// sortByNaturalName 返回按自然排序后的新数组，不改动入参。
export function sortByNaturalName<T>(items: readonly T[], nameOf: (item: T) => string): T[] {
  return items
    .map((item, index) => ({ item, index }))
    .sort((left, right) => {
      const diff = naturalCompare(nameOf(left.item), nameOf(right.item))
      // 名称等价时保留原始顺序，避免刷新后顺序抖动。
      return diff !== 0 ? diff : left.index - right.index
    })
    .map((entry) => entry.item)
}
