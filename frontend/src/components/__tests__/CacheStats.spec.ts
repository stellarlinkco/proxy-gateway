import { describe, expect, it } from 'vitest'
import { readFile } from 'node:fs/promises'
import {
  calcCapacityUsagePercent,
  calcHitRatePercent,
  formatCount,
  formatLocalDateTime,
  getCapacityUsageColor,
  getHitRateColor
} from '../cacheStatsUtils'

describe('CacheStats', () => {
  it('应正确计算命中率百分比并进行 0-100 限制', () => {
    expect(calcHitRatePercent(0)).toBe(0)
    expect(calcHitRatePercent(0.5)).toBe(50)
    expect(calcHitRatePercent(1)).toBe(100)
    expect(calcHitRatePercent(10)).toBe(100)
    expect(calcHitRatePercent(-1)).toBe(0)
    expect(calcHitRatePercent(Number.NaN)).toBe(0)
  })

  it('应正确计算容量使用率（capacity<=0 返回 null）', () => {
    expect(calcCapacityUsagePercent(0, 0)).toBe(null)
    expect(calcCapacityUsagePercent(10, -1)).toBe(null)
    expect(calcCapacityUsagePercent(50, 100)).toBe(50)
    expect(calcCapacityUsagePercent(200, 100)).toBe(100)
    expect(calcCapacityUsagePercent(-1, 100)).toBe(0)
    expect(calcCapacityUsagePercent(1, Number.NaN)).toBe(null)
  })

  it('应根据阈值返回命中率颜色', () => {
    expect(getHitRateColor(99)).toBe('success')
    expect(getHitRateColor(95)).toBe('success')
    expect(getHitRateColor(80)).toBe('warning')
    expect(getHitRateColor(79.9)).toBe('error')
    expect(getHitRateColor(Number.NaN)).toBe('error')
  })

  it('应根据阈值返回容量使用率颜色', () => {
    expect(getCapacityUsageColor(null)).toBe('grey')
    expect(getCapacityUsageColor(99)).toBe('error')
    expect(getCapacityUsageColor(95)).toBe('error')
    expect(getCapacityUsageColor(80)).toBe('warning')
    expect(getCapacityUsageColor(79.9)).toBe('success')
    expect(getCapacityUsageColor(Number.NaN)).toBe('success')
  })

  it('应格式化计数与时间戳', () => {
    expect(formatCount(0)).toBe('0')
    expect(formatCount(1000)).toBe('1,000')
    expect(formatCount(Number.NaN)).toBe('0')

    expect(formatLocalDateTime('')).toBe('')
    expect(formatLocalDateTime('invalid')).toBe('')
    expect(formatLocalDateTime('2026-01-01T00:00:00Z')).not.toBe('')
  })

  it('API 服务应包含 /cache/stats 调用', async () => {
    const apiSource = await readFile(new URL('../../services/api.ts', import.meta.url), 'utf8')
    expect(apiSource).toContain('getCacheStats')
    expect(apiSource).toContain("'/cache/stats'")
  })
})
