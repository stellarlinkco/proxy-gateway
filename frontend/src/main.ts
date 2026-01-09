import { createApp } from 'vue'
import vuetify from './plugins/vuetify'
import api from './services/api'
import App from './App.vue'
import './assets/style.css' // Tailwind + DaisyUI

// 在应用创建前同步初始化认证，避免子组件先发请求触发 401 清空本地密钥
api.initializeAuth()

const app = createApp(App)

app.use(vuetify)

app.mount('#app')
