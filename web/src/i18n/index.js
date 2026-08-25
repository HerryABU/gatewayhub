import { createI18n } from 'vue-i18n'
import en from './en'
import zh from './zh'

export const SUPPORTED_LOCALES = [
  { value: 'en-US', label: 'English' },
  { value: 'zh-CN', label: '简体中文' }
]

const saved = localStorage.getItem('gw_locale') || 'zh-CN'

const i18n = createI18n({
  legacy: false,
  globalInjection: true,
  locale: saved,
  fallbackLocale: 'en-US',
  messages: {
    'en-US': en,
    'zh-CN': zh
  }
})

export function setLocale(locale) {
  i18n.global.locale.value = locale
  localStorage.setItem('gw_locale', locale)
  document.documentElement.lang = locale
}

export default i18n
