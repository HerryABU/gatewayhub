<template>
  <div>
    <el-card class="gh-panel" shadow="never">
      <template #header><span class="gh-title">🗄️ {{ t('migration.title') }}</span></template>
      <div class="gh-tip info">{{ t('migration.migrateNote') }}</div>

      <el-form label-width="130px" style="margin-top:16px">
        <el-form-item :label="t('migration.currentDB')">
          <span class="gh-tag">{{ info.driver }}</span>
          <span class="gh-dim" style="margin-left:10px">{{ info.dsn }}</span>
        </el-form-item>
        <el-form-item :label="t('migration.driver')">
          <el-radio-group v-model="form.driver">
            <el-radio-button value="mysql">MySQL</el-radio-button>
            <el-radio-button value="sqlite">SQLite</el-radio-button>
          </el-radio-group>
        </el-form-item>
        <template v-if="form.driver === 'mysql'">
          <el-form-item :label="t('migration.host')"><el-input v-model="form.host" placeholder="127.0.0.1" style="max-width:360px" /></el-form-item>
          <el-form-item :label="t('migration.port')"><el-input-number v-model="form.port" :min="1" :max="65535" /></el-form-item>
          <el-form-item :label="t('migration.user')"><el-input v-model="form.user" placeholder="root" style="max-width:360px" /></el-form-item>
          <el-form-item :label="t('migration.password')"><el-input v-model="form.password" type="password" show-password style="max-width:360px" /></el-form-item>
          <el-form-item :label="t('migration.database')"><el-input v-model="form.database" placeholder="gatewayhub" style="max-width:360px" /></el-form-item>
        </template>
        <template v-else>
          <el-form-item :label="t('migration.path')"><el-input v-model="form.path" placeholder="gateway_new.db" style="max-width:360px" /></el-form-item>
        </template>
      </el-form>

      <div style="display:flex;gap:10px;margin-top:8px">
        <el-button :loading="testing" @click="test">{{ t('migration.testConnection') }}</el-button>
        <el-button type="primary" :loading="migrating" @click="run">{{ t('migration.startMigration') }}</el-button>
      </div>
    </el-card>
  </div>
</template>

<script setup>
import { ref, reactive, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessage, ElMessageBox } from 'element-plus'
import api from '../api'

const { t } = useI18n()
const info = ref({ driver: '', dsn: '' })
const testing = ref(false)
const migrating = ref(false)
const form = reactive({ driver: 'mysql', host: '127.0.0.1', port: 3306, user: 'root', password: '', database: 'gatewayhub', path: '' })

onMounted(async () => {
  const res = await api.migrateInfo()
  if (res.code === 0) info.value = res.data
})

async function test() {
  testing.value = true
  try {
    const res = await api.migrateTest(form)
    if (res.code === 0) ElMessage.success(t('migration.connectionOk'))
    else ElMessage.error(res.message || t('migration.connectionFailed'))
  } catch (e) {
    ElMessage.error(e?.response?.data?.message || t('migration.connectionFailed'))
  } finally {
    testing.value = false
  }
}

async function run() {
  await ElMessageBox.confirm(t('migration.migrateNote'), t('common.warning'), { type: 'warning' })
  migrating.value = true
  try {
    const res = await api.migrateRun(form)
    if (res.code === 0) {
      ElMessage.success(t('migration.migrateSuccess'))
    } else {
      ElMessage.error(res.message || t('common.failed'))
    }
  } catch (e) {
    ElMessage.error(e?.response?.data?.message || t('common.failed'))
  } finally {
    migrating.value = false
  }
}
</script>
