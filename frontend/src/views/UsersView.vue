<template>
  <v-container fluid>
    <!-- Header -->
    <v-card class="mb-4" style="border: 2px solid rgb(var(--v-theme-on-surface)); box-shadow: 6px 6px 0 0 rgb(var(--v-theme-on-surface));">
      <v-card-title class="d-flex align-center pa-4">
        <v-icon class="mr-3" size="28">mdi-account-multiple</v-icon>
        <span class="text-h6 font-weight-bold">User Management</span>
        <v-spacer />
        <v-btn color="primary" prepend-icon="mdi-account-plus" @click="openCreateDialog">
          Create User
        </v-btn>
      </v-card-title>
    </v-card>

    <!-- Error Alert -->
    <v-alert v-if="usersStore.error" type="error" variant="tonal" class="mb-4" closable @click:close="usersStore.error = null">
      {{ usersStore.error }}
    </v-alert>

    <!-- Users Table -->
    <v-card style="border: 2px solid rgb(var(--v-theme-on-surface)); box-shadow: 6px 6px 0 0 rgb(var(--v-theme-on-surface));">
      <v-data-table
        :headers="headers"
        :items="usersStore.users"
        :loading="usersStore.loading"
        item-value="id"
        hover
      >
        <template #item.role="{ item }">
          <v-chip
            :color="item.role === 'admin' ? 'primary' : 'default'"
            size="small"
            variant="tonal"
          >
            <v-icon start size="14">{{ item.role === 'admin' ? 'mdi-shield-account' : 'mdi-account' }}</v-icon>
            {{ item.role }}
          </v-chip>
        </template>

        <template #item.status="{ item }">
          <v-chip
            :color="item.status === 'active' ? 'success' : 'error'"
            size="small"
            variant="tonal"
          >
            {{ item.status }}
          </v-chip>
        </template>

        <template #item.daily_limit_cents="{ item }">
          {{ item.daily_limit_cents === 0 ? 'Unlimited' : formatCents(item.daily_limit_cents) }}
        </template>

        <template #item.monthly_limit_cents="{ item }">
          {{ item.monthly_limit_cents === 0 ? 'Unlimited' : formatCents(item.monthly_limit_cents) }}
        </template>

        <template #item.actions="{ item }">
          <div class="d-flex ga-1">
            <v-btn icon size="small" variant="text" @click="openEditDialog(item)" title="Edit">
              <v-icon size="18">mdi-pencil</v-icon>
            </v-btn>
            <v-btn icon size="small" variant="text" @click="openChannelsDialog(item)" title="Channel Permissions">
              <v-icon size="18">mdi-key-chain</v-icon>
            </v-btn>
            <v-btn icon size="small" variant="text" @click="openUsageDialog(item)" title="Usage Stats">
              <v-icon size="18">mdi-chart-line</v-icon>
            </v-btn>
            <v-btn icon size="small" variant="text" color="warning" @click="confirmRegenKey(item)" title="Regenerate API Key">
              <v-icon size="18">mdi-autorenew</v-icon>
            </v-btn>
            <v-btn icon size="small" variant="text" color="error" @click="confirmDelete(item)" title="Delete">
              <v-icon size="18">mdi-delete</v-icon>
            </v-btn>
          </div>
        </template>

        <template #no-data>
          <div class="text-center pa-8">
            <v-icon size="64" color="primary" class="mb-4">mdi-account-multiple</v-icon>
            <div class="text-h6 mb-2">No users yet</div>
            <div class="text-body-2 text-medium-emphasis mb-4">Create a user to get started with multi-user mode.</div>
            <v-btn color="primary" prepend-icon="mdi-account-plus" @click="openCreateDialog">Create User</v-btn>
          </div>
        </template>
      </v-data-table>
    </v-card>

    <!-- Create/Edit User Dialog -->
    <v-dialog v-model="showUserDialog" max-width="560" persistent>
      <v-card>
        <v-card-title class="d-flex align-center">
          <v-icon class="mr-3">{{ editingUser ? 'mdi-account-edit' : 'mdi-account-plus' }}</v-icon>
          {{ editingUser ? 'Edit User' : 'Create User' }}
        </v-card-title>
        <v-card-text>
          <v-form ref="userFormRef" @submit.prevent="saveUser">
            <v-text-field
              v-model="userForm.name"
              label="Name"
              variant="outlined"
              density="comfortable"
              :rules="[v => !!v || 'Name is required']"
              class="mb-2"
            />
            <v-text-field
              v-model="userForm.email"
              label="Email"
              variant="outlined"
              density="comfortable"
              type="email"
              :rules="[v => !!v || 'Email is required', v => /.+@.+\..+/.test(v) || 'Invalid email']"
              :disabled="!!editingUser"
              class="mb-2"
            />
            <v-select
              v-model="userForm.role"
              label="Role"
              variant="outlined"
              density="comfortable"
              :items="['user', 'admin']"
              class="mb-2"
            />
            <v-select
              v-if="editingUser"
              v-model="userForm.status"
              label="Status"
              variant="outlined"
              density="comfortable"
              :items="['active', 'disabled']"
              class="mb-2"
            />
            <v-row>
              <v-col cols="6">
                <v-text-field
                  v-model.number="userForm.daily_limit_cents"
                  label="Daily Limit (cents)"
                  variant="outlined"
                  density="comfortable"
                  type="number"
                  min="0"
                  hint="0 = unlimited"
                  persistent-hint
                />
              </v-col>
              <v-col cols="6">
                <v-text-field
                  v-model.number="userForm.monthly_limit_cents"
                  label="Monthly Limit (cents)"
                  variant="outlined"
                  density="comfortable"
                  type="number"
                  min="0"
                  hint="0 = unlimited"
                  persistent-hint
                />
              </v-col>
            </v-row>
          </v-form>
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn variant="text" @click="closeUserDialog">Cancel</v-btn>
          <v-btn color="primary" variant="elevated" :loading="saving" @click="saveUser">
            {{ editingUser ? 'Save' : 'Create' }}
          </v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <!-- Delete Confirmation Dialog -->
    <v-dialog v-model="showDeleteDialog" max-width="420">
      <v-card>
        <v-card-title class="d-flex align-center">
          <v-icon color="error" class="mr-3">mdi-account-remove</v-icon>
          Delete User
        </v-card-title>
        <v-card-text>
          Are you sure you want to delete <strong>{{ deletingUser?.name }}</strong> ({{ deletingUser?.email }})?
          This action cannot be undone. All usage data and channel permissions will be removed.
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn variant="text" @click="showDeleteDialog = false">Cancel</v-btn>
          <v-btn color="error" variant="elevated" :loading="saving" @click="doDelete">Delete</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <!-- API Key Display Dialog -->
    <v-dialog v-model="showApiKeyDialog" max-width="520" persistent>
      <v-card>
        <v-card-title class="d-flex align-center">
          <v-icon color="warning" class="mr-3">mdi-key</v-icon>
          API Key
        </v-card-title>
        <v-card-text>
          <v-alert type="warning" variant="tonal" class="mb-4" density="compact">
            Copy this API key now. It will not be shown again.
          </v-alert>
          <div class="d-flex align-center">
            <v-text-field
              :model-value="displayedApiKey"
              variant="outlined"
              density="comfortable"
              readonly
              :type="showKeyPlain ? 'text' : 'password'"
              class="flex-grow-1"
            >
              <template #append-inner>
                <v-btn icon size="small" variant="text" @click="showKeyPlain = !showKeyPlain">
                  <v-icon size="18">{{ showKeyPlain ? 'mdi-eye-off' : 'mdi-eye' }}</v-icon>
                </v-btn>
                <v-btn icon size="small" variant="text" @click="copyApiKey">
                  <v-icon size="18">mdi-content-copy</v-icon>
                </v-btn>
              </template>
            </v-text-field>
          </div>
          <div v-if="keyCopied" class="text-success text-caption mt-1">Copied to clipboard</div>
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn color="primary" variant="elevated" @click="showApiKeyDialog = false">Done</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <!-- Regenerate Key Confirmation Dialog -->
    <v-dialog v-model="showRegenDialog" max-width="420">
      <v-card>
        <v-card-title class="d-flex align-center">
          <v-icon color="warning" class="mr-3">mdi-autorenew</v-icon>
          Regenerate API Key
        </v-card-title>
        <v-card-text>
          Are you sure you want to regenerate the API key for <strong>{{ regenUser?.name }}</strong>?
          The current key will be invalidated immediately.
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn variant="text" @click="showRegenDialog = false">Cancel</v-btn>
          <v-btn color="warning" variant="elevated" :loading="saving" @click="doRegenKey">Regenerate</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <!-- Channel Permissions Dialog -->
    <v-dialog v-model="showChannelsDialog" max-width="600">
      <v-card>
        <v-card-title class="d-flex align-center">
          <v-icon class="mr-3">mdi-key-chain</v-icon>
          Channel Permissions - {{ channelsUser?.name }}
        </v-card-title>
        <v-card-text>
          <v-alert v-if="!userChannels.length && !channelsLoading" type="info" variant="tonal" density="compact" class="mb-4">
            No channels assigned. This user has access to all channels by default.
          </v-alert>
          <div v-if="channelsLoading" class="text-center pa-4">
            <v-progress-circular indeterminate />
          </div>
          <div v-else>
            <div v-for="channelType in ['messages', 'responses', 'gemini'] as const" :key="channelType" class="mb-4">
              <div class="text-subtitle-2 font-weight-bold mb-2 text-uppercase">{{ channelType }}</div>
              <div v-if="getAvailableChannels(channelType).length === 0" class="text-body-2 text-medium-emphasis ml-2">
                No {{ channelType }} channels configured.
              </div>
              <v-checkbox
                v-for="ch in getAvailableChannels(channelType)"
                :key="`${channelType}-${ch.index}`"
                :model-value="isChannelSelected(channelType, ch.index)"
                :label="`#${ch.index} ${ch.name}`"
                density="compact"
                hide-details
                @update:model-value="toggleChannel(channelType, ch.index, $event as boolean)"
              />
            </div>
          </div>
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn variant="text" @click="showChannelsDialog = false">Cancel</v-btn>
          <v-btn color="primary" variant="elevated" :loading="saving" @click="saveChannels">Save</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <!-- Usage Stats Dialog -->
    <v-dialog v-model="showUsageDialog" max-width="700">
      <v-card>
        <v-card-title class="d-flex align-center">
          <v-icon class="mr-3">mdi-chart-line</v-icon>
          Usage Statistics - {{ usageUser?.name }}
        </v-card-title>
        <v-card-text>
          <div v-if="usageLoading" class="text-center pa-4">
            <v-progress-circular indeterminate />
          </div>
          <div v-else-if="!usageData.length" class="text-center pa-4 text-medium-emphasis">
            No usage data available.
          </div>
          <v-table v-else density="compact">
            <thead>
              <tr>
                <th>Date</th>
                <th class="text-right">Requests</th>
                <th class="text-right">Input Tokens</th>
                <th class="text-right">Output Tokens</th>
                <th class="text-right">Cost</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="row in usageData" :key="row.date">
                <td>{{ row.date }}</td>
                <td class="text-right">{{ row.request_count.toLocaleString() }}</td>
                <td class="text-right">{{ row.input_tokens.toLocaleString() }}</td>
                <td class="text-right">{{ row.output_tokens.toLocaleString() }}</td>
                <td class="text-right">{{ formatCents(row.cost_cents) }}</td>
              </tr>
            </tbody>
          </v-table>
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn variant="text" @click="showUsageDialog = false">Close</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <!-- Toast -->
    <v-snackbar v-model="toast.show" :color="toast.color" :timeout="3000" location="top right">
      {{ toast.message }}
    </v-snackbar>
  </v-container>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useUsersStore, type UserResponse, type UserChannel, type UserUsage } from '@/stores/users'
import { useChannelStore } from '@/stores/channel'

const usersStore = useUsersStore()
const channelStore = useChannelStore()

// Table headers
const headers = [
  { title: 'Name', key: 'name', sortable: true },
  { title: 'Email', key: 'email', sortable: true },
  { title: 'Role', key: 'role', sortable: true },
  { title: 'Status', key: 'status', sortable: true },
  { title: 'Daily Limit', key: 'daily_limit_cents', sortable: true },
  { title: 'Monthly Limit', key: 'monthly_limit_cents', sortable: true },
  { title: 'Actions', key: 'actions', sortable: false, width: '220px' },
]

// Format cents to dollar display
function formatCents(cents: number): string {
  return `$${(cents / 100).toFixed(2)}`
}

// ===== Create/Edit Dialog =====
const showUserDialog = ref(false)
const editingUser = ref<UserResponse | null>(null)
const saving = ref(false)
const userFormRef = ref<{ validate: () => Promise<{ valid: boolean }> } | null>(null)
const userForm = ref({
  name: '',
  email: '',
  role: 'user',
  status: 'active',
  daily_limit_cents: 0,
  monthly_limit_cents: 0,
})

function openCreateDialog() {
  editingUser.value = null
  userForm.value = { name: '', email: '', role: 'user', status: 'active', daily_limit_cents: 0, monthly_limit_cents: 0 }
  showUserDialog.value = true
}

function openEditDialog(user: UserResponse) {
  editingUser.value = user
  userForm.value = {
    name: user.name,
    email: user.email,
    role: user.role,
    status: user.status,
    daily_limit_cents: user.daily_limit_cents,
    monthly_limit_cents: user.monthly_limit_cents,
  }
  showUserDialog.value = true
}

function closeUserDialog() {
  showUserDialog.value = false
  editingUser.value = null
}

async function saveUser() {
  if (userFormRef.value) {
    const { valid } = await userFormRef.value.validate()
    if (!valid) return
  }

  saving.value = true
  try {
    if (editingUser.value) {
      await usersStore.updateUser(editingUser.value.id, {
        name: userForm.value.name,
        role: userForm.value.role,
        status: userForm.value.status,
        daily_limit_cents: userForm.value.daily_limit_cents,
        monthly_limit_cents: userForm.value.monthly_limit_cents,
      })
      showToast('User updated', 'success')
    } else {
      const newUser = await usersStore.createUser({
        email: userForm.value.email,
        name: userForm.value.name,
        role: userForm.value.role,
        daily_limit_cents: userForm.value.daily_limit_cents,
        monthly_limit_cents: userForm.value.monthly_limit_cents,
      })
      // Show the API key from creation response
      if (newUser.api_key && !newUser.api_key.includes('****')) {
        displayedApiKey.value = newUser.api_key
        showApiKeyDialog.value = true
      }
      showToast('User created', 'success')
    }
    closeUserDialog()
  } catch (e) {
    showToast(e instanceof Error ? e.message : 'Operation failed', 'error')
  } finally {
    saving.value = false
  }
}

// ===== Delete Dialog =====
const showDeleteDialog = ref(false)
const deletingUser = ref<UserResponse | null>(null)

function confirmDelete(user: UserResponse) {
  deletingUser.value = user
  showDeleteDialog.value = true
}

async function doDelete() {
  if (!deletingUser.value) return
  saving.value = true
  try {
    await usersStore.deleteUser(deletingUser.value.id)
    showDeleteDialog.value = false
    showToast('User deleted', 'success')
  } catch (e) {
    showToast(e instanceof Error ? e.message : 'Delete failed', 'error')
  } finally {
    saving.value = false
  }
}

// ===== API Key Dialog =====
const showApiKeyDialog = ref(false)
const displayedApiKey = ref('')
const showKeyPlain = ref(false)
const keyCopied = ref(false)

async function copyApiKey() {
  try {
    await navigator.clipboard.writeText(displayedApiKey.value)
    keyCopied.value = true
    setTimeout(() => { keyCopied.value = false }, 2000)
  } catch {
    showToast('Failed to copy', 'error')
  }
}

// ===== Regenerate Key Dialog =====
const showRegenDialog = ref(false)
const regenUser = ref<UserResponse | null>(null)

function confirmRegenKey(user: UserResponse) {
  regenUser.value = user
  showRegenDialog.value = true
}

async function doRegenKey() {
  if (!regenUser.value) return
  saving.value = true
  try {
    const newKey = await usersStore.regenerateAPIKey(regenUser.value.id)
    showRegenDialog.value = false
    displayedApiKey.value = newKey
    showKeyPlain.value = false
    keyCopied.value = false
    showApiKeyDialog.value = true
    showToast('API key regenerated', 'success')
  } catch (e) {
    showToast(e instanceof Error ? e.message : 'Regeneration failed', 'error')
  } finally {
    saving.value = false
  }
}

// ===== Channel Permissions Dialog =====
const showChannelsDialog = ref(false)
const channelsUser = ref<UserResponse | null>(null)
const channelsLoading = ref(false)
const userChannels = ref<UserChannel[]>([])
const pendingChannels = ref<UserChannel[]>([])

interface SimpleChannel {
  index: number
  name: string
}

function getAvailableChannels(channelType: string): SimpleChannel[] {
  let data
  switch (channelType) {
    case 'messages': data = channelStore.channelsData; break
    case 'responses': data = channelStore.responsesChannelsData; break
    case 'gemini': data = channelStore.geminiChannelsData; break
    default: return []
  }
  return (data.channels || []).map(ch => ({ index: ch.index, name: ch.name }))
}

function isChannelSelected(channelType: string, channelIndex: number): boolean {
  return pendingChannels.value.some(
    ch => ch.channel_type === channelType && ch.channel_index === channelIndex
  )
}

function toggleChannel(channelType: string, channelIndex: number, selected: boolean) {
  if (selected) {
    pendingChannels.value.push({
      user_id: channelsUser.value?.id || '',
      channel_type: channelType,
      channel_index: channelIndex,
    })
  } else {
    pendingChannels.value = pendingChannels.value.filter(
      ch => !(ch.channel_type === channelType && ch.channel_index === channelIndex)
    )
  }
}

async function openChannelsDialog(user: UserResponse) {
  channelsUser.value = user
  channelsLoading.value = true
  showChannelsDialog.value = true
  try {
    userChannels.value = await usersStore.getUserChannels(user.id)
    pendingChannels.value = [...userChannels.value]
  } catch (e) {
    showToast(e instanceof Error ? e.message : 'Failed to load channels', 'error')
  } finally {
    channelsLoading.value = false
  }
}

async function saveChannels() {
  if (!channelsUser.value) return
  saving.value = true
  try {
    await usersStore.setUserChannels(channelsUser.value.id, pendingChannels.value)
    showChannelsDialog.value = false
    showToast('Channel permissions updated', 'success')
  } catch (e) {
    showToast(e instanceof Error ? e.message : 'Failed to save channels', 'error')
  } finally {
    saving.value = false
  }
}

// ===== Usage Stats Dialog =====
const showUsageDialog = ref(false)
const usageUser = ref<UserResponse | null>(null)
const usageLoading = ref(false)
const usageData = ref<UserUsage[]>([])

async function openUsageDialog(user: UserResponse) {
  usageUser.value = user
  usageLoading.value = true
  showUsageDialog.value = true
  try {
    usageData.value = await usersStore.getUserUsage(user.id)
  } catch (e) {
    showToast(e instanceof Error ? e.message : 'Failed to load usage', 'error')
  } finally {
    usageLoading.value = false
  }
}

// ===== Toast =====
const toast = ref({ show: false, message: '', color: 'success' })

function showToast(message: string, color: string = 'success') {
  toast.value = { show: true, message, color }
}

// ===== Init =====
onMounted(() => {
  usersStore.fetchUsers().catch(() => {})
})
</script>
