<template>
  <div class="iso-page">
    <!-- ============ 概览 ============ -->
    <div class="kpi-row">
      <div class="gh-panel kpi">
        <div class="kpi-label">{{ t('isolation.totalServices') }}</div>
        <div class="kpi-value">{{ count }}</div>
        <div class="gh-dim">{{ t('isolation.sourceFile') }}</div>
      </div>
      <div class="gh-panel kpi">
        <div class="kpi-label">{{ t('isolation.totalRequests') }}</div>
        <div class="kpi-value">{{ sum.requests }}</div>
        <div class="gh-dim">2xx {{ sum.status_2xx }} · 5xx {{ sum.status_5xx }}</div>
      </div>
      <div class="gh-panel kpi">
        <div class="kpi-label">{{ t('isolation.rewriteCount') }}</div>
        <div class="kpi-value">{{ sum.location_rewrites + sum.cookie_rewrites + sum.html_hits }}</div>
        <div class="gh-dim">
          Location {{ sum.location_rewrites }} · Cookie {{ sum.cookie_rewrites }} · HTML {{ sum.html_hits }}
        </div>
      </div>
      <div class="gh-panel kpi">
        <div class="kpi-label">{{ t('isolation.breakerOpen') }}</div>
        <div class="kpi-value" :class="{ danger: openBreakers > 0 }">{{ openBreakers }}</div>
        <div class="gh-dim">{{ t('isolation.backendErrors') }} {{ sum.backend_errors }}</div>
      </div>
    </div>

    <!-- 未启用提示 -->
    <el-alert
      v-if="loaded && !enabled"
      type="info"
      :closable="false"
      show-icon
      :title="t('isolation.disabledTitle')"
      :description="t('isolation.disabledDesc')"
      style="margin-bottom: 14px"
    />

    <!-- 配置告警（例如 html_rewrite 在非 strip 模式无效、帽子互为前缀） -->
    <el-alert
      v-if="warnings.length"
      type="warning"
      :closable="false"
      show-icon
      :title="t('isolation.warningsTitle', { n: warnings.length })"
      style="margin-bottom: 14px"
    >
      <ul class="warn-list">
        <li v-for="(w, i) in warnings" :key="i">{{ w }}</li>
      </ul>
    </el-alert>

    <div class="gh-panel">
      <div class="toolbar">
        <div>
          <span class="gh-title">{{ t('isolation.serviceList') }}</span>
          <div class="gh-dim">
            {{ t('isolation.sourceHint') }}
            <span v-if="hash" class="mono">{{ hash.slice(0, 12) }}</span>
            <span v-if="loadedAt"> · {{ loadedAt }}</span>
          </div>
        </div>
        <div class="tools">
          <el-checkbox v-model="autoRefresh" :label="t('isolation.autoRefresh')" />
          <el-button :loading="loading" @click="load">{{ t('common.refresh') }}</el-button>
          <el-button type="primary" :loading="reloading" @click="reloadConfig">
            {{ t('isolation.reloadConfig') }}
          </el-button>
        </div>
      </div>

      <el-table :data="services" v-loading="loading" row-key="name" empty-text="—">
        <el-table-column type="expand">
          <template #default="{ row }">
            <div class="expand">
              <div class="kv-grid">
                <div class="kv"><span class="k">{{ t('isolation.prefix') }}</span><span class="mono">/{{ row.prefix }}/</span></div>
                <div class="kv"><span class="k">{{ t('isolation.hostMode') }}</span><span>{{ row.host_mode }}</span></div>
                <div class="kv"><span class="k">{{ t('route.timeout') }}</span><span>{{ row.timeout }}s</span></div>
                <div class="kv"><span class="k">{{ t('isolation.htmlRewrite') }}</span>
                  <span :class="'gh-badge ' + (row.html_rewrite ? 'green' : 'gray')">
                    {{ row.html_rewrite ? t('common.enabled') : t('common.disabled') }}
                  </span>
                </div>
                <div class="kv"><span class="k">{{ t('isolation.breakerState') }}</span>
                  <span :class="'gh-badge ' + breakerColor(row.breaker.state)">{{ breakerText(row.breaker.state) }}</span>
                  <span class="gh-dim" v-if="row.breaker.trips"> ×{{ row.breaker.trips }}</span>
                </div>
                <div class="kv"><span class="k">{{ t('isolation.lastSeen') }}</span><span>{{ fmtTime(row.metrics.last_seen_unix) }}</span></div>
              </div>

              <div class="metrics-grid">
                <div v-for="m in metricCells(row.metrics)" :key="m.k" class="cell">
                  <div class="cell-v" :class="m.cls">{{ m.v }}</div>
                  <div class="cell-k">{{ m.k }}</div>
                </div>
              </div>

              <div v-if="row.known_abs_prefixes?.length" class="abs">
                <span class="gh-dim">{{ t('isolation.knownAbsPrefixes') }}：</span>
                <span v-for="p in row.known_abs_prefixes" :key="p" class="gh-tag mono">{{ p }}</span>
              </div>
              <div v-if="row.no_full_compat" class="gh-dim compat">⚠️ {{ t('isolation.noFullCompat') }}</div>
            </div>
          </template>
        </el-table-column>

        <el-table-column :label="t('route.name')" min-width="150">
          <template #default="{ row }">
            <div class="name">{{ row.name }}</div>
            <div class="gh-dim mono">/{{ row.prefix }}/</div>
          </template>
        </el-table-column>

        <el-table-column prop="target" :label="t('route.target')" min-width="140" show-overflow-tooltip>
          <template #default="{ row }"><span class="mono">{{ row.target }}</span></template>
        </el-table-column>

        <el-table-column :label="t('isolation.mode')" width="168">
          <template #default="{ row }">
            <el-select
              :model-value="row.mode"
              size="small"
              :disabled="!enabled"
              @change="(v) => changeMode(row, v)"
            >
              <el-option v-for="m in modes" :key="m.value" :value="m.value" :label="m.label" />
            </el-select>
          </template>
        </el-table-column>

        <el-table-column :label="t('isolation.cookieHat')" width="110" align="center">
          <template #default="{ row }">
            <span v-if="row.cookie_hat" class="gh-tag mono">{{ row.cookie_hat }}</span>
            <span v-else class="gh-dim">—</span>
          </template>
        </el-table-column>

        <el-table-column :label="t('isolation.breakerState')" width="106" align="center">
          <template #default="{ row }">
            <span :class="'gh-badge ' + breakerColor(row.breaker.state)">{{ breakerText(row.breaker.state) }}</span>
          </template>
        </el-table-column>

        <el-table-column prop="metrics.requests" :label="t('isolation.requests')" width="92" align="right" />

        <el-table-column :label="t('isolation.rate5xx')" width="92" align="right">
          <template #default="{ row }">
            <span :class="{ danger: row.metrics.rate_5xx >= 0.5 }">{{ pct(row.metrics.rate_5xx) }}</span>
          </template>
        </el-table-column>

        <el-table-column :label="t('isolation.avgLatency')" width="96" align="right">
          <template #default="{ row }">{{ row.metrics.avg_latency_ms ? row.metrics.avg_latency_ms.toFixed(1) : '—' }} ms</template>
        </el-table-column>

        <el-table-column :label="t('common.action')" width="128" align="center">
          <template #default="{ row }">
            <el-button
              size="small"
              :disabled="!enabled || row.mode === 'isolated'"
              @click="downgrade(row)"
            >
              {{ t('isolation.degrade') }}
            </el-button>
          </template>
        </el-table-column>
      </el-table>
    </div>

    <!-- ============ 变更审计 ============ -->
    <div class="gh-panel audit-panel">
      <div class="toolbar">
        <div>
          <span class="gh-title">{{ t('isolation.audit') }}</span>
          <div class="gh-dim">{{ auditFile || t('isolation.auditHint') }}</div>
        </div>
        <el-button size="small" :loading="auditLoading" @click="loadAudit">{{ t('common.refresh') }}</el-button>
      </div>
      <el-table :data="audit" size="small" empty-text="—">
        <el-table-column prop="time" :label="t('isolation.auditTime')" width="180">
          <template #default="{ row }"><span class="mono">{{ row.time }}</span></template>
        </el-table-column>
        <el-table-column prop="actor" :label="t('isolation.auditActor')" width="120" />
        <el-table-column :label="t('isolation.auditAction')" width="140">
          <template #default="{ row }"><span class="gh-tag">{{ actionText(row.action) }}</span></template>
        </el-table-column>
        <el-table-column prop="service" :label="t('isolation.auditService')" width="110">
          <template #default="{ row }">{{ row.service || '—' }}</template>
        </el-table-column>
        <el-table-column :label="t('isolation.auditChange')" min-width="180">
          <template #default="{ row }">
            <span v-if="row.from || row.to">
              <span class="from">{{ row.from || '—' }}</span>
              <span class="arrow"> → </span>
              <span class="to">{{ row.to || '—' }}</span>
            </span>
            <span v-else class="gh-dim">{{ row.detail || '—' }}</span>
          </template>
        </el-table-column>
      </el-table>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onBeforeUnmount, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessage, ElMessageBox } from 'element-plus'
import api from '../api'

const { t } = useI18n()

const services = ref([])
const warnings = ref([])
const hash = ref('')
const loadedAt = ref('')
const auditFile = ref('')
const enabled = ref(false)
const loaded = ref(false)
const loading = ref(false)
const reloading = ref(false)
const auditLoading = ref(false)
const audit = ref([])
const autoRefresh = ref(true)

let timer = null

const count = computed(() => services.value.length)
const openBreakers = computed(() => services.value.filter((s) => s.breaker?.state === 'open').length)

// sum 汇总所有服务指标（概览卡用）。
const sum = computed(() => {
  const acc = { requests: 0, status_2xx: 0, status_5xx: 0, location_rewrites: 0, cookie_rewrites: 0, html_hits: 0, backend_errors: 0 }
  for (const s of services.value) {
    const m = s.metrics || {}
    for (const k of Object.keys(acc)) acc[k] += m[k] || 0
  }
  return acc
})

const modes = computed(() => [
  { value: 'pass-through', label: t('isolation.modePassThrough') },
  { value: 'strip', label: t('isolation.modeStrip') },
  { value: 'isolated', label: t('isolation.modeIsolated') }
])

function pct(v) {
  if (!v) return '0%'
  return (v * 100).toFixed(1) + '%'
}

function fmtTime(unix) {
  if (!unix) return '—'
  const d = new Date(unix * 1000)
  return d.toLocaleString()
}

function breakerColor(s) {
  if (s === 'open') return 'red'
  if (s === 'half-open') return 'orange'
  return 'green'
}
function breakerText(s) {
  if (s === 'open') return t('isolation.breakerOpen')
  if (s === 'half-open') return t('isolation.breakerHalfOpen')
  return t('isolation.breakerClosed')
}

function actionText(a) {
  if (a === 'reload') return t('isolation.actReload')
  if (a === 'set_mode') return t('isolation.actSetMode')
  if (a === 'enable') return t('isolation.actEnable')
  if (a === 'disable') return t('isolation.actDisable')
  return a
}

// metricCells 生成展开行的指标格（顺序与文案固定，便于横向对比）。
function metricCells(m) {
  return [
    { k: '2xx', v: m.status_2xx },
    { k: '3xx', v: m.status_3xx },
    { k: '4xx', v: m.status_4xx, cls: m.rate_4xx >= 0.5 ? 'danger' : '' },
    { k: '5xx', v: m.status_5xx, cls: m.rate_5xx >= 0.5 ? 'danger' : '' },
    { k: t('isolation.locationRewrites'), v: m.location_rewrites },
    { k: t('isolation.cookieRewrites'), v: m.cookie_rewrites },
    { k: t('isolation.cookiesDropped'), v: m.cookies_dropped },
    { k: t('isolation.htmlHits'), v: m.html_hits },
    { k: t('isolation.htmlFailures'), v: m.html_failures, cls: m.html_failures ? 'danger' : '' },
    { k: t('isolation.bodySkipped'), v: m.body_skipped },
    { k: t('isolation.backendErrors'), v: m.backend_errors, cls: m.backend_errors ? 'danger' : '' },
    { k: t('isolation.breakerTrips'), v: m.breaker_trips },
    { k: t('isolation.loopAlerts'), v: m.loop_alerts, cls: m.loop_alerts ? 'danger' : '' },
    { k: t('isolation.absPrefixHits'), v: m.abs_prefix_hits, cls: m.abs_prefix_hits ? 'danger' : '' }
  ]
}

async function load() {
  loading.value = true
  try {
    const res = await api.isolationServices()
    const d = res.data || {}
    enabled.value = !!d.enabled
    services.value = d.services || []
    warnings.value = d.warnings || []
    hash.value = d.hash || ''
    loadedAt.value = d.loaded_at || ''
    auditFile.value = d.audit_file || ''
  } catch (e) {
    ElMessage.error(e?.response?.data?.message || t('common.failed'))
  } finally {
    loading.value = false
    loaded.value = true
  }
}

async function loadAudit() {
  auditLoading.value = true
  try {
    const res = await api.isolationAudit(50)
    audit.value = res.data?.entries || []
    if (res.data?.file) auditFile.value = res.data.file
  } catch (e) {
  } finally {
    auditLoading.value = false
  }
}

async function changeMode(row, mode) {
  if (mode === row.mode) return
  try {
    if (mode === 'isolated') {
      await ElMessageBox.confirm(
        t('isolation.confirmDowngrade', { name: row.name }),
        t('common.warning'),
        { type: 'warning' }
      )
    }
    const res = await api.setIsolationMode(row.name, mode)
    if (res.code === 0) {
      ElMessage.success(t('common.success'))
      await Promise.all([load(), loadAudit()])
    } else {
      ElMessage.error(res.message || t('common.failed'))
    }
  } catch (e) {
    if (e !== 'cancel' && e?.response?.data?.message) ElMessage.error(e.response.data.message)
  }
}

async function downgrade(row) {
  await changeMode(row, 'isolated')
}

async function reloadConfig() {
  reloading.value = true
  try {
    const res = await api.isolationReload()
    if (res.code === 0) {
      const n = res.data?.count ?? 0
      ElMessage.success(t('isolation.reloaded', { n }))
      await Promise.all([load(), loadAudit()])
    } else {
      ElMessage.error(res.message || t('common.failed'))
    }
  } catch (e) {
    ElMessage.error(e?.response?.data?.message || t('common.failed'))
  } finally {
    reloading.value = false
  }
}

function startTimer() {
  stopTimer()
  timer = setInterval(() => {
    if (autoRefresh.value) load()
  }, 5000)
}
function stopTimer() {
  if (timer) {
    clearInterval(timer)
    timer = null
  }
}

watch(autoRefresh, (v) => {
  if (v) load()
})

onMounted(async () => {
  await load()
  await loadAudit()
  startTimer()
})
onBeforeUnmount(stopTimer)
</script>

<style scoped>
.kpi-row {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(190px, 1fr));
  gap: 14px;
  margin-bottom: 14px;
}
.kpi {
  padding: 14px 16px;
}
.kpi-label {
  font-size: 12px;
  color: var(--gh-text-dim);
  margin-bottom: 6px;
}
.kpi-value {
  font-size: 24px;
  font-weight: 700;
  line-height: 1.2;
  background: linear-gradient(120deg, var(--gh-primary-soft), var(--gh-primary2));
  -webkit-background-clip: text;
  background-clip: text;
  -webkit-text-fill-color: transparent;
}
.kpi-value.danger {
  background: none;
  -webkit-text-fill-color: var(--gh-red);
  color: var(--gh-red);
}

.toolbar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  margin-bottom: 14px;
  flex-wrap: wrap;
}
.tools {
  display: flex;
  align-items: center;
  gap: 10px;
}
.warn-list {
  margin: 6px 0 0;
  padding-left: 18px;
  font-size: 12px;
  line-height: 1.7;
}
.mono {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 12px;
}
.name {
  font-weight: 600;
  font-size: 14px;
}
.danger {
  color: var(--gh-red);
  font-weight: 600;
}

/* 展开区 */
.expand {
  padding: 6px 14px 14px;
}
.kv-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
  gap: 8px 18px;
  margin-bottom: 12px;
}
.kv {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12px;
}
.kv .k {
  color: var(--gh-text-dim);
  min-width: 76px;
}
.metrics-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(96px, 1fr));
  gap: 10px;
}
.cell {
  background: var(--gh-track);
  border: 1px solid var(--gh-border);
  border-radius: 8px;
  padding: 8px 10px;
  text-align: center;
}
.cell-v {
  font-size: 16px;
  font-weight: 700;
}
.cell-k {
  font-size: 11px;
  color: var(--gh-text-dim);
  margin-top: 2px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.abs {
  margin-top: 12px;
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}
.compat {
  margin-top: 8px;
}

/* 审计 */
.audit-panel {
  margin-top: 14px;
}
.from {
  color: var(--gh-text-dim);
}
.arrow {
  color: var(--gh-primary);
}
.to {
  font-weight: 600;
}
</style>
