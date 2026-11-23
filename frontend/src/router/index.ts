import { createRouter, createWebHistory } from 'vue-router'
import { fetchStatus } from '../api/client'
import HomeView from '../views/HomeView.vue'
import LoginView from '../views/LoginView.vue'
import SettingsView from '../views/SettingsView.vue'
import SetupView from '../views/SetupView.vue'
import TasksView from '../views/TasksView.vue'
import WorkbenchView from '../views/WorkbenchView.vue'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', name: 'home', component: HomeView },
    { path: '/login', name: 'login', component: LoginView },
    { path: '/setup', name: 'setup', component: SetupView },
    { path: '/workbench', name: 'workbench', component: WorkbenchView },
    { path: '/tasks', name: 'tasks', component: TasksView },
    { path: '/settings', name: 'settings', component: SettingsView },
    { path: '/:pathMatch(.*)*', redirect: '/' },
  ],
})

// 每次进入页面前确认服务端状态：未初始化去初始化页，未登录去登录页。
router.beforeEach(async (to) => {
  try {
    const status = await fetchStatus()
    if (!status.initialized) {
      return to.name === 'setup' ? true : { name: 'setup' }
    }
    if (!status.authenticated) {
      return to.name === 'login' ? true : { name: 'login' }
    }
    if (to.name === 'login' || to.name === 'setup') {
      return { name: 'home' }
    }
    return true
  } catch {
    return to.name === 'login' ? true : { name: 'login' }
  }
})

export default router
