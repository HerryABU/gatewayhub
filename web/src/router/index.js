import { createRouter, createWebHistory } from 'vue-router'
import { isAuthed } from '../store'
import api from '../api'
import Home from '../views/Home.vue'
import Dashboard from '../views/Dashboard.vue'
import SetupWizard from '../views/SetupWizard.vue'

const routes = [
  { path: '/', name: 'home', component: Home },
  { path: '/dashboard', name: 'dashboard', component: Dashboard },
  { path: '/setup', name: 'setup', component: SetupWizard }
]

const router = createRouter({
  history: createWebHistory(),
  routes
})

// 建站状态缓存（首次加载时获取一次）
let setupChecked = false
let setupConfigured = true

async function checkSetup() {
  if (setupChecked) return setupConfigured
  try {
    const res = await api.setupStatus()
    setupConfigured = res.data?.configured !== false
    setupChecked = true
  } catch (e) {
    setupConfigured = true
    setupChecked = true
  }
  return setupConfigured
}

router.beforeEach(async (to) => {
  const configured = await checkSetup()

  // 未完成建站 → 强制进入向导
  if (!configured && to.name !== 'setup') {
    return { name: 'setup' }
  }
  // 已完成建站 → 向导永久关闭
  if (configured && to.name === 'setup') {
    return { name: 'home' }
  }
  // 管理员后台需登录
  if (to.name === 'dashboard' && !isAuthed()) {
    return { name: 'home' }
  }
  return true
})

export default router
