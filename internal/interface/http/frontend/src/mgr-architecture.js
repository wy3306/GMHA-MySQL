export function isValidMGRMemberCount(count) {
  const value = Number(count || 0)
  return value >= 3 && value <= 9 && value % 2 === 1
}

export function mgrMemberRequirement(count) {
  const value = Number(count || 0)
  if (value < 3) return `当前只有 ${value} 个实例，还需添加 ${3 - value} 个实例才能组成最小三节点 MGR。`
  if (value > 9) return `当前有 ${value} 个实例，MGR 最多支持 9 个成员，请先移除 ${value - 9} 个。`
  if (value % 2 === 0) return `当前有 ${value} 个实例；MGR 需要奇数成员，请再添加 1 个实例或移除 1 个。`
  return ''
}
