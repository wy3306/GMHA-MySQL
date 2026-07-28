export function taskSummaryFromDetail(detail) {
  return detail?.task || detail?.Task || null
}

export function mergeTaskSummary(items, detail) {
  const task = taskSummaryFromDetail(detail)
  const taskID = task?.ID || task?.id
  if (!taskID || !Array.isArray(items)) return Array.isArray(items) ? items : []
  return items.map(item => {
    const itemID = item?.ID || item?.id
    return itemID === taskID ? { ...item, ...task } : item
  })
}
