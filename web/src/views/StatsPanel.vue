<template>
  <div>
    <div class="gh-grid gh-grid-4">
      <div class="gh-panel gh-stat">
        <div class="gh-stat-icon">📊</div>
        <div class="gh-num">{{ fmt(overview.total_pv) }}</div>
        <div class="gh-stat-label">{{ t('stats.totalPV') }}</div>
      </div>
      <div class="gh-panel gh-stat">
        <div class="gh-stat-icon">📈</div>
        <div class="gh-num">{{ fmt(overview.today_pv) }}</div>
        <div class="gh-stat-label">{{ t('stats.todayPV') }}</div>
      </div>
      <div class="gh-panel gh-stat">
        <div class="gh-stat-icon">📋</div>
        <div class="gh-num">{{ overview.total_routes }}</div>
        <div class="gh-stat-label">{{ t('stats.totalRoutes') }}</div>
      </div>
      <div class="gh-panel gh-stat">
        <div class="gh-stat-icon">❤️</div>
        <div class="gh-num">{{ (overview.health_rate || 0).toFixed(1) }}%</div>
        <div class="gh-stat-label">{{ t('stats.healthRate') }}</div>
      </div>
    </div>

    <el-card class="gh-panel" shadow="never" style="margin-top:16px">
      <template #header>
        <div class="panel-head">
          <span class="gh-title">📈 {{ t('stats.trend') }}</span>
          <el-radio-group v-model="trendDays" size="small" @change="loadTrend">
            <el-radio-button :value="7">{{ t('stats.last7') }}</el-radio-button>
            <el-radio-button :value="30">{{ t('stats.last30') }}</el-radio-button>
          </el-radio-group>
        </div>
      </template>
      <div ref="trendRef" class="trend-chart"></div>
    </el-card>

    <el-card class="gh-panel" shadow="never" style="margin-top:16px">
      <template #header>
        <div class="panel-head">
          <span class="gh-title">📋 {{ t('stats.routePVDetail') }}</span>
          <el-button size="small" type="danger" plain @click="openCleanup">{{ t('stats.cleanup') }}</el-button>
        </div>
      </template>
      <el-table :data="routeStats" v-loading="loading" @row-click="openDetail">
        <el-table-column type="index" :label="t('stats.rank')" width="70" align="center" />
        <el-table-column :label="t('route.prefix')" min-width="140">
          <template #default="{ row }"><span class="gh-tag">/{{ row.prefix }}</span></template>
        </el-table-column>
        <el-table-column prop="name" :label="t('route.name')" min-width="150" />
        <el-table-column :label="t('stats.totalPV')" min-width="100" align="right">
          <template #default="{ row }">{{ fmt(row.total_pv) }}</template>
        </el-table-column>
        <el-table-column :label="t('stats.todayPV')" min-width="90" align="right">
          <template #default="{ row }">{{ fmt(row.today_pv) }}</template>
        </el-table-column>
        <el-table-column :label="t('stats.yesterdayPV')" min-width="90" align="right">
          <template #default="{ row }">{{ fmt(row.yesterday_pv) }}</template>
        </el-table-column>
        <el-table-column :label="t('stats.trend7')" min-width="150" align="center">
          <template #default="{ row }"><Sparkline :data="row.trend" color="#22d3ee" /></template>
        </el-table-column>
      </el-table>
    </el-card>

    <el-dialog v-model="detailVisible" :title="`${detailName} · ${t('stats.detailTitle')}`" width="640px">
      <div ref="detailRef" class="detail-chart"></div>
    </el-dialog>

    <el-dialog v-model="cleanupVisible" :title="t('stats.cleanup')" width="440px">
      <div class="gh-tip warn">{{ t('stats.cleanupTip') }}</div>
      <el-form label-width="100px" style="margin-top:14px">
        <el-form-item :label="t('stats.retainDays')">
          <el-input-number v-model="retainDays" :min="1" :max="3650" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="cleanupVisible = false">{{ t('common.cancel') }}</el-button>
        <el-button type="danger" :loading="cleaning" @click="doCleanup">{{ t('common.confirm') }}</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, onMounted, nextTick, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessage } from 'element-plus'
import * as echarts from 'echarts'
import api from '../api'
import Sparkline from '../components/Sparkline.vue'

const { t } = useI18n()
const overview = ref({})
const routeStats = ref([])
const loading = ref(false)
const trendDays = ref(7)
const trendRef = ref()
const detailRef = ref()
let trendChart = null
let detailChart = null
const detailVisible = ref(false)
const detailName = ref('')
const cleanupVisible = ref(false)
const retainDays = ref(180)
const cleaning = ref(false)

function fmt(n) {
  if (n === null || n === undefined) return '0'
  return Number(n).toLocaleString('zh-CN')
}

async function loadOverview() {
  try {
    const res = await api.overview()
    if (res.code === 0) overview.value = res.data
  } catch (e) {}
}
async function loadRoutes() {
  loading.value = true
  try {
    const res = await api.routesStats(7)
    if (res.code === 0) routeStats.value = (res.data.routes || []).sort((a, b) => b.total_pv - a.total_pv)
  } finally {
    loading.value = false
  }
}
async function loadTrend() {
  try {
    const r = await api.routesStats(trendDays.value)
    if (r.code === 0) {
      const list = r.data.routes || []
      const len = list.length ? list[0].trend.length : 0
      const values = []
      for (let i = 0; i < len; i++) {
        let sum = 0
        list.forEach((x) => (sum += x.trend[i] || 0))
        values.push(sum)
      }
      const tr = await api.trend(list.length ? list[0].prefix : '', trendDays.value)
      const lbl = tr.code === 0 ? tr.data.labels : []
      renderTrend(lbl, values)
    }
  } catch (e) {}
}

function renderTrend(labels, values) {
  if (!trendChart && trendRef.value) trendChart = echarts.init(trendRef.value)
  if (!trendChart) return
  trendChart.setOption({
    tooltip: { trigger: 'axis' },
    grid: { left: 50, right: 20, top: 30, bottom: 30 },
    xAxis: { type: 'category', data: labels, boundaryGap: false, axisLine: { lineStyle: { color: '#1c2740' } }, axisLabel: { color: '#8494ad' } },
    yAxis: { type: 'value', splitLine: { lineStyle: { color: '#1c2740' } }, axisLabel: { color: '#8494ad' } },
    series: [
      {
        name: t('stats.totalPV'),
        type: 'line',
        smooth: true,
        data: values,
        areaStyle: {
          color: new echarts.graphic.LinearGradient(0, 0, 0, 1, [
            { offset: 0, color: 'rgba(34,211,238,0.35)' },
            { offset: 1, color: 'rgba(34,211,238,0)' }
          ])
        },
        itemStyle: { color: '#22d3ee' },
        lineStyle: { color: '#22d3ee', shadowColor: 'rgba(34,211,238,0.4)', shadowBlur: 12 }
      }
    ]
  })
}

async function openDetail(row) {
  detailName.value = `${row.name}（/${row.prefix}）`
  detailVisible.value = true
  await nextTick()
  const res = await api.trend(row.prefix, 7)
  if (res.code === 0) {
    if (!detailChart && detailRef.value) detailChart = echarts.init(detailRef.value)
    detailChart &&
      detailChart.setOption({
        tooltip: { trigger: 'axis' },
        grid: { left: 50, right: 20, top: 30, bottom: 30 },
        xAxis: { type: 'category', data: res.data.labels, axisLabel: { color: '#8494ad' } },
        yAxis: { type: 'value', splitLine: { lineStyle: { color: '#1c2740' } } },
        series: [{ type: 'line', smooth: true, data: res.data.values, areaStyle: { opacity: 0.15 }, itemStyle: { color: '#34d399' }, lineStyle: { color: '#34d399' } }]
      })
  }
}

function openCleanup() {
  retainDays.value = 180
  cleanupVisible.value = true
}
async function doCleanup() {
  cleaning.value = true
  try {
    const res = await api.cleanup(retainDays.value)
    if (res.code === 0) {
      ElMessage.success(t('common.success'))
      cleanupVisible.value = false
      loadOverview()
      loadRoutes()
    } else {
      ElMessage.error(res.message || t('common.failed'))
    }
  } finally {
    cleaning.value = false
  }
}

function onResize() {
  trendChart && trendChart.resize()
  detailChart && detailChart.resize()
}

onMounted(() => {
  loadOverview()
  loadRoutes()
  loadTrend()
  window.addEventListener('resize', onResize)
})
onUnmounted(() => {
  window.removeEventListener('resize', onResize)
  trendChart && trendChart.dispose()
  detailChart && detailChart.dispose()
})
</script>

<style scoped>
.panel-head { display: flex; justify-content: space-between; align-items: center; }
.trend-chart { height: 320px; }
.detail-chart { height: 360px; }
</style>
