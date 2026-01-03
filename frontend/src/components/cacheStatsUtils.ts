const clampPercent = (value: number): number => {
  if (!Number.isFinite(value)) return 0
  return Math.max(0, Math.min(100, value))
}

export const calcHitRatePercent = (hitRate: number): number => {
  if (!Number.isFinite(hitRate)) return 0
  return clampPercent(hitRate * 100)
}

export const calcCapacityUsagePercent = (entries: number, capacity: number): number | null => {
  if (!Number.isFinite(entries) || !Number.isFinite(capacity) || capacity <= 0) return null
  return clampPercent((entries / capacity) * 100)
}

export const getHitRateColor = (hitRatePercent: number): 'success' | 'warning' | 'error' => {
  const rate = clampPercent(hitRatePercent)
  if (rate >= 95) return 'success'
  if (rate >= 80) return 'warning'
  return 'error'
}

export const getCapacityUsageColor = (usagePercent: number | null): string => {
  if (usagePercent === null) return 'grey'
  const usage = clampPercent(usagePercent)
  if (usage >= 95) return 'error'
  if (usage >= 80) return 'warning'
  return 'success'
}

export const formatCount = (n: number): string => {
  if (!Number.isFinite(n)) return '0'
  return new Intl.NumberFormat('en-US').format(n)
}

export const formatLocalDateTime = (timestamp: string): string => {
  if (!timestamp) return ''
  const d = new Date(timestamp)
  if (Number.isNaN(d.getTime())) return ''
  return d.toLocaleString()
}

