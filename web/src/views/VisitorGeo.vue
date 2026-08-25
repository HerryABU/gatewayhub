<template>
  <div>
    <div class="gh-grid gh-grid-4">
      <div class="gh-panel gh-stat">
        <div class="gh-stat-icon">🌐</div>
        <div class="gh-num">{{ fmt(data.total) }}</div>
        <div class="gh-stat-label">{{ t('geo.total') }}</div>
      </div>
      <div class="gh-panel gh-stat">
        <div class="gh-stat-icon">🇨🇳</div>
        <div class="gh-num">{{ fmt(data.china_total) }}</div>
        <div class="gh-stat-label">{{ t('geo.china') }}</div>
      </div>
      <div class="gh-panel gh-stat">
        <div class="gh-stat-icon">✈️</div>
        <div class="gh-num">{{ fmt(data.overseas_total) }}</div>
        <div class="gh-stat-label">{{ t('geo.overseas') }}</div>
      </div>
      <div class="gh-panel gh-stat">
        <div class="gh-stat-icon">📅</div>
        <div class="gh-num">{{ data.days || 7 }}</div>
        <div class="gh-stat-label">Days</div>
      </div>
    </div>

    <el-card class="gh-panel" shadow="never" style="margin-top:16px">
      <template #header>
        <div class="panel-head">
          <span class="gh-title">🌍 {{ t('geo.title') }}</span>
          <el-radio-group v-model="view" size="small">
            <el-radio-button value="china">{{ t('geo.chinaMap') }}</el-radio-button>
            <el-radio-button value="world">{{ t('geo.worldMap') }}</el-radio-button>
          </el-radio-group>
        </div>
      </template>
      <div ref="mapRef" class="map-chart"></div>
      <div class="gh-tip info">{{ t('geo.legalNote') }}</div>
    </el-card>

    <div class="gh-grid gh-grid-2" style="margin-top:16px">
      <el-card class="gh-panel" shadow="never">
        <template #header><span class="gh-title">🏙️ {{ t('geo.topCities') }}</span></template>
        <div v-for="(c, i) in data.cities" :key="i" class="rank-row">
          <span class="rank">{{ i + 1 }}</span>
          <span class="rank-name">{{ c.city }}</span>
          <span class="gh-dim">{{ c.province }}</span>
          <span class="rank-val">{{ fmt(c.value) }}</span>
        </div>
      </el-card>
      <el-card class="gh-panel" shadow="never">
        <template #header><span class="gh-title">✈️ {{ t('geo.overseas') }}</span></template>
        <div v-for="(o, i) in data.overseas" :key="i" class="rank-row">
          <span class="rank">{{ i + 1 }}</span>
          <span class="rank-name">{{ locale === 'zh-CN' ? o.name_cn : (o.name_en || o.name_cn) }}</span>
          <span class="rank-val">{{ fmt(o.value) }}</span>
        </div>
        <div v-if="!data.overseas || !data.overseas.length" class="gh-dim">—</div>
      </el-card>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted, onUnmounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import * as echarts from 'echarts'
import api from '../api'
import chinaGeo from '../assets/china.json'
import worldGeo from '../assets/world.json'

const { t, locale } = useI18n()
const view = ref('china')
const data = ref({ total: 0, china_total: 0, overseas_total: 0, provinces: [], cities: [], overseas: [], days: 7 })
const mapRef = ref()
let chart = null

function fmt(n) {
  return Number(n || 0).toLocaleString('zh-CN')
}

async function load() {
  try {
    const res = await api.geo(7)
    if (res.code === 0) data.value = res.data
  } catch (e) {}
}

function chinaOption() {
  const provinces = data.value.provinces || []
  const max = Math.max(1, ...provinces.map((p) => p.value))
  return {
    tooltip: {
      trigger: 'item',
      formatter: (p) => `${p.name}<br/>${t('geo.visits')}: <b>${fmt(p.value || 0)}</b>`
    },
    visualMap: {
      min: 0,
      max,
      left: 20,
      bottom: 20,
      text: ['High', 'Low'],
      calculable: true,
      inRange: { color: ['#0f172a', '#0ea5c9', '#22d3ee', '#a5f3fc'] }
    },
    series: [
      {
        name: t('geo.visits'),
        type: 'map',
        map: 'china',
        roam: true,
        label: { show: false },
        emphasis: { label: { show: true, color: '#fff' }, itemStyle: { areaColor: '#0ea5c9' } },
        itemStyle: { borderColor: '#334155', areaColor: '#16213a' },
        data: provinces
      }
    ]
  }
}

function worldOption() {
  const overseas = data.value.overseas || []
  const mapData = overseas.map((o) => ({ name: o.name_en, value: o.value }))
  mapData.push({ name: 'China', value: data.value.china_total })
  const max = Math.max(1, ...mapData.map((d) => d.value))
  return {
    tooltip: {
      trigger: 'item',
      formatter: (p) => `${p.name}<br/>${t('geo.visits')}: <b>${fmt(p.value || 0)}</b>`
    },
    visualMap: {
      min: 0,
      max,
      left: 20,
      bottom: 20,
      text: ['High', 'Low'],
      calculable: true,
      inRange: { color: ['#0f172a', '#0ea5c9', '#22d3ee', '#a5f3fc'] }
    },
    series: [
      {
        name: t('geo.visits'),
        type: 'map',
        map: 'world',
        roam: true,
        scaleLimit: { min: 1, max: 8 },
        label: { show: false },
        emphasis: { label: { show: true, color: '#fff' }, itemStyle: { areaColor: '#0ea5c9' } },
        itemStyle: { borderColor: '#334155', areaColor: '#16213a' },
        data: mapData
      }
    ]
  }
}

function render() {
  if (!mapRef.value) return
  if (!chart) {
    chart = echarts.init(mapRef.value)
    echarts.registerMap('china', chinaGeo)
    echarts.registerMap('world', worldGeo)
  }
  chart.setOption(view.value === 'china' ? chinaOption() : worldOption(), true)
}

watch(view, () => render())

function onResize() {
  chart && chart.resize()
}

onMounted(() => {
  load().then(() => render())
  window.addEventListener('resize', onResize)
})
onUnmounted(() => {
  window.removeEventListener('resize', onResize)
  chart && chart.dispose()
})
</script>

<style scoped>
.panel-head { display: flex; justify-content: space-between; align-items: center; margin-bottom: 12px; }
.map-chart { height: 520px; }
.rank-row {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 8px 4px;
  border-bottom: 1px solid var(--gh-border);
}
.rank-row:last-child { border-bottom: none; }
.rank {
  width: 22px;
  height: 22px;
  border-radius: 6px;
  background: rgba(34, 211, 238, 0.12);
  color: #67e8f9;
  display: grid;
  place-items: center;
  font-size: 12px;
  font-weight: 700;
}
.rank-name { flex: 1; font-weight: 600; color: var(--gh-text); }
.rank-val { font-family: 'JetBrains Mono', monospace; color: #67e8f9; }
</style>
