import { createApp } from 'vue'
import App from './App.vue'
import router from './router'
import { startThemeSync } from './utils/theme.ts'
import './style.css'

startThemeSync()

createApp(App).use(router).mount('#app')
