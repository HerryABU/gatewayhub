<template>
  <div>
    <div class="gh-tip info">{{ meta.law }} · {{ meta.total_articles }} {{ t('compliance.articles') }}</div>

    <el-card class="gh-panel" shadow="never" style="margin-top:16px">
      <template #header><span class="gh-title">⚖️ {{ t('compliance.title') }}</span></template>
      <el-descriptions :column="1" border>
        <el-descriptions-item :label="t('compliance.logRetention')">
          {{ t('compliance.retentionText', { days: meta.log_retention_days }) }}
        </el-descriptions-item>
        <el-descriptions-item :label="t('compliance.dataMin')">
          {{ t('compliance.dataMinText') }}
        </el-descriptions-item>
        <el-descriptions-item :label="t('compliance.accessControl')">
          {{ t('compliance.accessControlText') }}
        </el-descriptions-item>
      </el-descriptions>
    </el-card>

    <el-card v-for="p in meta.provisions" :key="p.article" class="gh-panel" shadow="never" style="margin-top:16px">
      <template #header>
        <div style="display:flex;align-items:center;gap:10px">
          <el-tag type="primary">{{ p.article }}</el-tag>
          <span class="gh-title">{{ p.title }}</span>
        </div>
      </template>
      <p class="p-text">{{ p.text }}</p>
      <div class="gh-tip info">📌 {{ t('compliance.relation') }}：{{ p.relevance }}</div>
    </el-card>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import api from '../api'

const { t } = useI18n()
const meta = ref({ law: '', note: '', log_retention_days: 180, provisions: [], total_articles: 81 })

onMounted(async () => {
  try {
    const res = await api.compliance()
    if (res.code === 0) meta.value = res.data
  } catch (e) {}
})
</script>

<style scoped>
.p-text { color: #b6c2d6; line-height: 1.9; font-size: 14px; margin: 4px 0 12px; }
</style>
