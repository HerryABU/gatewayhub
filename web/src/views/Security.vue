<template>
  <div>
    <div class="gh-tip info">🛡️ DDoS / CC · WAF · IP/API 黑白名单</div>

    <div class="gh-grid gh-grid-2" style="margin-top:16px">
      <el-card class="gh-panel" shadow="never">
        <template #header>
          <div class="panel-head">
            <span class="gh-title">{{ t('security.ipRules') }}</span>
            <el-button size="small" type="primary" @click="openAdd('ip')">➕ {{ t('security.addRule') }}</el-button>
          </div>
        </template>
        <el-table :data="ipRules" v-loading="loading">
          <el-table-column prop="ip" :label="t('security.ip')" min-width="150">
            <template #default="{ row }"><span class="gh-tag">{{ row.ip }}</span></template>
          </el-table-column>
          <el-table-column :label="t('common.action')" width="90" align="center">
            <template #default="{ row }">
              <span :class="'gh-badge ' + (row.action === 'allow' ? 'green' : 'red')">
                {{ row.action === 'allow' ? t('security.allow') : t('security.deny') }}
              </span>
            </template>
          </el-table-column>
          <el-table-column prop="note" :label="t('common.note')" min-width="120" />
          <el-table-column width="70" align="center">
            <template #default="{ row }">
              <el-button size="small" type="danger" text @click="del('ip', row.id)">{{ t('common.delete') }}</el-button>
            </template>
          </el-table-column>
        </el-table>
      </el-card>

      <el-card class="gh-panel" shadow="never">
        <template #header>
          <div class="panel-head">
            <span class="gh-title">{{ t('security.apiRules') }}</span>
            <el-button size="small" type="primary" @click="openAdd('api')">➕ {{ t('security.addRule') }}</el-button>
          </div>
        </template>
        <el-table :data="apiRules" v-loading="loading">
          <el-table-column prop="path" :label="t('security.path')" min-width="150">
            <template #default="{ row }"><span class="gh-tag">{{ row.path }}</span></template>
          </el-table-column>
          <el-table-column :label="t('common.action')" width="90" align="center">
            <template #default="{ row }">
              <span :class="'gh-badge ' + (row.action === 'allow' ? 'green' : 'red')">
                {{ row.action === 'allow' ? t('security.allow') : t('security.deny') }}
              </span>
            </template>
          </el-table-column>
          <el-table-column prop="note" :label="t('common.note')" min-width="120" />
          <el-table-column width="70" align="center">
            <template #default="{ row }">
              <el-button size="small" type="danger" text @click="del('api', row.id)">{{ t('common.delete') }}</el-button>
            </template>
          </el-table-column>
        </el-table>
      </el-card>
    </div>

    <el-dialog v-model="dialogVisible" :title="t('security.addRule')" width="440px">
      <el-form :model="form" label-width="100px">
        <el-form-item :label="addType === 'ip' ? t('security.ip') : t('security.path')">
          <el-input v-model="form.value" :placeholder="addType === 'ip' ? t('security.ipHint') : t('security.pathHint')" />
        </el-form-item>
        <el-form-item :label="t('common.action')">
          <el-radio-group v-model="form.action">
            <el-radio value="deny">{{ t('security.deny') }}</el-radio>
            <el-radio value="allow">{{ t('security.allow') }}</el-radio>
          </el-radio-group>
        </el-form-item>
        <el-form-item :label="t('common.note')">
          <el-input v-model="form.note" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">{{ t('common.cancel') }}</el-button>
        <el-button type="primary" :loading="saving" @click="save">{{ t('common.confirm') }}</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, reactive, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessage } from 'element-plus'
import api from '../api'

const { t } = useI18n()
const ipRules = ref([])
const apiRules = ref([])
const loading = ref(false)
const dialogVisible = ref(false)
const addType = ref('ip')
const saving = ref(false)
const form = reactive({ value: '', action: 'deny', note: '' })

async function load() {
  loading.value = true
  try {
    const [ips, apis] = await Promise.all([api.listIPRules(), api.listAPIRules()])
    ipRules.value = ips.data || []
    apiRules.value = apis.data || []
  } finally {
    loading.value = false
  }
}

function openAdd(type) {
  addType.value = type
  Object.assign(form, { value: '', action: 'deny', note: '' })
  dialogVisible.value = true
}

async function save() {
  if (!form.value) return
  saving.value = true
  try {
    const payload = addType.value === 'ip' ? { ip: form.value, action: form.action, note: form.note } : { path: form.value, action: form.action, note: form.note }
    const res = addType.value === 'ip' ? await api.createIPRule(payload) : await api.createAPIRule(payload)
    if (res.code === 0) {
      ElMessage.success(t('common.success'))
      dialogVisible.value = false
      load()
    } else {
      ElMessage.error(res.message || t('common.failed'))
    }
  } catch (e) {
    ElMessage.error(e?.response?.data?.message || t('common.failed'))
  } finally {
    saving.value = false
  }
}

async function del(type, id) {
  const res = type === 'ip' ? await api.deleteIPRule(id) : await api.deleteAPIRule(id)
  if (res.code === 0) load()
}

onMounted(load)
</script>

<style scoped>
.panel-head { display: flex; justify-content: space-between; align-items: center; }
</style>
