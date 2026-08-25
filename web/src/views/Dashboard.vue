<template>
  <div class="console">
    <!-- ============ 侧边导航栏 ============ -->
    <aside class="console-side">
      <div class="side-brand" @click="router.push('/')">
        <div class="gh-logo side-logo">🔗</div>
        <div class="side-brand-text">
          <div class="side-name">{{ siteName }}</div>
          <div class="side-sub">CONSOLE</div>
        </div>
      </div>

      <nav class="side-nav">
        <div
          v-for="item in navItems"
          :key="item.key"
          class="side-item"
          :class="{ active: activeTab === item.key }"
          @click="activeTab = item.key"
        >
          <span class="side-ico">{{ item.icon }}</span>
          <span class="side-label">{{ item.label }}</span>
        </div>
      </nav>

      <div class="side-foot">
        <div class="side-user">
          <el-avatar :size="34" style="background:linear-gradient(135deg,#22d3ee,#3b82f6);font-weight:700">{{ avatarChar }}</el-avatar>
          <div class="side-user-text">
            <div class="side-uname">{{ authState.username }}</div>
            <div class="side-role">ADMIN</div>
          </div>
        </div>
      </div>
    </aside>

    <!-- ============ 主区 ============ -->
    <div class="console-main">
      <header class="console-top">
        <div class="top-title">
          <div class="top-heading">{{ currentNav?.label }}</div>
          <div class="top-sub">{{ currentNav?.sub }}</div>
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
              <el-avatar :size="28" style="background:linear-gradient(135deg,#22d3ee,#3b82f6);font-weight:700">{{ avatarChar }}</el-avatar>
              <span class="uname">{{ authState.username }}</span>
              <el-icon><ArrowDown /></el-icon>
            </span>
            <template #dropdown>
              <el-dropdown-menu>
                <el-dropdown-item command="password">🔑 {{ t('dashboard.changePassword') }}</el-dropdown-item>
                <el-dropdown-item command="site">⚙️ {{ t('nav.site') }}</el-dropdown-item>
                <el-dropdown-item command="logout" divided>{{ t('common.logout') }}</el-dropdown-item>
              </el-dropdown-menu>
            </template>
          </el-dropdown>
        </div>
      </header>

      <main class="console-body">
        <RouteManage v-show="activeTab === 'routes'" />
        <Health v-show="activeTab === 'health'" />
        <StatsPanel v-show="activeTab === 'stats'" />
        <VisitorGeo v-show="activeTab === 'geo'" />
        <Security v-show="activeTab === 'security'" />
        <Migration v-show="activeTab === 'migration'" />
        <Backup v-show="activeTab === 'backup'" />
        <Compliance v-show="activeTab === 'compliance'" />
      </main>
    </div>

    <!-- 站点设置弹窗 -->
    <el-dialog v-model="showSite" :title="t('nav.site')" width="460px">
      <Settings @done="showSite = false" />
    </el-dialog>

    <!-- 修改密码弹窗 -->
    <el-dialog v-model="showPwd" :title="t('dashboard.changePassword')" width="420px" :close-on-click-modal="false">
      <el-form :model="pwdForm" label-width="90px" @submit.prevent>
        <el-form-item :label="t('dashboard.oldPassword')">
          <el-input v-model="pwdForm.old_password" type="password" show-password autocomplete="current-password" />
        </el-form-item>
        <el-form-item :label="t('dashboard.newPassword')">
          <el-input v-model="pwdForm.new_password" type="password" show-password autocomplete="new-password" />
        </el-form-item>
        <el-form-item :label="t('dashboard.confirmPassword')">
          <el-input v-model="pwdForm.confirm" type="password" show-password autocomplete="new-password" @keyup.enter="submitPwd" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="showPwd = false">{{ t('common.cancel') }}</el-button>
        <el-button type="primary" :loading="pwdLoading" @click="submitPwd">{{ t('common.confirm') }}</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, computed, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { ElMessage, ElMessageBox } from 'element-plus'
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
const showSite = ref(false)
const showPwd = ref(false)
const pwdLoading = ref(false)
const pwdForm = ref({ old_password: '', new_password: '', confirm: '' })
const siteName = ref('GatewayHub')
const locales = SUPPORTED_LOCALES

const navItems = computed(() => [
  { key: 'routes', icon: '📋', label: t('nav.routes'), sub: t('route.hotReload') },
  { key: 'health', icon: '🩺', label: t('nav.health'), sub: t('health.autoCheck') },
  { key: 'stats', icon: '📊', label: t('nav.stats'), sub: t('stats.trend') },
  { key: 'geo', icon: '🌍', label: t('nav.geo'), sub: t('geo.title') },
  { key: 'security', icon: '🛡️', label: t('nav.security'), sub: t('security.ddos') },
  { key: 'migration', icon: '🗄️', label: t('nav.migration'), sub: t('migration.title') },
  { key: 'backup', icon: '💾', label: t('nav.backup'), sub: t('backup.scheduled') },
  { key: 'compliance', icon: '⚖️', label: t('nav.compliance'), sub: t('compliance.title') }
])
const currentNav = computed(() => navItems.value.find((x) => x.key === activeTab.value))

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
    pwdForm.value = { old_password: '', new_password: '', confirm: '' }
    showPwd.value = true
  } else if (cmd === 'site') {
    showSite.value = true
  }
}

async function submitPwd() {
  const f = pwdForm.value
  if (!f.old_password || !f.new_password) {
    ElMessage.warning(t('dashboard.oldPassword') + ' / ' + t('dashboard.newPassword'))
    return
  }
  if (f.new_password.length < 6) {
    ElMessage.warning(t('dashboard.minLength'))
    return
  }
  if (f.new_password !== f.confirm) {
    ElMessage.warning(t('dashboard.pwdMismatch'))
    return
  }
  pwdLoading.value = true
  try {
    const res = await api.changePassword({ old_password: f.old_password, new_password: f.new_password })
    if (res.code === 0) {
      ElMessage.success(t('common.success'))
      showPwd.value = false
    } else {
      ElMessage.error(res.message || t('common.failed'))
    }
  } catch (e) {
    ElMessage.error(e?.response?.data?.message || t('common.failed'))
  } finally {
    pwdLoading.value = false
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
.console {
  display: flex;
  min-height: 100vh;
}

/* ============ 侧边栏 ============ */
.console-side {
  width: 216px;
  flex: none;
  display: flex;
  flex-direction: column;
  background: var(--gh-panel);
  backdrop-filter: blur(14px);
  border-right: 1px solid var(--gh-border);
  position: sticky;
  top: 0;
  height: 100vh;
  z-index: 50;
}
.side-brand {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 18px 16px 14px;
  cursor: pointer;
  user-select: none;
  border-bottom: 1px solid var(--gh-border);
}
.side-logo {
  width: 36px;
  height: 36px;
  font-size: 20px;
}
.side-brand-text { min-width: 0; }
.side-name {
  font-size: 15px;
  font-weight: 700;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  background: linear-gradient(90deg, var(--gh-primary-soft), var(--gh-primary2));
  -webkit-background-clip: text;
  background-clip: text;
  -webkit-text-fill-color: transparent;
}
.side-sub { font-size: 10px; letter-spacing: 2px; color: var(--gh-text-dim); }

.side-nav {
  flex: 1;
  overflow-y: auto;
  padding: 12px 10px;
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.side-item {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 10px 14px;
  border-radius: 10px;
  cursor: pointer;
  color: var(--gh-text-dim);
  font-size: 14px;
  font-weight: 500;
  border: 1px solid transparent;
  transition: all 0.2s ease;
  position: relative;
}
.side-item:hover {
  color: var(--gh-text);
  background: rgba(var(--gh-primary-rgb), 0.06);
}
.side-item.active {
  color: var(--gh-primary);
  background: linear-gradient(90deg, rgba(var(--gh-primary-rgb), 0.16), rgba(var(--gh-primary-rgb), 0.05));
  border-color: rgba(var(--gh-primary-rgb), 0.22);
  box-shadow: inset 0 0 0 1px rgba(var(--gh-primary-rgb), 0.06), 0 2px 12px rgba(var(--gh-primary-rgb), 0.10);
}
.side-item.active::before {
  content: '';
  position: absolute;
  left: -10px;
  top: 20%;
  height: 60%;
  width: 3px;
  border-radius: 0 3px 3px 0;
  background: linear-gradient(180deg, var(--gh-primary), var(--gh-primary2));
  box-shadow: 0 0 10px rgba(var(--gh-primary-rgb), 0.8);
}
.side-ico { font-size: 16px; width: 20px; text-align: center; }
.side-label { flex: 1; }

.side-foot {
  padding: 12px 14px;
  border-top: 1px solid var(--gh-border);
}
.side-user {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 8px;
  border-radius: 10px;
  background: rgba(var(--gh-primary-rgb), 0.05);
  border: 1px solid var(--gh-border);
}
.side-user-text { min-width: 0; }
.side-uname {
  font-size: 13px;
  font-weight: 600;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.side-role { font-size: 10px; letter-spacing: 1px; color: var(--gh-text-dim); }

/* ============ 主区 ============ */
.console-main { flex: 1; min-width: 0; display: flex; flex-direction: column; }
.console-top {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 14px 26px;
  background: var(--gh-panel);
  backdrop-filter: blur(14px);
  border-bottom: 1px solid var(--gh-border);
  position: sticky;
  top: 0;
  z-index: 40;
}
.top-heading {
  font-size: 18px;
  font-weight: 700;
  background: linear-gradient(120deg, var(--gh-primary-soft), var(--gh-primary2));
  -webkit-background-clip: text;
  background-clip: text;
  -webkit-text-fill-color: transparent;
}
.top-sub { font-size: 12px; color: var(--gh-text-dim); margin-top: 2px; }
.head-actions { display: flex; align-items: center; gap: 16px; }

.console-body {
  flex: 1;
  padding: 22px 26px 48px;
  max-width: 1320px;
  width: 100%;
  margin: 0 auto;
  box-sizing: border-box;
}

.user {
  display: flex;
  align-items: center;
  gap: 8px;
  color: var(--gh-text);
  cursor: pointer;
  outline: none;
}
.uname { font-size: 14px; font-weight: 600; }

@media (max-width: 900px) {
  .console-side { width: 68px; }
  .side-brand-text, .side-label, .side-user-text { display: none; }
  .side-item { justify-content: center; }
  .console-body { padding: 16px 14px 40px; }
}
</style>
