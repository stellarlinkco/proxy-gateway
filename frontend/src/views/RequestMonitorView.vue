<template>
  <div class="request-monitor-view">
    <div class="d-flex align-center justify-space-between flex-wrap ga-3 mb-4">
      <div class="d-flex align-center ga-2">
        <v-icon color="primary">mdi-pulse</v-icon>
        <h2 class="text-h6 text-md-h5 font-weight-bold mb-0">请求监控</h2>
      </div>

      <v-tabs v-model="apiTypeModel" color="primary" density="compact" grow class="api-type-tabs">
        <v-tab value="messages">Claude</v-tab>
        <v-tab value="responses">Codex</v-tab>
        <v-tab value="gemini">Gemini</v-tab>
      </v-tabs>
    </div>

    <v-row dense class="ga-4">
      <v-col cols="12">
        <LiveRequestMonitor :api-type="apiTypeModel" />
      </v-col>
      <v-col cols="12">
        <RequestLogList :api-type="apiTypeModel" />
      </v-col>
    </v-row>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import LiveRequestMonitor from '../components/LiveRequestMonitor.vue'
import RequestLogList from '../components/RequestLogList.vue'
import type { ApiType } from '../services/api'

const props = defineProps<{
  apiType: ApiType
}>()

const emit = defineEmits<{
  (e: 'update:apiType', value: ApiType): void
}>()

const apiTypeModel = computed<ApiType>({
  get: () => props.apiType,
  set: value => emit('update:apiType', value),
})
</script>

<style scoped>
.api-type-tabs {
  min-width: 320px;
}

@media (max-width: 600px) {
  .api-type-tabs {
    width: 100%;
    min-width: 0;
  }
}
</style>
