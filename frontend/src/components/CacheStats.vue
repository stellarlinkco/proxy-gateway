<template>
  <v-card elevation="0" rounded="lg" variant="outlined" class="cache-stats-card">
    <v-card-title class="d-flex align-center justify-space-between py-2">
      <div class="d-flex align-center">
        <v-icon class="mr-2" color="primary">mdi-database</v-icon>
        <span class="text-subtitle-1 font-weight-bold">缓存统计</span>
        <v-chip
          v-if="autoRefresh"
          size="x-small"
          color="primary"
          variant="tonal"
          class="ml-2"
        >
          自动刷新 {{ refreshIntervalSeconds }}s
        </v-chip>
      </div>

      <div class="d-flex align-center ga-2">
        <v-progress-circular v-if="isLoading" indeterminate size="16" width="2" color="primary" />
        <v-btn
          icon
          size="x-small"
          variant="text"
          :disabled="isLoading"
          title="刷新"
          @click="refresh"
        >
          <v-icon size="small">mdi-refresh</v-icon>
        </v-btn>
      </div>
    </v-card-title>

    <v-divider />

    <v-card-text class="py-3">
      <v-alert v-if="errorMessage" type="error" variant="tonal" density="compact" class="mb-3">
        {{ errorMessage }}
      </v-alert>

      <div v-if="!modelsStats" class="d-flex align-center justify-center text-caption text-medium-emphasis" style="height: 120px">
        暂无缓存数据
      </div>

      <v-row v-else dense>
        <!-- 命中率 -->
        <v-col cols="12" sm="4">
          <div class="metric-box">
            <div class="text-caption text-medium-emphasis mb-1">命中率</div>
            <div class="d-flex align-center ga-3">
              <v-progress-circular
                :model-value="hitRatePercent"
                :size="56"
                :width="6"
                :color="hitRateColor"
              >
                <span class="text-caption font-weight-bold">{{ hitRatePercent.toFixed(0) }}%</span>
              </v-progress-circular>

              <div class="text-caption">
                <div>命中 {{ formatCount(modelsStats.readHit) }}</div>
                <div>未命中 {{ formatCount(modelsStats.readMiss) }}</div>
                <div>总读 {{ formatCount(reads) }}</div>
              </div>
            </div>
          </div>
        </v-col>

        <!-- 读写次数 -->
        <v-col cols="12" sm="4">
          <div class="metric-box">
            <div class="text-caption text-medium-emphasis mb-1">读写次数</div>
            <div class="text-h6 font-weight-bold">{{ formatCount(reads + writes) }}</div>
            <div class="text-caption text-medium-emphasis">
              读 {{ formatCount(reads) }} · 写 {{ formatCount(writes) }}
            </div>
            <div class="text-caption text-medium-emphasis">
              新增 {{ formatCount(modelsStats.writeSet) }} · 更新 {{ formatCount(modelsStats.writeUpdate) }}
            </div>
          </div>
        </v-col>

        <!-- 容量使用率 -->
        <v-col cols="12" sm="4">
          <div class="metric-box">
            <div class="text-caption text-medium-emphasis mb-1">容量使用率</div>
            <template v-if="capacityUsagePercent !== null">
              <div class="d-flex align-center justify-space-between mb-1">
                <span class="text-caption">
                  {{ formatCount(modelsStats.entries) }}/{{ formatCount(modelsStats.capacity) }}
                </span>
                <span class="text-caption font-weight-bold">{{ capacityUsagePercent.toFixed(1) }}%</span>
              </div>
              <v-progress-linear
                :model-value="capacityUsagePercent"
                height="8"
                rounded
                :color="capacityUsageColor"
              />
            </template>
            <div v-else class="text-caption text-medium-emphasis">未启用容量限制</div>
          </div>
        </v-col>
      </v-row>

      <div v-if="lastUpdatedAt" class="text-caption text-disabled mt-2">
        更新时间：{{ lastUpdatedAt }}
      </div>
    </v-card-text>
  </v-card>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { api, type CacheStats, type CacheStatsResponse } from '../services/api'
import {
  calcCapacityUsagePercent,
  calcHitRatePercent,
  formatCount,
  formatLocalDateTime,
  getCapacityUsageColor,
  getHitRateColor
} from './cacheStatsUtils'

const props = withDefaults(defineProps<{
  /** 自动刷新间隔（毫秒），默认 15s */
  refreshIntervalMs?: number
  /** 是否启用自动刷新 */
  autoRefresh?: boolean
}>(), {
  refreshIntervalMs: 15000,
  autoRefresh: true
})

const isLoading = ref(false)
const errorMessage = ref<string>('')
const data = ref<CacheStatsResponse | null>(null)

let refreshTimer: ReturnType<typeof setInterval> | null = null

const refreshIntervalSeconds = computed(() => Math.max(1, Math.round(props.refreshIntervalMs / 1000)))

const modelsStats = computed<CacheStats | null>(() => data.value?.models ?? null)

const reads = computed(() => {
  if (!modelsStats.value) return 0
  return modelsStats.value.readHit + modelsStats.value.readMiss
})

const writes = computed(() => {
  if (!modelsStats.value) return 0
  return modelsStats.value.writeSet + modelsStats.value.writeUpdate
})

const hitRatePercent = computed(() => {
  if (!modelsStats.value) return 0
  return calcHitRatePercent(modelsStats.value.hitRate)
})

const capacityUsagePercent = computed<number | null>(() => {
  if (!modelsStats.value) return null
  return calcCapacityUsagePercent(modelsStats.value.entries, modelsStats.value.capacity)
})

const hitRateColor = computed(() => {
  return getHitRateColor(hitRatePercent.value)
})

const capacityUsageColor = computed(() => {
  return getCapacityUsageColor(capacityUsagePercent.value)
})

const lastUpdatedAt = computed(() => {
  return formatLocalDateTime(data.value?.timestamp || '')
})

const refresh = async () => {
  if (isLoading.value) return
  isLoading.value = true
  errorMessage.value = ''
  try {
    data.value = await api.getCacheStats()
  } catch (err) {
    const message = err instanceof Error ? err.message : '未知错误'
    errorMessage.value = `获取缓存统计失败：${message}`
  } finally {
    isLoading.value = false
  }
}

const startAutoRefresh = () => {
  stopAutoRefresh()
  if (!props.autoRefresh) return
  refreshTimer = setInterval(() => {
    if (!isLoading.value) {
      refresh()
    }
  }, props.refreshIntervalMs)
}

const stopAutoRefresh = () => {
  if (refreshTimer) {
    clearInterval(refreshTimer)
    refreshTimer = null
  }
}

onMounted(() => {
  refresh()
  startAutoRefresh()
})

onUnmounted(() => {
  stopAutoRefresh()
})

defineExpose({ refresh })
</script>

<style scoped>
.cache-stats-card {
  background: rgb(var(--v-theme-surface));
}

.metric-box {
  min-height: 96px;
}
</style>
