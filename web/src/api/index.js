import axios from 'axios'
import { clearAuth } from '../store'
import { joinBase } from '../base'

// baseURL 使用动态部署根：无前缀部署为 /api，子路径 /{name}/ 部署为 /{name}/api
const api = axios.create({ baseURL: joinBase('api'), timeout: 15000 })

api.interceptors.request.use((config) => {
  const token = localStorage.getItem('gw_token')
  if (token) config.headers.Authorization = `Bearer ${token}`
  return config
})

api.interceptors.response.use(
  (res) => res.data,
  (err) => {
    if (err.response && err.response.status === 401) {
      clearAuth()
      if (window.location.pathname !== joinBase('')) window.location.assign(joinBase(''))
    }
    return Promise.reject(err)
  }
)

export default {
  // 认证
  login: (data) => api.post('/auth/login', data),
  refresh: () => api.post('/auth/refresh'),
  changePassword: (data) => api.put('/auth/password', data),
  // 建站向导
  setupStatus: () => api.get('/setup/status'),
  setupConfigure: (data) => api.post('/setup/configure', data),
  // 站点设置
  getSettings: () => api.get('/settings'),
  updateSettings: (data) => api.put('/settings', data),
  // 路由
  listRoutes: () => api.get('/routes'),
  createRoute: (data) => api.post('/routes', data),
  updateRoute: (prefix, data) => api.put(`/routes/${prefix}`, data),
  deleteRoute: (prefix) => api.delete(`/routes/${prefix}`),
  updateRouteStatus: (prefix, status) => api.patch(`/routes/${prefix}/status`, { status }),
  // 统计
  overview: () => api.get('/stats/overview'),
  routesStats: (days = 7) => api.get('/stats/routes', { params: { days } }),
  trend: (prefix, days = 7) => api.get('/stats/trend', { params: { prefix, days } }),
  cleanup: (retainDays) => api.post('/stats/cleanup', { retain_days: retainDays }),
  // 地理
  geo: (days = 7) => api.get('/stats/geo', { params: { days } }),
  // 合规
  compliance: () => api.get('/compliance'),
  // 健康检查
  healthStatus: () => api.get('/health'),
  healthCheckNow: () => api.post('/health/check'),
  // 安全防护
  listIPRules: () => api.get('/security/ips'),
  createIPRule: (data) => api.post('/security/ips', data),
  deleteIPRule: (id) => api.delete(`/security/ips/${id}`),
  listAPIRules: () => api.get('/security/apis'),
  createAPIRule: (data) => api.post('/security/apis', data),
  deleteAPIRule: (id) => api.delete(`/security/apis/${id}`),
  // 数据库迁移
  migrateInfo: () => api.get('/migrate/info'),
  migrateTest: (data) => api.post('/migrate/test', data),
  migrateRun: (data) => api.post('/migrate/run', data),
  // 数据库备份
  backupCreate: () => api.post('/backup'),
  backupList: () => api.get('/backup/list'),
  backupDelete: (id) => api.delete(`/backup/${id}`)
}
