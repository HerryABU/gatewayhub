<template>
  <div class="home-page">
    <!-- ============ 顶栏：访客态不出现「后台入口」文字按钮 ============ -->
    <header class="gh-header">
      <div class="gh-brand" @click="goHome">
        <div class="gh-logo">🔗</div>
        <div class="brand-copy">
          <div class="gh-brand-name">{{ siteName }}</div>
          <div class="gh-brand-sub">{{ t('app.subtitle') }}</div>
        </div>
      </div>
      <nav class="gh-nav" aria-label="site nav">
        <a href="#services">{{ t('home.openServices') }}</a>
        <a href="#links">{{ t('home.friendLinks') }}</a>
        <a v-if="authed" href="#all-services">{{ t('home.allServices') }}</a>
      </nav>
      <div class="head-actions">
        <button class="gh-icon-btn" :title="t('common.theme')" aria-label="theme" @click="toggleTheme">
          {{ theme === 'dark' ? '☀️' : '🌙' }}
        </button>
        <el-dropdown @command="onLocale">
          <span class="gh-lang">🌐 {{ currentLocaleLabel }} <el-icon><ArrowDown /></el-icon></span>
          <template #dropdown>
            <el-dropdown-menu>
              <el-dropdown-item v-for="l in locales" :key="l.value" :command="l.value">{{ l.label }}</el-dropdown-item>
            </el-dropdown-menu>
          </template>
        </el-dropdown>

        <!-- 访客态不显示任何后台入口：管理入口走 /login 直达（见下方说明），
             访客页保持纯展示，避免暴露管理面。 -->

        <!-- 已登录：右上角用户菜单 -->
        <el-dropdown v-if="authed" @command="onCommand">
          <span class="user-chip">
            <span class="user-avatar">{{ (authState.username || 'A').slice(0, 1).toUpperCase() }}</span>
            <span class="user-name">{{ authState.username }}</span>
            <el-icon><ArrowDown /></el-icon>
          </span>
          <template #dropdown>
            <el-dropdown-menu>
              <el-dropdown-item command="console">{{ t('home.enterConsole') }}</el-dropdown-item>
              <el-dropdown-item command="logout" divided>{{ t('common.logout') }}</el-dropdown-item>
            </el-dropdown-menu>
          </template>
        </el-dropdown>
      </div>
    </header>

    <!-- ============ Hero ============ -->
    <section class="hero">
      <div class="hero-bg" aria-hidden="true">
        <i class="orb orb-1"></i>
        <i class="orb orb-2"></i>
        <i class="orb orb-3"></i>
        <i class="grid-lines"></i>
      </div>
      <div class="hero-inner">
        <div class="hero-eyebrow"><span class="pulse"></span>{{ appVersion }} · {{ t('app.subtitle') }}</div>
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
          <el-button v-if="authed" type="primary" size="large" round @click="$router.push('/dashboard')">
            {{ t('home.enterConsole') }} →
          </el-button>
          <a class="cta-primary" href="#services">{{ t('home.viewServices') }} ↓</a>
          <a class="cta-secondary" href="#links">{{ t('home.viewLinks') }} ↗</a>
        </div>
      </div>
    </section>

    <main class="main">
      <!-- ============ 01 已开放服务 ============ -->
      <section id="services" class="section">
        <div class="section-head">
          <div>
            <div class="section-index">01 / SERVICES</div>
            <h2 class="section-title">{{ t('home.openServices') }} <span class="count-chip">{{ openRoutes.length }}</span></h2>
          </div>
          <div v-if="authed" class="section-tools">
            <span class="gh-dim refresh-hint">{{ t('home.autoRefresh') }}</span>
            <button class="ghost-btn" :disabled="loading" @click="load">↻ {{ t('common.refresh') }}</button>
          </div>
        </div>

        <!-- 状态汇总：参考 uptime-kuma 的「所有服务运行正常」一行 -->
        <div v-if="openRoutes.length" class="overall-status" :class="overallState">
          <span class="overall-icon">{{ overallState === 'ok' ? '✓' : '!' }}</span>
          <span class="overall-text">{{ overallText }}</span>
          <span class="overall-count">{{ healthy }} / {{ openRoutes.length }}</span>
        </div>

        <div v-if="openRoutes.length" class="monitor-list">
          <a
            v-for="r in openRoutes"
            :key="r.prefix"
            class="monitor-item"
            :href="joinBase(r.prefix) + '/'"
            target="_blank"
            rel="noopener"
          >
            <div class="mi-info">
              <span class="mi-uptime-pct" :class="uptimeTone(r)">{{ uptimePct(r) }}</span>
              <span class="mi-name">{{ r.name }}</span>
              <span v-if="r.cert_days > 0" class="mi-cert" :class="certTone(r.cert_days)">
                {{ t('home.certDays', { n: r.cert_days }) }}
              </span>
            </div>
            <div class="mi-chart">
              <UptimeBar :history="r.history" :height="20" />
              <div class="mi-chart-foot">
                <span>{{ lastCheckAgo(r) }}</span>
                <span>{{ r.latency_ms != null ? r.latency_ms + 'ms' : healthText(r.health) }}</span>
              </div>
            </div>
          </a>
        </div>
        <div v-else class="empty-note">{{ t('home.noOpenServices') }}</div>
      </section>

      <!-- ============ 02 友情链接（纯跳转，不经网关转发） ============ -->
      <section id="links" class="section">
        <div class="section-head">
          <div>
            <div class="section-index">02 / FRIEND LINKS</div>
            <h2 class="section-title">{{ t('home.friendLinks') }}</h2>
          </div>
          <span class="section-hint">{{ t('home.friendLinksHint') }}</span>
        </div>

        <div v-if="links.length" class="link-grid">
          <a
            v-for="l in links"
            :key="l.id"
            class="link-card"
            data-testid="friend-link"
            :href="l.url"
            target="_blank"
            rel="noopener noreferrer"
          >
            <span class="link-avatar">{{ l.name.slice(0, 1).toUpperCase() }}</span>
            <span class="link-body">
              <span class="link-name">{{ l.name }}</span>
              <span class="link-desc">{{ l.description || l.url }}</span>
            </span>
            <span class="link-arrow" :title="t('home.externalOpen')">↗</span>
          </a>
        </div>
        <div v-else class="empty-note">{{ t('home.noFriendLinks') }}</div>
      </section>

      <!-- ============ 03 全部服务（详情页） ============ -->
      <section id="all-services" class="section panel-section">
        <div class="section-head">
          <div>
            <div class="section-index">03 / OVERVIEW</div>
            <h2 class="section-title">{{ t('home.serviceList') }}</h2>
          </div>
        </div>
        <el-table :data="routes" v-loading="loading" class="svc-table">
          <el-table-column :label="t('home.serviceName')" min-width="150">
            <template #default="{ row }">{{ row.name }}</template>
          </el-table-column>
          <el-table-column :label="t('home.forwardName')" min-width="140">
            <template #default="{ row }">
              <a v-if="row.status === 'active'" class="route-link" :href="joinBase(row.prefix) + '/'" target="_blank" rel="noopener">/{{ row.prefix }}</a>
              <span v-else class="gh-tag">/{{ row.prefix }}</span>
            </template>
          </el-table-column>
          <el-table-column v-if="authed" prop="target" :label="t('home.backend')" min-width="150" />
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
        <div class="gh-tip info table-tip">💡 {{ t('home.accessFormat') }}</div>
      </section>

      <footer class="page-foot">
        <span>© {{ year }} {{ siteName }}</span>
        <span>·</span>
        <span>{{ appVersion }}</span>
      </footer>
    </main>

    <LoginDialog v-model="showLogin" />
  </div>
</template>

<script setup>
import { ref, computed, watch, onMounted, onUnmounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { ArrowDown } from '@element-plus/icons-vue'
import api from '../api'
import { authState, clearAuth } from '../store'
import { SUPPORTED_LOCALES, setLocale } from '../i18n'
import { theme, toggleTheme } from '../theme'
import { basePath, joinBase } from '../base'
import LoginDialog from '../components/LoginDialog.vue'
import UptimeBar from '../components/UptimeBar.vue'

const { t, locale } = useI18n()
const route = useRoute()
const router = useRouter()
const routes = ref([])
const links = ref([])
const loading = ref(false)
const showLogin = ref(false)
const siteName = ref('GatewayHub')
const intro = ref('')
// 版本号以服务端 /api/settings 为准，i18n 里的值只作首帧兜底：
// 前端产物是打进 exe 的，写死版本必然与后端脱节（曾出现页面 v1.5.0、接口 1.6.x）。
const appVersion = ref(t('app.version'))
let timer = null

const authed = computed(() => !!authState.token)
const locales = SUPPORTED_LOCALES
const currentLocaleLabel = computed(() => {
  const l = locales.find((x) => x.value === locale.value)
  return l ? l.label : locale.value
})

function onLocale(v) {
  setLocale(v)
}
function onCommand(cmd) {
  if (cmd === 'console') {
    window.location.assign(joinBase('dashboard'))
  } else if (cmd === 'logout') {
    clearAuth()
    window.location.assign(basePath)
  }
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
      if (res.data.version) appVersion.value = 'v' + res.data.version
    }
  } catch (e) {}
}

async function loadLinks() {
  try {
    // 友链是纯跳转链接，公开可读；不经过任何反向代理路由
    const res = await api.listFriendLinks()
    if (res.code === 0) links.value = res.data || []
  } catch (e) {}
}

onMounted(() => {
  loadSite()
  loadLinks()
  load()
  timer = setInterval(load, 10000)
  // 管理员直达 /dashboard 未登录时，路由守卫带 ?login=1 回到这里弹登录框。
  if (route.query.login === '1') showLogin.value = true
})
onUnmounted(() => clearInterval(timer))

// 登录框关闭后清掉 ?login=1，避免刷新页面又弹出登录框。
watch(showLogin, (v) => {
  if (!v && route.query.login === '1') router.replace({ name: 'home' })
})

const year = new Date().getFullYear()
const total = computed(() => routes.value.length)
const healthy = computed(() => routes.value.filter((r) => r.health === 'healthy').length)
const down = computed(() => routes.value.filter((r) => r.health === 'down' || r.health === 'unhealthy').length)
const unknown = computed(() => routes.value.filter((r) => r.health !== 'healthy' && r.health !== 'down' && r.health !== 'unhealthy').length)
const openRoutes = computed(() => routes.value.filter((r) => r.status === 'active'))

// ===== 访客页监控面板风格元素（参考 uptime-kuma 展示） =====
const overallState = computed(() => {
  if (!openRoutes.value.length) return 'ok'
  return down.value > 0 ? 'bad' : 'ok'
})
const overallText = computed(() =>
  overallState.value === 'ok' ? t('home.allHealthy') : t('home.someDown', { n: down.value })
)

// 可用性百分比：按历史点里健康占比；无历史时按当前状态给 100% / 0%。
function uptimePct(r) {
  const h = r.history || []
  if (!h.length) return r.health === 'healthy' ? '100%' : '—'
  const ok = h.filter((p) => p.status === 'healthy' || p.status === 'warning').length
  return Math.round((ok / h.length) * 100) + '%'
}
function uptimeTone(r) {
  const p = parseInt(uptimePct(r), 10)
  if (isNaN(p)) return 'gray'
  if (p >= 99) return 'ok'
  if (p >= 90) return 'warn'
  return 'bad'
}
// 证书有效期配色：≥30 天绿，<30 天橙，0（不适用）不显示。
function certTone(days) {
  return days >= 30 ? 'ok' : 'warn'
}
// 最近一次探测距今（参考页右端的 3h ago / 17m ago）。
function lastCheckAgo(r) {
  const h = r.history || []
  if (!h.length) return t('home.unknown')
  const last = h[h.length - 1].time
  if (!last) return t('home.unknown')
  const mins = Math.max(0, Math.floor((Date.now() - last) / 60000))
  if (mins < 1) return t('home.justNow')
  if (mins < 60) return t('home.minsAgo', { n: mins })
  const hrs = Math.floor(mins / 60)
  if (hrs < 24) return t('home.hoursAgo', { n: hrs })
  return t('home.daysAgo', { n: Math.floor(hrs / 24) })
}

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

/* ============ 顶栏 ============ */
.gh-header {
  position: sticky;
  top: 0;
  z-index: 20;
  display: flex;
  align-items: center;
  gap: 24px;
  padding: 12px max(24px, calc((100vw - 1180px) / 2));
  border-bottom: 1px solid var(--gh-border);
  background: color-mix(in srgb, var(--gh-bg) 82%, transparent);
  backdrop-filter: blur(14px);
}
.gh-brand { display: flex; align-items: center; gap: 10px; cursor: pointer; flex: none; }
.gh-logo {
  width: 38px; height: 38px;
  display: grid; place-items: center;
  border-radius: 11px;
  background: linear-gradient(140deg, rgba(var(--gh-primary-rgb), 0.22), rgba(37, 99, 235, 0.16));
  border: 1px solid var(--gh-border-strong);
  font-size: 19px;
}
.gh-brand-name { font-size: 15px; font-weight: 800; letter-spacing: 0.3px; }
.gh-brand-sub { font-size: 10px; color: var(--gh-text-dim); letter-spacing: 1px; text-transform: uppercase; }
.gh-nav { display: flex; gap: 22px; margin-left: auto; }
.gh-nav a {
  font-size: 13px;
  font-weight: 600;
  color: var(--gh-text-dim);
  text-decoration: none;
  padding: 6px 0;
  border-bottom: 2px solid transparent;
  transition: 0.2s;
}
.gh-nav a:hover { color: var(--gh-primary); border-bottom-color: var(--gh-primary); }
.head-actions { display: flex; align-items: center; gap: 12px; }
.gh-icon-btn {
  width: 34px; height: 34px;
  display: inline-grid; place-items: center;
  border-radius: 10px;
  border: 1px solid var(--gh-border);
  background: var(--gh-panel);
  color: var(--gh-text);
  cursor: pointer;
  font-size: 15px;
  transition: 0.2s;
}
.gh-icon-btn:hover { border-color: var(--gh-border-strong); transform: translateY(-1px); box-shadow: var(--gh-glow); }
.admin-entry { color: var(--gh-text-dim); }
.admin-entry:hover { color: var(--gh-primary); }
.gh-lang { display: inline-flex; align-items: center; gap: 4px; font-size: 12px; cursor: pointer; color: var(--gh-text-dim); }
.user-chip { display: inline-flex; align-items: center; gap: 8px; cursor: pointer; font-size: 12px; }
.user-avatar {
  width: 28px; height: 28px; border-radius: 50%;
  display: grid; place-items: center;
  background: linear-gradient(135deg, var(--gh-primary), var(--gh-primary2));
  color: #fff; font-weight: 700; font-size: 12px;
}
.user-name { max-width: 100px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }

/* ============ Hero ============ */
.hero { position: relative; overflow: hidden; padding: 66px 20px 58px; text-align: center; }
.hero-bg { position: absolute; inset: 0; pointer-events: none; }
.hero-bg .orb { position: absolute; border-radius: 50%; filter: blur(60px); opacity: 0.5; animation: float 9s ease-in-out infinite; }
.orb-1 { width: 420px; height: 420px; left: -80px; top: -140px; background: radial-gradient(circle, rgba(var(--gh-primary-rgb), 0.55), transparent 70%); }
.orb-2 { width: 380px; height: 380px; right: -60px; top: -100px; background: radial-gradient(circle, rgba(59, 130, 246, 0.45), transparent 70%); animation-delay: -3s; }
.orb-3 { width: 300px; height: 300px; left: 50%; bottom: -160px; transform: translateX(-50%); background: radial-gradient(circle, rgba(var(--gh-primary-rgb), 0.35), transparent 70%); animation-delay: -6s; }
.grid-lines {
  position: absolute; inset: 0;
  background-image:
    linear-gradient(rgba(var(--gh-primary-rgb), 0.05) 1px, transparent 1px),
    linear-gradient(90deg, rgba(var(--gh-primary-rgb), 0.05) 1px, transparent 1px);
  background-size: 44px 44px;
  mask-image: radial-gradient(ellipse 80% 70% at 50% 40%, #000 30%, transparent 75%);
  -webkit-mask-image: radial-gradient(ellipse 80% 70% at 50% 40%, #000 30%, transparent 75%);
}
@keyframes float { 0%, 100% { transform: translateY(0) scale(1); } 50% { transform: translateY(-18px) scale(1.05); } }
.hero-inner { position: relative; z-index: 1; }
.hero-eyebrow {
  display: inline-flex; align-items: center; gap: 8px;
  font-size: 11px; letter-spacing: 2px; text-transform: uppercase;
  color: var(--gh-primary);
  background: rgba(var(--gh-primary-rgb), 0.1);
  border: 1px solid var(--gh-border-strong);
  border-radius: 20px; padding: 5px 15px; margin-bottom: 18px;
}
.pulse { width: 6px; height: 6px; border-radius: 50%; background: var(--gh-green); box-shadow: 0 0 8px var(--gh-green); }
.hero-title {
  margin: 0 0 14px; font-size: 52px; font-weight: 800; letter-spacing: 1px;
  background: linear-gradient(120deg, var(--gh-primary-soft), var(--gh-primary2) 55%, var(--gh-primary));
  -webkit-background-clip: text; background-clip: text; -webkit-text-fill-color: transparent;
  text-shadow: 0 0 40px rgba(var(--gh-primary-rgb), 0.25);
}
.hero-intro { max-width: 640px; margin: 0 auto; font-size: 15px; line-height: 1.9; color: var(--gh-text-dim); white-space: pre-wrap; }
.gh-dim { color: var(--gh-text-dim); }
.hero-stats { display: flex; justify-content: center; gap: 40px; margin-top: 34px; flex-wrap: wrap; }
.hero-stat { display: flex; flex-direction: column; gap: 2px; min-width: 76px; }
.hs-num { font-family: 'JetBrains Mono', monospace; font-size: 30px; font-weight: 700; background: linear-gradient(135deg, var(--gh-primary-soft), var(--gh-primary2)); -webkit-background-clip: text; background-clip: text; -webkit-text-fill-color: transparent; }
.hs-label { font-size: 12px; color: var(--gh-text-dim); }
.hero-cta { display: flex; justify-content: center; align-items: center; gap: 14px; margin-top: 30px; flex-wrap: wrap; }
.cta-primary, .cta-secondary {
  display: inline-flex; align-items: center; justify-content: center;
  min-height: 40px; padding: 0 22px; border-radius: 22px;
  font-size: 14px; font-weight: 700; text-decoration: none; transition: 0.2s;
}
.cta-primary { color: #fff; background: linear-gradient(120deg, var(--gh-primary), var(--gh-primary2)); box-shadow: var(--gh-glow-strong); }
.cta-secondary { color: var(--gh-primary); border: 1px solid var(--gh-border-strong); background: var(--gh-panel); }
.cta-primary:hover, .cta-secondary:hover { transform: translateY(-2px); }

/* ============ 主体分区 ============ */
.main { max-width: 1180px; margin: 0 auto; padding: 12px 20px 48px; }
.section { margin-bottom: 40px; scroll-margin-top: 84px; }
.section-head { display: flex; align-items: flex-end; justify-content: space-between; gap: 16px; margin-bottom: 18px; }
.section-index { font-family: 'JetBrains Mono', monospace; font-size: 10px; letter-spacing: 2px; color: var(--gh-primary); opacity: 0.85; margin-bottom: 6px; }
.section-title { margin: 0; font-size: 21px; font-weight: 800; }
.section-hint { font-size: 11px; color: var(--gh-text-dim); max-width: 420px; text-align: right; }
.section-tools { display: flex; align-items: center; gap: 10px; }
.refresh-hint { font-size: 11px; }
.ghost-btn {
  border: 1px solid var(--gh-border); background: var(--gh-panel); color: var(--gh-text-dim);
  border-radius: 9px; padding: 6px 12px; font-size: 12px; cursor: pointer;
}
.ghost-btn:hover { color: var(--gh-primary); border-color: var(--gh-border-strong); }
.count-chip { display: inline-block; min-width: 22px; text-align: center; padding: 2px 8px; border-radius: 20px; background: rgba(var(--gh-primary-rgb), 0.12); border: 1px solid var(--gh-border-strong); font-size: 11px; vertical-align: 3px; }

/* ===== 监控面板风格：汇总条 + 列表行（参考 uptime-kuma，配色沿用科技风变量） ===== */
.overall-status {
  display: flex; align-items: center; gap: 10px;
  padding: 12px 18px; margin-bottom: 16px;
  border: 1px solid var(--gh-border); border-radius: 12px;
  background: var(--gh-panel);
}
.overall-status.ok { border-color: color-mix(in srgb, var(--gh-green) 42%, var(--gh-border)); }
.overall-status.bad { border-color: color-mix(in srgb, var(--gh-red) 55%, var(--gh-border)); }
.overall-icon {
  width: 22px; height: 22px; flex: none; border-radius: 50%;
  display: grid; place-items: center; font-size: 13px; font-weight: 800; color: #fff;
}
.overall-status.ok .overall-icon { background: var(--gh-green); }
.overall-status.bad .overall-icon { background: var(--gh-red); }
.overall-text { font-size: 14px; font-weight: 700; flex: 1; }
.overall-status.ok .overall-text { color: var(--gh-green); }
.overall-status.bad .overall-text { color: var(--gh-red); }
.overall-count { font-family: 'JetBrains Mono', monospace; font-size: 12px; color: var(--gh-text-dim); }

.monitor-list {
  border: 1px solid var(--gh-border); border-radius: 14px;
  background: var(--gh-panel); overflow: hidden;
}
.monitor-item {
  display: flex; align-items: center; gap: 18px;
  padding: 13px 18px; text-decoration: none; color: var(--gh-text);
  border-bottom: 1px solid var(--gh-border);
  transition: background 0.2s;
}
.monitor-item:last-child { border-bottom: none; }
.monitor-item:hover { background: rgba(var(--gh-primary-rgb), 0.06); }
.mi-info { display: flex; align-items: center; gap: 10px; flex: 1; min-width: 0; }
.mi-uptime-pct {
  font-family: 'JetBrains Mono', monospace; font-size: 11px; font-weight: 700;
  padding: 3px 9px; border-radius: 20px; flex: none;
  background: rgba(var(--gh-primary-rgb), 0.12); border: 1px solid var(--gh-border-strong);
  color: var(--gh-text-dim);
}
.mi-uptime-pct.ok { color: var(--gh-green); border-color: color-mix(in srgb, var(--gh-green) 45%, transparent); }
.mi-uptime-pct.warn { color: var(--gh-orange); border-color: color-mix(in srgb, var(--gh-orange) 45%, transparent); }
.mi-uptime-pct.bad { color: var(--gh-red); border-color: color-mix(in srgb, var(--gh-red) 45%, transparent); }
.mi-name {
  font-size: 14px; font-weight: 700; flex: 1;
  overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
}
.monitor-item:hover .mi-name { color: var(--gh-primary); }
.mi-cert {
  font-size: 10px; padding: 3px 9px; border-radius: 6px; flex: none;
  background: color-mix(in srgb, var(--gh-green) 16%, transparent);
  color: var(--gh-green); border: 1px solid color-mix(in srgb, var(--gh-green) 35%, transparent);
  white-space: nowrap;
}
.mi-cert.warn {
  background: color-mix(in srgb, var(--gh-orange) 16%, transparent);
  color: var(--gh-orange); border-color: color-mix(in srgb, var(--gh-orange) 35%, transparent);
}
.mi-chart { width: 34%; min-width: 180px; flex: none; }
.mi-chart :deep(.uptime-bar) { width: 100%; }
.mi-chart-foot {
  display: flex; justify-content: space-between; margin-top: 4px;
  font-family: 'JetBrains Mono', monospace; font-size: 10px; color: var(--gh-text-dim);
}

/* 友链卡片 */
.link-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(280px, 1fr)); gap: 14px; }
.link-card {
  display: flex; align-items: center; gap: 13px;
  padding: 16px 18px; text-decoration: none; color: var(--gh-text);
  border: 1px solid var(--gh-border); border-radius: 14px;
  background: linear-gradient(140deg, var(--gh-panel-solid), var(--gh-panel));
  transition: 0.25s;
}
.link-card:hover { border-color: var(--gh-border-strong); box-shadow: var(--gh-glow-strong); transform: translateY(-3px); }
.link-avatar {
  width: 40px; height: 40px; flex: none; border-radius: 12px;
  display: grid; place-items: center; font-weight: 800; font-size: 16px;
  color: var(--gh-primary);
  background: rgba(var(--gh-primary-rgb), 0.12);
  border: 1px solid var(--gh-border-strong);
}
.link-body { display: flex; flex-direction: column; gap: 4px; min-width: 0; flex: 1; }
.link-name { font-size: 14px; font-weight: 800; }
.link-desc { font-size: 11px; color: var(--gh-text-dim); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.link-arrow { color: var(--gh-primary); font-size: 17px; }
.empty-note { text-align: center; padding: 34px; color: var(--gh-text-dim); font-size: 13px; border: 1px dashed var(--gh-border); border-radius: 14px; }

/* 全部服务 */
.panel-section { border: 1px solid var(--gh-border); border-radius: 16px; padding: 22px; background: var(--gh-panel); }
.health-cell { display: flex; align-items: center; gap: 8px; }
.health-cell :deep(.uptime-bar) { flex: 1; }
.route-link { font-family: 'JetBrains Mono', monospace; color: var(--gh-primary); text-decoration: none; border-bottom: 1px dashed var(--gh-border-strong); }
.route-link:hover { color: var(--gh-primary-soft); }
.table-tip { margin-top: 14px; font-size: 12px; }

.page-foot { margin-top: 26px; text-align: center; font-size: 12px; color: var(--gh-text-dim); display: flex; justify-content: center; gap: 8px; }
@media (max-width: 760px) {
  .gh-nav { display: none; }
  .gh-header { padding: 12px 16px; }
  .hero-title { font-size: 34px; }
  .hero-stats { gap: 22px; }
  .section-hint, .refresh-hint { display: none; }
  .user-name { display: none; }
  .monitor-item { flex-direction: column; align-items: stretch; gap: 10px; }
  .mi-chart { width: 100%; min-width: 0; }
  .mi-cert { margin-left: auto; }
}
</style>
