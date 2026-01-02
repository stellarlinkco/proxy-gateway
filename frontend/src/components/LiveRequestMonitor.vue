<template>
  <v-card elevation="1" rounded="lg">
    <v-card-title class="d-flex align-center justify-space-between py-3">
      <div class="d-flex align-center">
        <v-icon class="mr-2" color="primary">mdi-pulse</v-icon>
        <span class="text-subtitle-1">实时请求</span>
        <v-chip size="x-small" class="ml-2" variant="tonal">{{ apiType }}</v-chip>
        <v-chip size="x-small" class="ml-2" color="info" variant="tonal">
          {{ requests.length }}
        </v-chip>
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
      <v-list v-if="requests.length" density="compact">
        <v-list-item v-for="req in requests" :key="req.requestId">
          <v-list-item-title class="text-body-2">
            {{ req.channelName }} · {{ req.model || '--' }}
          </v-list-item-title>
          <v-list-item-subtitle class="text-caption text-medium-emphasis">
            {{ req.requestId }}
          </v-list-item-subtitle>
          <template #append>
            <div class="d-flex align-center ga-2">
              <v-chip v-if="req.isStreaming" size="x-small" color="info" variant="tonal">
                streaming
              </v-chip>
              <span class="text-caption font-mono">{{ req.keyMask }}</span>
              <span class="text-caption text-medium-emphasis">{{ formatElapsed(req.startTime) }}</span>
            </div>
          </template>
        </v-list-item>
      </v-list>

      <div v-else class="pa-4 text-center text-caption text-medium-emphasis">
        当前没有进行中的请求
      </div>
    </v-card-text>
  </v-card>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { api, type ApiType, type LiveRequest } from '../services/api'

const props = defineProps<{
  apiType: ApiType
}>()

const requests = ref<LiveRequest[]>([])
const loading = ref(false)
const error = ref<unknown>(null)

let refreshTimer: number | undefined
let tickTimer: number | undefined
let requestSeq = 0
const nowMs = ref(Date.now())

const errorMessage = computed(() => {
  if (!error.value) return ''
  if (error.value instanceof Error) return error.value.message
  return String(error.value)
})

function formatElapsed(startTime: string) {
  const startedAt = new Date(startTime).getTime()
  if (!Number.isFinite(startedAt)) return '--'
  const elapsedMs = Math.max(0, nowMs.value - startedAt)
  return `${(elapsedMs / 1000).toFixed(1)}s`
}

async function fetchLiveRequests() {
  const seq = ++requestSeq
  loading.value = true
  error.value = null
  try {
    const resp = await api.getLiveRequests(props.apiType)
    if (seq !== requestSeq) return
    requests.value = resp.requests || []
  } catch (err) {
    if (seq !== requestSeq) return
    error.value = err
    requests.value = []
  } finally {
    if (seq === requestSeq) loading.value = false
  }
}

function refresh() {
  fetchLiveRequests()
}

function stopTimers() {
  if (refreshTimer) {
    window.clearInterval(refreshTimer)
    refreshTimer = undefined
  }
  if (tickTimer) {
    window.clearInterval(tickTimer)
    tickTimer = undefined
  }
}

function startTimers() {
  stopTimers()
  refreshTimer = window.setInterval(() => {
    if (!loading.value) {
      fetchLiveRequests()
    }
  }, 2000)
  tickTimer = window.setInterval(() => {
    nowMs.value = Date.now()
  }, 200)
}

watch(() => props.apiType, () => {
  fetchLiveRequests()
})

onMounted(() => {
  fetchLiveRequests()
  startTimers()
})

onUnmounted(() => {
  stopTimers()
})
</script>

<style scoped>
.font-mono {
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', 'Courier New', monospace;
}
</style>
