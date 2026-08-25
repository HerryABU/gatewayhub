<template>
  <div class="uptime-bar" :class="{ empty: !segments.length }" :style="{ height: height + 'px' }">
    <span
      v-for="(p, i) in segments"
      :key="i"
      class="seg"
      :class="segColor(p.status)"
      :title="segTitle(p)"
    ></span>
    <span v-if="!segments.length" class="seg none" :title="emptyTitle"></span>
  </div>
</template>

<script setup>
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

// 视觉设计参考 Uptime Kuma（MIT，Copyright (c) 2021 Louis Lam）的 HeartbeatBar：
// 横向由多段竖条组成、绿/橙/红/灰分段历史，右端最新。详见 THIRD_PARTY_NOTICES.md。

const props = defineProps({
  history: { type: Array, default: () => [] }, // [{status, latency_ms, time}]
  height: { type: Number, default: 22 }
})

const { t } = useI18n()

// 最近一次在前（右侧）——倒序展示，越靠右越新
const segments = computed(() => {
  const h = props.history || []
  return [...h].reverse().slice(0, 90)
})

const emptyTitle = computed(() => t('health.unknown'))

function segColor(status) {
  if (status === 'healthy') return 'green'
  if (status === 'warning') return 'orange'
  if (status === 'down') return 'red'
  return 'gray'
}

function segTitle(p) {
  const when = p.time ? new Date(p.time).toLocaleTimeString() : ''
  const ms = p.latency_ms != null ? `${p.latency_ms}ms` : ''
  return `${when} · ${labelOf(p.status)}${ms ? ' · ' + ms : ''}`
}

function labelOf(status) {
  const map = {
    healthy: t('health.healthy'),
    warning: t('health.warning'),
    down: t('health.down'),
    unknown: t('health.unknown')
  }
  return map[status] || status
}
</script>

<style scoped>
/* 分段 uptime 状态条：横向由若干竖段组成，绿/橙/红/灰（右端最新） */
.uptime-bar {
  flex: 1;
  display: flex;
  gap: 2px;
  align-items: stretch;
  padding: 2px;
  border-radius: 6px;
  background: var(--gh-track);
  overflow: hidden;
}
.uptime-bar .seg {
  flex: 1 1 0;
  min-width: 0;
  border-radius: 2px;
}
.uptime-bar .seg.green { background: var(--gh-green); }
.uptime-bar .seg.orange { background: var(--gh-orange); }
.uptime-bar .seg.red { background: var(--gh-red); }
.uptime-bar .seg.gray, .uptime-bar .seg.none { background: var(--gh-gray); }
.uptime-bar .seg.none { flex: 1; opacity: 0.5; }
</style>
