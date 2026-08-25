<template>
  <div>
    <header class="gh-header">
      <div class="gh-brand" @click="$router.push('/')">
        <div class="gh-logo">🔗</div>
        <div>
          <div class="gh-brand-name">{{ siteName }}</div>
          <div class="gh-brand-sub">{{ t('app.subtitle') }}</div>
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
        <template v-if="authState.token">
          <el-button type="primary" size="small" @click="$router.push('/dashboard')">{{ t('home.enterConsole') }}</el-button>
        </template>
        <template v-else>
          <el-button type="primary" size="small" @click="showLogin = true">{{ t('common.login') }}</el-button>
        </template>
      </div>
    </header>

    <main class="main">
      <!-- 已开放服务（对外项目，访客可直接访问） -->
      <el-card class="gh-panel" shadow="never">
        <template #header>
          <div class="panel-head">
            <span class="gh-title">🌐 {{ t('home.openServices') }}</span>
            <div style="display:flex;gap:10px;align-items:center">
              <span class="gh-dim">{{ t('home.autoRefresh') }}</span>
              <el-button size="small" :icon="Refresh" :loading="loading" @click="load">{{ t('common.refresh') }}</el-button>
            </div>
          </div>
        </template>

        <div v-if="openRoutes.length" class="open-grid">
          <a
            v-for="r in openRoutes"
            :key="r.prefix"
            class="open-card"
            :href="'/' + r.prefix + '/'"
            target="_blank"
            rel="noopener"
          >
            <div class="open-top">
              <span class="open-name">{{ r.name }}</span>
              <span :class="'gh-badge ' + healthColor(r.health)">{{ healthText(r.health) }}</span>
            </div>
            <div class="open-prefix">/{{ r.prefix }}</div>
            <div class="open-bar">
              <UptimeBar :history="r.history" :height="14" />
            </div>
            <div class="open-cta">{{ t('home.visit') }} →</div>
          </a>
        </div>
        <div v-else class="gh-dim" style="text-align:center;padding:30px">{{ t('home.noOpenServices') }}</div>
      </el-card>

      <div class="gh-grid gh-grid-4" style="margin-top:16px">
        <div class="gh-panel gh-stat">
          <div class="gh-stat-icon">📦</div>
          <div class="gh-num">{{ total }}</div>
          <div class="gh-stat-label">{{ t('home.totalServices') }}</div>
        </div>
        <div class="gh-panel gh-stat">
          <div class="gh-stat-icon">🟢</div>
          <div class="gh-num" style="color:var(--gh-green)">{{ healthy }}</div>
          <div class="gh-stat-label">{{ t('home.healthy') }}</div>
        </div>
        <div class="gh-panel gh-stat">
          <div class="gh-stat-icon">🔴</div>
          <div class="gh-num" style="color:var(--gh-red)">{{ down }}</div>
          <div class="gh-stat-label">{{ t('home.unhealthy') }}</div>
        </div>
        <div class="gh-panel gh-stat">
          <div class="gh-stat-icon">⚪</div>
          <div class="gh-num" style="color:var(--gh-gray)">{{ unknown }}</div>
          <div class="gh-stat-label">{{ t('home.unknown') }}</div>
        </div>
      </div>

      <el-card class="gh-panel" shadow="never" style="margin-top:16px">
        <template #header>
          <span class="gh-title">{{ t('home.serviceList') }}</span>
        </template>
        <el-table :data="routes" v-loading="loading">
          <el-table-column :label="t('home.serviceName')" min-width="150">
            <template #default="{ row }">{{ row.name }}</template>
          </el-table-column>
          <el-table-column :label="t('home.forwardName')" min-width="140">
            <template #default="{ row }">
              <a v-if="row.status === 'active'" class="route-link" :href="'/' + row.prefix + '/'" target="_blank" rel="noopener">/{{ row.prefix }}</a>
              <span v-else class="gh-tag">/{{ row.prefix }}</span>
            </template>
          </el-table-column>
          <el-table-column prop="target" :label="t('home.backend')" min-width="150" />
          <el-table-column :label="t('common.status')" width="100" align="center">
            <template #default="{ row }">
              <span :class="'gh-badge ' + (row.status === 'active' ? 'green' : 'gray')">{{ row.status === 'active' ? t('home.serving') : t('home.stopped') }}</span>
            </template>
          </el-table-column>
          <el-table-column :label="t('common.health')" min-width="240">
            <template #default="{ row }">
              <div class="health-cell">
                <UptimeBar :history="row.history" :height="16" />
                <span :class="'gh-badge ' + healthColor(row.health)">{{ healthText(row.health) }}</span>
              </div>
            </template>
          </el-table-column>
        </el-table>
        <div class="gh-tip info" style="margin-top:14px">💡 {{ t('home.accessFormat') }}</div>
      </el-card>
    </main>

    <LoginDialog v-model="showLogin" />
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { Refresh } from '@element-plus/icons-vue'
import api from '../api'
import { authState } from '../store'
import { SUPPORTED_LOCALES, setLocale } from '../i18n'
import { theme, toggleTheme } from '../theme'
import LoginDialog from '../components/LoginDialog.vue'
import UptimeBar from '../components/UptimeBar.vue'

const { t, locale } = useI18n()
const routes = ref([])
const loading = ref(false)
const showLogin = ref(false)
const siteName = ref('GatewayHub')
let timer = null

const locales = SUPPORTED_LOCALES
const currentLocaleLabel = computed(() => {
  const l = locales.find((x) => x.value === locale.value)
  return l ? l.label : locale.value
})

function onLocale(v) {
  setLocale(v)
}

async function load() {
  loading.value = true
  try {
    const res = await api.listRoutes()
    routes.value = res.data || []
  } catch (e) {
    /* ignore */
  } finally {
    loading.value = false
  }
}

async function loadSite() {
  try {
    const res = await api.getSettings()
    if (res.code === 0 && res.data.site_name) siteName.value = res.data.site_name
  } catch (e) {}
}

onMounted(() => {
  loadSite()
  load()
  timer = setInterval(load, 10000)
})
onUnmounted(() => clearInterval(timer))

const total = computed(() => routes.value.length)
const healthy = computed(() => routes.value.filter((r) => r.health === 'healthy').length)
const down = computed(() => routes.value.filter((r) => r.health === 'down' || r.health === 'unhealthy').length)
const unknown = computed(() => routes.value.filter((r) => r.health !== 'healthy' && r.health !== 'down' && r.health !== 'unhealthy').length)
const openRoutes = computed(() => routes.value.filter((r) => r.status === 'active'))

function healthColor(h) {
  if (h === 'healthy') return 'green'
  if (h === 'down' || h === 'unhealthy') return 'red'
  if (h === 'warning') return 'orange'
  return 'gray'
}
function healthText(h) {
  if (h === 'healthy') return t('home.healthy')
  if (h === 'down' || h === 'unhealthy') return t('home.unhealthy')
  if (h === 'warning') return t('health.warning')
  return t('home.unknown')
}
</script>

<style scoped>
.main {
  max-width: 1120px;
  margin: 0 auto;
  padding: 24px 20px 40px;
}
.head-actions {
  display: flex;
  align-items: center;
  gap: 16px;
}
.panel-head {
  display: flex;
  justify-content: space-between;
  align-items: center;
}
.route-link {
  font-family: 'JetBrains Mono', monospace;
  color: var(--gh-primary);
  text-decoration: none;
  border-bottom: 1px dashed var(--gh-border-strong);
}
.route-link:hover { color: var(--gh-primary-soft); }
.health-cell {
  display: flex;
  align-items: center;
  gap: 8px;
}
.health-cell .uptime-bar { flex: 1; }

/* 已开放服务卡片 */
.open-grid {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 14px;
}
@media (max-width: 860px) { .open-grid { grid-template-columns: 1fr 1fr; } }
@media (max-width: 560px) { .open-grid { grid-template-columns: 1fr; } }
.open-card {
  display: block;
  padding: 16px;
  border: 1px solid var(--gh-border);
  border-radius: 12px;
  background: var(--gh-panel-solid);
  text-decoration: none;
  color: var(--gh-text);
  transition: all 0.2s;
}
.open-card:hover {
  border-color: var(--gh-border-strong);
  box-shadow: var(--gh-glow-strong);
  transform: translateY(-2px);
}
.open-top {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 8px;
  margin-bottom: 6px;
}
.open-name {
  font-size: 16px;
  font-weight: 700;
  color: var(--gh-text);
}
.open-prefix {
  font-family: 'JetBrains Mono', monospace;
  font-size: 13px;
  color: var(--gh-primary);
  margin-bottom: 10px;
}
.open-bar { margin-bottom: 10px; }
.open-cta {
  font-size: 13px;
  font-weight: 600;
  color: var(--gh-primary);
}
</style>
