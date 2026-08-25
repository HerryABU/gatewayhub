<template>
  <div>
    <el-card class="gh-panel" shadow="never">
      <template #header>
        <div class="panel-head">
          <span class="gh-title">💾 {{ t('backup.title') }}</span>
          <div style="display:flex;gap:10px;align-items:center">
            <span class="gh-dim">{{ t('backup.scheduled') }}</span>
            <el-button type="primary" size="small" :loading="backing" @click="doBackup">{{ t('backup.manualBackup') }}</el-button>
          </div>
        </div>
      </template>
      <el-table :data="list" v-loading="loading">
        <el-table-column prop="filename" :label="t('backup.filename')" min-width="240" show-overflow-tooltip>
          <template #default="{ row }"><span class="gh-tag">{{ row.filename }}</span></template>
        </el-table-column>
        <el-table-column :label="t('backup.size')" width="110" align="right">
          <template #default="{ row }">{{ fmtSize(row.size) }}</template>
        </el-table-column>
        <el-table-column :label="t('backup.kind')" width="110" align="center">
          <template #default="{ row }">
            <span :class="'gh-badge ' + (row.kind === 'manual' ? 'green' : 'orange')">
              {{ row.kind === 'manual' ? t('backup.manual') : t('backup.scheduledKind') }}
            </span>
          </template>
        </el-table-column>
        <el-table-column :label="t('backup.createdAt')" min-width="170">
          <template #default="{ row }">{{ row.created_at }}</template>
        </el-table-column>
        <el-table-column :label="t('common.action')" width="150" align="center">
          <template #default="{ row }">
            <el-button size="small" @click="download(row)">{{ t('backup.download') }}</el-button>
            <el-button size="small" type="danger" text @click="del(row)">{{ t('backup.delete') }}</el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-card>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessage } from 'element-plus'
import api from '../api'

const { t } = useI18n()
const list = ref([])
const loading = ref(false)
const backing = ref(false)

function fmtSize(n) {
  if (n < 1024) return n + ' B'
  if (n < 1024 * 1024) return (n / 1024).toFixed(1) + ' KB'
  return (n / 1024 / 1024).toFixed(2) + ' MB'
}

async function load() {
  loading.value = true
  try {
    const res = await api.backupList()
    if (res.code === 0) list.value = res.data || []
  } finally {
    loading.value = false
  }
}

async function doBackup() {
  backing.value = true
  try {
    const res = await api.backupCreate()
    if (res.code === 0) {
      ElMessage.success(t('common.success'))
      load()
    } else {
      ElMessage.error(res.message || t('common.failed'))
    }
  } catch (e) {
    ElMessage.error(t('common.failed'))
  } finally {
    backing.value = false
  }
}

function download(row) {
  const name = row.filename.split(/[\\/]/).pop()
  const token = localStorage.getItem('gw_token')
  window.open(`/api/backup/download?file=${encodeURIComponent(name)}&_t=${token}`, '_blank')
}

async function del(row) {
  const res = await api.backupDelete(row.id)
  if (res.code === 0) load()
}

onMounted(load)
</script>

<style scoped>
.panel-head { display: flex; justify-content: space-between; align-items: center; }
</style>
