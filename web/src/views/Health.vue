<template>
  <div>
    <el-card class="gh-panel" shadow="never">
      <template #header>
        <div class="panel-head">
          <span class="gh-title">🩺 {{ t('health.title') }}</span>
          <div style="display:flex;gap:10px;align-items:center">
            <span class="gh-dim">{{ t('health.autoCheck') }}</span>
            <el-button type="primary" size="small" :loading="checking" @click="checkNow">{{ t('health.checkNow') }}</el-button>
          </div>
        </div>
      </template>

      <div v-if="sites.length" class="site-list">
        <StatusBar
          v-for="s in sites"
          :key="s.prefix"
          :name="`${s.name} · /${s.prefix}`"
          :status="s.status"
          :latency="s.latency_ms"
          :history="s.history"
          :meta="s.enabled ? `${s.target} · ${s.latency_ms}ms · ${s.interval}s` : t('common.disabled')"
        />
      </div>
      <div v-else class="gh-dim" style="text-align:center;padding:30px">—</div>

      <div class="legend" style="margin-top:16px;display:flex;gap:20px;align-items:center">
        <span class="gh-badge green">■ {{ t('health.healthy') }}</span>
        <span class="gh-badge orange">■ {{ t('health.warning') }}</span>
        <span class="gh-badge red">■ {{ t('health.down') }}</span>
        <span class="gh-badge gray">■ {{ t('health.unknown') }}</span>
        <span class="gh-dim">← {{ t('health.older') }} · {{ t('health.newer') }} →</span>
      </div>
    </el-card>
  </div>
</template>

<script setup>
import { ref, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import api from '../api'
import StatusBar from '../components/StatusBar.vue'

const { t } = useI18n()
const sites = ref([])
const checking = ref(false)
let timer = null

async function load() {
  try {
    const res = await api.healthStatus()
    if (res.code === 0) sites.value = res.data || []
  } catch (e) {}
}

async function checkNow() {
  checking.value = true
  try {
    const res = await api.healthCheckNow()
    if (res.code === 0) sites.value = res.data || []
  } finally {
    checking.value = false
  }
}

onMounted(() => {
  load()
  timer = setInterval(load, 10000)
})
onUnmounted(() => clearInterval(timer))
</script>

<style scoped>
.panel-head { display: flex; justify-content: space-between; align-items: center; }
.site-list { display: flex; flex-direction: column; gap: 10px; }
</style>
