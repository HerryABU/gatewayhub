<template>
  <div class="gh-bar">
    <div class="bar-info">
      <div class="bar-name">{{ name }}</div>
      <div class="bar-meta">{{ meta }}</div>
    </div>

    <UptimeBar :history="history" :height="22" />

    <span :class="'gh-badge ' + color">{{ label }}</span>
  </div>
</template>

<script setup>
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import UptimeBar from './UptimeBar.vue'

const props = defineProps({
  name: { type: String, default: '' },
  status: { type: String, default: 'unknown' }, // healthy/warning/down/unknown
  latency: { type: Number, default: 0 },
  meta: { type: String, default: '' },
  history: { type: Array, default: () => [] } // [{status, latency_ms, time}]
})

const { t } = useI18n()

const color = computed(() => {
  if (props.status === 'healthy') return 'green'
  if (props.status === 'warning') return 'orange'
  if (props.status === 'down') return 'red'
  return 'gray'
})

const label = computed(() => {
  const map = {
    healthy: t('health.healthy'),
    warning: t('health.warning'),
    down: t('health.down'),
    unknown: t('health.unknown')
  }
  return map[props.status] || props.status
})
</script>

<style scoped>
.bar-info { min-width: 180px; }
.bar-name { font-size: 14px; font-weight: 600; color: var(--gh-text); }
.bar-meta { font-size: 11px; color: var(--gh-text-dim); font-family: 'JetBrains Mono', monospace; }
</style>
