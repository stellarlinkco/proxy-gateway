<template>
  <v-card elevation="1" rounded="lg">
    <v-card-title class="d-flex align-center justify-space-between py-3">
      <div class="d-flex align-center">
        <v-icon class="mr-2" color="primary">mdi-format-list-bulleted</v-icon>
        <span class="text-subtitle-1">请求日志</span>
        <v-chip size="x-small" class="ml-2" variant="tonal">{{ apiType }}</v-chip>
      </div>
      <div class="d-flex align-center ga-2">
        <v-btn
          icon
          size="x-small"
          variant="text"
          :disabled="loading"
          title="刷新"
          @click="refresh"
        >
          <v-icon size="small">mdi-refresh</v-icon>
        </v-btn>
        <v-switch
          v-model="autoRefresh"
          label="自动刷新"
          density="compact"
          color="primary"
          hide-details
        />
        <v-progress-circular v-if="loading" indeterminate size="16" width="2" color="primary" />
      </div>
    </v-card-title>

    <v-divider />

    <v-alert
      v-if="errorMessage"
      type="error"
      variant="tonal"
      density="compact"
      class="ma-3"
    >
      {{ errorMessage }}
    </v-alert>

    <v-card-text class="pa-0">
      <v-data-table-server
        :headers="headers"
        :items="logs"
        :items-length="total"
        :loading="loading"
        :items-per-page="itemsPerPage"
        :page="page"
        :items-per-page-options="[25, 50, 100]"
        density="compact"
        fixed-header
        hover
        height="420"
        @update:options="onOptionsUpdate"
      >
        <template #item.timestamp="{ item }">
          <span class="text-caption">{{ formatTimestamp(item.timestamp) }}</span>
        </template>

        <template #item.channelName="{ item }">
          <span class="text-body-2">{{ item.channelName }}</span>
        </template>

        <template #item.keyMask="{ item }">
          <span class="text-caption font-mono">{{ item.keyMask }}</span>
        </template>

        <template #item.model="{ item }">
          <span class="text-caption">{{ item.model || '--' }}</span>
        </template>

        <template #item.statusCode="{ item }">
          <v-tooltip v-if="item.errorMessage" location="top" :open-delay="200">
            <template #activator="{ props }">
              <v-chip
                v-bind="props"
                size="x-small"
                :color="getStatusColor(item.statusCode)"
                variant="tonal"
                class="justify-center"
                style="min-width: 54px"
              >
                {{ item.statusCode }}
              </v-chip>
            </template>
            <div class="text-caption" style="max-width: 360px; white-space: normal">
              {{ item.errorMessage }}
            </div>
          </v-tooltip>
          <v-chip
            v-else
            size="x-small"
            :color="getStatusColor(item.statusCode)"
            variant="tonal"
            class="justify-center"
            style="min-width: 54px"
          >
            {{ item.statusCode }}
          </v-chip>
        </template>

        <template #item.durationMs="{ item }">
          <span class="text-caption">{{ formatDurationMs(item.durationMs) }}</span>
        </template>

        <template #item.tokens="{ item }">
          <span class="text-caption">
            {{ formatTokens(item.inputTokens, item.outputTokens) }}
          </span>
        </template>

        <template #item.costCents="{ item }">
          <span class="text-caption">{{ formatCost(item.costCents) }}</span>
        </template>
      </v-data-table-server>
    </v-card-text>
  </v-card>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { api, type ApiType, type RequestLogRecord } from '../services/api'

const props = defineProps<{
  apiType: ApiType
}>()

const headers = [
  { title: '时间', key: 'timestamp', width: '140px', sortable: false },
  { title: '渠道', key: 'channelName', width: '120px', sortable: false },
  { title: 'Key', key: 'keyMask', width: '100px', sortable: false },
  { title: '模型', key: 'model', width: '180px', sortable: false },
  { title: '状态', key: 'statusCode', width: '70px', align: 'center', sortable: false },
  { title: '耗时', key: 'durationMs', width: '80px', align: 'end', sortable: false },
  { title: 'Token', key: 'tokens', width: '120px', align: 'end', sortable: false },
  { title: '成本', key: 'costCents', width: '80px', align: 'end', sortable: false },
] as const

const logs = ref<RequestLogRecord[]>([])
const total = ref(0)
const loading = ref(false)
const error = ref<unknown>(null)

const page = ref(1)
const itemsPerPage = ref(50)

const autoRefresh = ref(true)
let refreshTimer: number | undefined
let requestSeq = 0

const errorMessage = computed(() => {
  if (!error.value) return ''
  if (error.value instanceof Error) return error.value.message
  return String(error.value)
})

function getStatusColor(statusCode: number) {
  if (statusCode >= 200 && statusCode < 300) return 'success'
  if (statusCode >= 400 && statusCode < 500) return 'warning'
  if (statusCode >= 500) return 'error'
  return 'grey'
}

function formatTimestamp(timestamp: string) {
  const date = new Date(timestamp)
  if (Number.isNaN(date.getTime())) return timestamp
  return new Intl.DateTimeFormat('zh-CN', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
  }).format(date)
}

function formatDurationMs(durationMs: number) {
  if (!Number.isFinite(durationMs)) return '--'
  if (durationMs < 1000) return `${Math.round(durationMs)}ms`
  return `${(durationMs / 1000).toFixed(2)}s`
}

function formatTokens(inputTokens: number, outputTokens: number) {
  const input = Number.isFinite(inputTokens) ? inputTokens : 0
  const output = Number.isFinite(outputTokens) ? outputTokens : 0
  return `${input} / ${output}`
}

function formatCost(costCents: number) {
  if (!Number.isFinite(costCents)) return '--'
  return `$${(costCents / 100).toFixed(2)}`
}

async function fetchLogs() {
  const seq = ++requestSeq
  loading.value = true
  error.value = null
  try {
    const limit = itemsPerPage.value
    const offset = (page.value - 1) * limit
    const resp = await api.getRequestLogs(props.apiType, limit, offset)
    if (seq !== requestSeq) return
    logs.value = resp.logs || []
    total.value = resp.total || 0
  } catch (err) {
    if (seq !== requestSeq) return
    error.value = err
  } finally {
    if (seq === requestSeq) loading.value = false
  }
}

function refresh() {
  fetchLogs()
}

function onOptionsUpdate(options: any) {
  const nextPage = options.page || 1
  const nextItemsPerPage = options.itemsPerPage || 50

  const pageChanged = nextPage !== page.value
  const itemsPerPageChanged = nextItemsPerPage !== itemsPerPage.value

  page.value = nextPage
  itemsPerPage.value = nextItemsPerPage

  if (pageChanged || itemsPerPageChanged) {
    fetchLogs()
  }
}

function stopAutoRefresh() {
  if (refreshTimer) {
    window.clearInterval(refreshTimer)
    refreshTimer = undefined
  }
}

function startAutoRefresh() {
  stopAutoRefresh()
  refreshTimer = window.setInterval(() => {
    if (!loading.value) {
      fetchLogs()
    }
  }, 5000)
}

watch(() => props.apiType, () => {
  page.value = 1
  fetchLogs()
})

watch(autoRefresh, enabled => {
  if (enabled) startAutoRefresh()
  else stopAutoRefresh()
})

onMounted(() => {
  fetchLogs()
  if (autoRefresh.value) startAutoRefresh()
})

onUnmounted(() => {
  stopAutoRefresh()
})
</script>

<style scoped>
.font-mono {
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', 'Courier New', monospace;
}
</style>
