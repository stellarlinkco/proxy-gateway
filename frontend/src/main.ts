import { createApp } from 'vue'
import { createPinia } from 'pinia'
import piniaPluginPersistedstate from 'pinia-plugin-persistedstate'
import vuetify from './plugins/vuetify'
import api from './services/api'
import router from './router'
import App from './App.vue'
import './assets/style.css'
import { useAuthStore } from './stores/auth'

// 在应用创建前同步初始化认证，避免子组件先发请求触发 401 清空本地密钥
api.initializeAuth()

const app = createApp(App)

const pinia = createPinia()
pinia.use(piniaPluginPersistedstate)

app.use(pinia)
app.use(vuetify)
app.use(router)

// 初始化 AuthStore（从 localStorage 恢复状态）
const authStore = useAuthStore()
authStore.initializeAuth()

app.mount('#app')
