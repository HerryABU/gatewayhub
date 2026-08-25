import { ref } from 'vue'

const THEME_KEY = 'gw_theme'
const saved = localStorage.getItem(THEME_KEY) || 'light'

export const theme = ref(saved)

export function isDark() {
  return document.documentElement.classList.contains('dark')
}

export function applyTheme(t) {
  theme.value = t
  if (t === 'dark') {
    document.documentElement.classList.add('dark')
  } else {
    document.documentElement.classList.remove('dark')
  }
  localStorage.setItem(THEME_KEY, t)
}

export function toggleTheme() {
  applyTheme(theme.value === 'dark' ? 'light' : 'dark')
}

export function initTheme() {
  applyTheme(saved)
}

// 图表（ECharts）主题色，随明暗主题切换
export function chartColors() {
  return isDark()
    ? { axisLabel: '#8494ad', splitLine: '#1c2740', mapArea: '#16213a', mapBorder: '#334155' }
    : { axisLabel: '#64748b', splitLine: '#e2e8f0', mapArea: '#eef2f7', mapBorder: '#cbd5e1' }
}

