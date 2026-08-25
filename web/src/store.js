import { reactive } from 'vue'

export const authState = reactive({
  token: localStorage.getItem('gw_token') || '',
  username: localStorage.getItem('gw_user') || '',
  role: localStorage.getItem('gw_role') || ''
})

export function setAuth(data) {
  authState.token = data.token
  authState.username = data.username
  authState.role = data.role
  localStorage.setItem('gw_token', data.token)
  localStorage.setItem('gw_user', data.username)
  localStorage.setItem('gw_role', data.role)
}

export function clearAuth() {
  authState.token = ''
  authState.username = ''
  authState.role = ''
  localStorage.removeItem('gw_token')
  localStorage.removeItem('gw_user')
  localStorage.removeItem('gw_role')
}

export function isAuthed() {
  return !!authState.token
}
