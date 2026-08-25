<template>
  <div class="home-page">
    <header class="gh-header">
      <div class="gh-brand" @click="goHome">
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
          <el-button type="primary" size="small" round @click="$router.push('/dashboard')">{{ t('home.enterConsole') }}</el-button>
        </template>
        <template v-else>
          <el-button type="primary" size="small" round @click="showLogin = true">{{ t('common.login') }}</el-button>
        </template>
      </div>
    </header>

    <!-- ============ Hero 高级感横幅 ============ -->
    <section class="hero">
      <div class="hero-bg" aria-hidden="true">
        <i class="orb orb-1"></i>
        <i class="orb orb-2"></i>
        <i class="orb orb-3"></i>
        <i class="grid-lines"></i>
      </div>
      <div class="hero-inner">
        <div class="hero-eyebrow">{{ t('app.version') }} · {{ t('app.subtitle') }}</div>
        <h1 class="hero-title">{{ siteName }}</h1>
        <p v-if="intro" class="hero-intro">{{ intro }}</p>
        <p v-else class="hero-intro gh-dim">{{ t('home.heroDefault') }}</p>
        <div class="hero-stats">
          <div class="hero-stat">
            <span class="hs-num">{{ total }}</span>
            <span class="hs-label">{{ t('home.totalServices') }}</span>
          </div>
          <div class="hero-stat">
            <span class="hs-num" style="color:var(--gh-green)">{{ healthy }}</span>
            <span class="hs-label">{{ t('home.healthy') }}</span>
          </div>
          <div class="hero-stat">
            <span class="hs-num" style="color:var(--gh-red)">{{ down }}</span>
            <span class="hs-label">{{ t('home.unhealthy') }}</span>
          </div>
          <div class="hero-stat">
            <span class="hs-num" style="color:var(--gh-gray)">{{ unknown }}</span>
            <span class="hs-label">{{ t('home.unknown') }}</span>
          </div>
        </div>
        <div class="hero-cta">
          <template v-if="authState.token">
            <el-button type="primary" size="large" round @click="$router.push('/dashboard')">{{ t('home.enterConsole') }} →</el-button>
          </template>
          <template v-else>
            <el-button type="primary" size="large" round @click="showLogin = true">{{ t('common.login') }}</el-button>
          </template>
          <a v-if="openRoutes.length" class="hero-scroll" href="#services">{{ t('home.viewServices') }} ↓</a>
        </div>
      </div>
    </section>

    <main class="main">
      <!-- 已开放服务（每项竖排一行，含条条，hover 浮动） -->
      <el-card id="services" class="gh-panel" shadow="never">
        <template #header>
          <div class="panel-head">
            <span class="gh-title">🌐 {{ t('home.openServices') }} <span class="gh-dim count-chip">{{ openRoutes.length }}</span></span>
            <div style="display:flex;gap:10px;align-items:center">
              <span class="gh-dim">{{ t('home.autoRefresh') }}</span>
              <el-button size="small" :icon="Refresh" :loading="loading" @click="load">{{ t('common.refresh') }}</el-button>
            </div>
          </div>
        </template>

        <div v-if="openRoutes.length" class="open-list">
          <a
            v-for="r in openRoutes"
            :key="r.prefix"
            class="open-row"
            :href="joinBase(r.prefix) + '/'"
            target="_blank"
            rel="noopener"
          >
            <span class="row-status" :class="healthColor(r.health)" :title="healthText(r.health)"></span>
            <div class="row-main">
              <div class="row-top">
                <span class="row-name">{{ r.name }}</span>
                <span class="gh-tag row-prefix">/{{ r.prefix }}</span>
              </div>
              <div v-if="r.description" class="row-desc" :title="r.description">{{ r.description }}</div>
              <div class="row-bar">
                <UptimeBar :history="r.history" :height="14" />
              </div>
            </div>
            <div class="row-side">
              <span v-if="r.latency_ms != null" class="row-latency">{{ r.latency_ms }}ms</span>
              <span :class="'gh-badge ' + healthColor(r.health)">{{ healthText(r.health) }}</span>
              <span class="row-cta">{{ t('home.visit') }} →</span>
            </div>
          </a>
        </div>
        <div v-else class="gh-dim" style="text-align:center;padding:30px">{{ t('home.noOpenServices') }}</div>
      </el-card>

      <!-- 服务列表 -->
      <el-card class="gh-panel" shadow="never" style="margin-top:16px">
        <template #header>
          <span class="gh-title">📋 {{ t('home.serviceList') }}</span>
        </template>
        <el-table :data="routes" v-loading="loading">
          <el-table-column :label="t('home.serviceName')" min-width="150">
            <template #default="{ row }">{{ row.name }}</template>
          </el-table-column>
          <el-table-column :label="t('home.forwardName')" min-width="140">
            <template #default="{ row }">
              <a v-if="row.status === 'active'" class="route-link" :href="joinBase(row.prefix) + '/'" target="_blank" rel="noopener">/{{ row.prefix }}</a>
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

      <footer class="page-foot">
        <span>© {{ year }} {{ siteName }}</span>
        <span>·</span>
        <span>{{ t('app.version') }}</span>
      </footer>
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
import { basePath, joinBase } from '../base'
import LoginDialog from '../components/LoginDialog.vue'
import UptimeBar from '../components/UptimeBar.vue'

const { t, locale } = useI18n()
const routes = ref([])
const loading = ref(false)
const showLogin = ref(false)
const siteName = ref('GatewayHub')
const intro = ref('')
let timer = null

const locales = SUPPORTED_LOCALES
const currentLocaleLabel = computed(() => {
  const l = locales.find((x) => x.value === locale.value)
  return l ? l.label : locale.value
})

function onLocale(v) {
  setLocale(v)
}

function goHome() {
  window.location.assign(basePath)
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
    if (res.code === 0) {
      if (res.data.site_name) siteName.value = res.data.site_name
      intro.value = res.data.site_intro || ''
    }
  } catch (e) {}
}

onMounted(() => {
  loadSite()
  load()
  timer = setInterval(load, 10000)
})
onUnmounted(() => clearInterval(timer))

const year = new Date().getFullYear()
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
.home-page { min-height: 100vh; }
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
.count-chip {
  display: inline-block;
  min-width: 20px;
  text-align: center;
  padding: 1px 7px;
  border-radius: 20px;
  background: rgba(var(--gh-primary-rgb), 0.12);
  border: 1px solid var(--gh-border-strong);
  font-size: 11px;
  vertical-align: 2px;
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

/* ============ Hero 高级感 ============ */
.hero {
  position: relative;
  overflow: hidden;
  padding: 64px 20px 56px;
  text-align: center;
}
.hero-bg { position: absolute; inset: 0; pointer-events: none; }
.hero-bg .orb {
  position: absolute;
  border-radius: 50%;
  filter: blur(60px);
  opacity: 0.55;
  animation: float 9s ease-in-out infinite;
}
.hero-bg .orb-1 {
  width: 420px; height: 420px;
  left: -80px; top: -140px;
  background: radial-gradient(circle, rgba(var(--gh-primary-rgb), 0.55), transparent 70%);
}
.hero-bg .orb-2 {
  width: 380px; height: 380px;
  right: -60px; top: -100px;
  background: radial-gradient(circle, rgba(59, 130, 246, 0.45), transparent 70%);
  animation-delay: -3s;
}
.hero-bg .orb-3 {
  width: 300px; height: 300px;
  left: 50%; bottom: -160px;
  transform: translateX(-50%);
  background: radial-gradient(circle, rgba(var(--gh-primary-rgb), 0.35), transparent 70%);
  animation-delay: -6s;
}
.hero-bg .grid-lines {
  position: absolute; inset: 0;
  background-image:
    linear-gradient(rgba(var(--gh-primary-rgb), 0.05) 1px, transparent 1px),
    linear-gradient(90deg, rgba(var(--gh-primary-rgb), 0.05) 1px, transparent 1px);
  background-size: 44px 44px;
  mask-image: radial-gradient(ellipse 80% 70% at 50% 40%, #000 30%, transparent 75%);
  -webkit-mask-image: radial-gradient(ellipse 80% 70% at 50% 40%, #000 30%, transparent 75%);
}
@keyframes float {
  0%, 100% { transform: translateY(0) scale(1); }
  50% { transform: translateY(-18px) scale(1.05); }
}
.hero-inner { position: relative; z-index: 1; }
.hero-eyebrow {
  display: inline-block;
  font-size: 12px;
  letter-spacing: 2px;
  color: var(--gh-primary);
  background: rgba(var(--gh-primary-rgb), 0.1);
  border: 1px solid var(--gh-border-strong);
  border-radius: 20px;
  padding: 5px 14px;
  margin-bottom: 18px;
}
.hero-title {
  margin: 0 0 14px;
  font-size: 46px;
  font-weight: 800;
  letter-spacing: 1px;
  background: linear-gradient(120deg, var(--gh-primary-soft), var(--gh-primary2) 55%, var(--gh-primary));
  -webkit-background-clip: text;
  background-clip: text;
  -webkit-text-fill-color: transparent;
  text-shadow: 0 0 40px rgba(var(--gh-primary-rgb), 0.25);
}
.hero-intro {
  max-width: 640px;
  margin: 0 auto;
  font-size: 15px;
  line-height: 1.9;
  color: var(--gh-text-dim);
  white-space: pre-wrap;
}
.hero-stats {
  display: flex;
  justify-content: center;
  gap: 40px;
  margin-top: 34px;
  flex-wrap: wrap;
}
.hero-stat {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 76px;
}
.hs-num {
  font-family: 'JetBrains Mono', 'Consolas', monospace;
  font-size: 30px;
  font-weight: 700;
  background: linear-gradient(135deg, var(--gh-primary-soft), var(--gh-primary2));
  -webkit-background-clip: text;
  background-clip: text;
  -webkit-text-fill-color: transparent;
}
.hs-label { font-size: 12px; color: var(--gh-text-dim); }
.hero-cta {
  display: flex;
  justify-content: center;
  align-items: center;
  gap: 18px;
  margin-top: 30px;
  flex-wrap: wrap;
}
.hero-scroll {
  color: var(--gh-primary);
  text-decoration: none;
  font-size: 14px;
  font-weight: 600;
}
.hero-scroll:hover { text-shadow: 0 0 14px rgba(var(--gh-primary-rgb), 0.6); }

/* ============ 开放服务竖排列行 ============ */
.open-list {
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.open-row {
  display: flex;
  align-items: center;
  gap: 16px;
  padding: 14px 18px;
  border: 1px solid var(--gh-border);
  border-radius: 12px;
  background: var(--gh-panel-solid);
  text-decoration: none;
  color: var(--gh-text);
  transition: all 0.25s ease;
}
.open-row:hover {
  border-color: var(--gh-border-strong);
  box-shadow: var(--gh-glow-strong);
  transform: translateY(-3px);
}
.row-status {
  flex: none;
  width: 10px;
  height: 10px;
  border-radius: 50%;
}
.row-status.green { background: var(--gh-green); box-shadow: 0 0 10px rgba(var(--gh-green-rgb), 0.9); }
.row-status.orange { background: var(--gh-orange); box-shadow: 0 0 10px rgba(var(--gh-orange-rgb), 0.9); }
.row-status.red { background: var(--gh-red); box-shadow: 0 0 10px rgba(var(--gh-red-rgb), 0.9); }
.row-status.gray { background: var(--gh-gray); }
.row-main { flex: 1; min-width: 0; }
.row-top {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 6px;
}
.row-name {
  font-size: 15px;
  font-weight: 700;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.row-prefix { font-size: 11px; }
.row-desc {
  font-size: 12.5px;
  color: var(--gh-text-dim);
  line-height: 1.55;
  margin-bottom: 8px;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}
.row-bar { display: flex; }
.row-bar .uptime-bar { flex: 1; }
.row-side {
  flex: none;
  display: flex;
  align-items: center;
  gap: 12px;
}
.row-latency {
  font-family: 'JetBrains Mono', monospace;
  font-size: 12px;
  color: var(--gh-text-dim);
}
.row-cta {
  font-size: 13px;
  font-weight: 700;
  color: var(--gh-primary);
  white-space: nowrap;
}
@media (max-width: 640px) {
  .row-side { display: none; }
  .hero-title { font-size: 32px; }
  .hero-stats { gap: 22px; }
}

.page-foot {
  margin-top: 26px;
  text-align: center;
  font-size: 12px;
  color: var(--gh-text-dim);
  display: flex;
  justify-content: center;
  gap: 8px;
}
</style>
