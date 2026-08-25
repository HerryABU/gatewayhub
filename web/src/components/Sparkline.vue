<template>
  <svg :width="width" :height="height" viewBox="0 0 100 30" preserveAspectRatio="none">
    <polyline
      :points="points"
      fill="none"
      :stroke="color"
      stroke-width="2"
      stroke-linecap="round"
      stroke-linejoin="round"
    />
  </svg>
</template>

<script setup>
import { computed } from 'vue'

const props = defineProps({
  data: { type: Array, default: () => [] },
  color: { type: String, default: '#409eff' },
  width: { type: Number, default: 100 },
  height: { type: Number, default: 30 }
})

const points = computed(() => {
  const d = props.data
  if (!d.length) return ''
  const max = Math.max(...d, 1)
  const min = Math.min(...d, 0)
  const range = max - min || 1
  const step = 100 / (d.length - 1 || 1)
  return d
    .map((v, i) => {
      const x = (i * step).toFixed(2)
      const y = (28 - ((v - min) / range) * 26).toFixed(2)
      return `${x},${y}`
    })
    .join(' ')
})
</script>
