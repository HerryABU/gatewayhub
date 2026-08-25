<template>
  <div>
    <header class="gh-header">
      <div class="gh-brand" @click="$router.push('/')">
        <div class="gh-logo">🔗</div>
        <div>
          <div class="gh-brand-name">{{ siteName }}</div>
          <div class="gh-brand-sub">CONSOLE</div>
        </div>
      </div>
      <div class="head-actions">
        <button class="gh-theme-btn" :title="t('common.theme')" @click="toggleTheme">{{ theme === 'dark' ? '☀️' : '🌙' }}</button>
        <el-dropdown @command="onLocale">
          <span class="gh-lang">🌐 {{ currentLocaleLabel }} <el-icon><ArrowDown /></el-icon></span>
          <template #dropdown>
            <el-dropdown-menu>
              <el-dropdown-item v-for="l in locales" :key="l.value" :command="l.value">{{ l.label }}</el-dropdown-item>
            </el-dropdown-menu>
          </template>
        </el-dropdown>
        <el-dropdown @command="onCommand">
          <span class="user">
            <el-avatar :size="28" style="background:linear-gradient(135deg,#22d3ee,#3b82f6)">{{ avatarChar }}</el-avatar>
            <span class="uname">{{ authState.username }}</span>
            <el-icon><ArrowDown /></el-icon>
          </span>
          <template #dropdown>
            <el-dropdown-menu>
              <el-dropdown-item command="password">{{ t('nav.site') }}</el-dropdown-item>
              <el-dropdown-item command="home">{{ t('home.enterConsole') }}</el-dropdown-item>
              <el-dropdown-item command="logout" divided>{{ t('common.logout') }}</el-dropdown-item>
            </el-dropdown-menu>
          </template>
        </el-dropdown>
      </div>
    </header>

    <main class="main">
      <el-tabs v-model="activeTab" class="gh-tabs">
        <el-tab-pane :name="'routes'">
          <template #label><span>📋 {{ t('nav.routes') }}</span></template>
          <RouteManage />
        </el-tab-pane>
        <el-tab-pane :name="'health'">
          <template #label><span>🩺 {{ t('nav.health') }}</span></template>
          <Health />
        </el-tab-pane>
        <el-tab-pane :name="'stats'">
          <template #label><span>📊 {{ t('nav.stats') }}</span></template>
          <StatsPanel />
        </el-tab-pane>
        <el-tab-pane :name="'geo'">
          <template #label><span>🌍 {{ t('nav.geo') }}</span></template>
          <VisitorGeo />
        </el-tab-pane>
        <el-tab-pane :name="'security'">
          <template #label><span>🛡️ {{ t('nav.security') }}</span></template>
          <Security />
        </el-tab-pane>
        <el-tab-pane :name="'migration'">
          <template #label><span>🗄️ {{ t('nav.migration') }}</span></template>
          <Migration />
        </el-tab-pane>
        <el-tab-pane :name="'backup'">
          <template #label><span>💾 {{ t('nav.backup') }}</span></template>
          <Backup />
        </el-tab-pane>
        <el-tab-pane :name="'compliance'">
          <template #label><span>⚖️ {{ t('nav.compliance') }}</span></template>
          <Compliance />
        </el-tab-pane>
      </el-tabs>
    </main>

    <el-dialog v-model="showPwd" :title="t('nav.site')" width="440px">
      <Settings @done="showPwd = false" />
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, computed, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { ElMessageBox } from 'element-plus'
import api from '../api'
import { authState, clearAuth } from '../store'
import { SUPPORTED_LOCALES, setLocale } from '../i18n'
import { theme, toggleTheme } from '../theme'
import RouteManage from './RouteManage.vue'
import StatsPanel from './StatsPanel.vue'
import VisitorGeo from './VisitorGeo.vue'
import Compliance from './Compliance.vue'
import Security from './Security.vue'
import Migration from './Migration.vue'
import Backup from './Backup.vue'
import Health from './Health.vue'
import Settings from './Settings.vue'

const { t, locale } = useI18n()
const router = useRouter()
const activeTab = ref('routes')
const showPwd = ref(false)
const siteName = ref('GatewayHub')
const locales = SUPPORTED_LOCALES

const currentLocaleLabel = computed(() => {
  const l = locales.find((x) => x.value === locale.value)
  return l ? l.label : locale.value
})
const avatarChar = computed(() => (authState.username || 'A').charAt(0).toUpperCase())

function onLocale(v) {
  setLocale(v)
}

async function onCommand(cmd) {
  if (cmd === 'logout') {
    await ElMessageBox.confirm(t('common.logout') + '?', t('common.warning'), { type: 'warning' })
    clearAuth()
    router.push('/')
  } else if (cmd === 'password') {
    showPwd.value = true
  } else if (cmd === 'home') {
    router.push('/')
  }
}

onMounted(async () => {
  try {
    const res = await api.getSettings()
    if (res.code === 0 && res.data.site_name) siteName.value = res.data.site_name
  } catch (e) {}
})
</script>

<style scoped>
.main {
  max-width: 1240px;
  margin: 0 auto;
  padding: 20px 20px 40px;
}
.head-actions {
  display: flex;
  align-items: center;
  gap: 16px;
}
.user {
  display: flex;
  align-items: center;
  gap: 8px;
  color: var(--gh-text);
  cursor: pointer;
  outline: none;
}
.uname { font-size: 14px; }
.gh-tabs :deep(.el-tabs__nav-wrap::after) { background: var(--gh-border); }
.gh-tabs :deep(.el-tabs__item) { color: var(--gh-text-dim); font-size: 14px; }
.gh-tabs :deep(.el-tabs__item.is-active) { color: var(--gh-primary); }
</style>
