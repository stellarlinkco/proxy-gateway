import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api, type UserResponse, type CreateUserRequest, type UpdateUserRequest, type UserChannel, type UserUsage } from '@/services/api'

export type { UserResponse, CreateUserRequest, UpdateUserRequest, UserChannel, UserUsage }

export const useUsersStore = defineStore('users', () => {
  const users = ref<UserResponse[]>([])
  const loading = ref(false)
  const error = ref<string | null>(null)

  async function fetchUsers() {
    loading.value = true
    error.value = null
    try {
      users.value = await api.listUsers() || []
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'Failed to fetch users'
      throw e
    } finally {
      loading.value = false
    }
  }

  async function createUser(req: CreateUserRequest): Promise<UserResponse> {
    const user = await api.createUser(req)
    await fetchUsers()
    return user
  }

  async function updateUser(id: string, req: UpdateUserRequest): Promise<UserResponse> {
    const user = await api.updateUser(id, req)
    await fetchUsers()
    return user
  }

  async function deleteUser(id: string): Promise<void> {
    await api.deleteUser(id)
    await fetchUsers()
  }

  async function regenerateAPIKey(id: string): Promise<string> {
    const result = await api.regenerateUserAPIKey(id)
    await fetchUsers()
    return result.api_key
  }

  async function getUserChannels(id: string): Promise<UserChannel[]> {
    const result = await api.getUserChannels(id)
    return Array.isArray(result) ? result : []
  }

  async function setUserChannels(id: string, channels: UserChannel[]): Promise<void> {
    await api.setUserChannels(id, channels)
  }

  async function getUserUsage(id: string): Promise<UserUsage[]> {
    const result = await api.getUserUsage(id)
    return Array.isArray(result) ? result : []
  }

  return {
    users,
    loading,
    error,
    fetchUsers,
    createUser,
    updateUser,
    deleteUser,
    regenerateAPIKey,
    getUserChannels,
    setUserChannels,
    getUserUsage,
  }
})
