<template>
  <div>
    <div class="gh-panel">
      <div class="toolbar">
        <div>
          <span class="gh-title">{{ t('route.list') }}</span>
          <div class="gh-dim">{{ t('route.hotReload') }}</div>
        </div>
        <el-button type="primary" @click="openCreate">➕ {{ t('route.addRoute') }}</el-button>
      </div>
      <el-table :data="routes" v-loading="loading">
        <el-table-column type="index" width="56" align="center" />
        <el-table-column prop="name" :label="t('route.name')" min-width="140" />
        <el-table-column :label="t('route.prefix')" min-width="120">
          <template #default="{ row }"><span class="gh-tag">/{{ row.prefix }}</span></template>
        </el-table-column>
        <el-table-column prop="description" :label="t('route.description')" min-width="180" show-overflow-tooltip>
          <template #default="{ row }">
            <span v-if="row.description" class="row-desc">{{ row.description }}</span>
            <span v-else class="gh-dim">—</span>
          </template>
        </el-table-column>
        <el-table-column prop="target" :label="t('route.target')" min-width="150" show-overflow-tooltip />
        <el-table-column :label="t('route.timeout')" width="80" align="center">
          <template #default="{ row }">{{ row.timeout }}s</template>
        </el-table-column>
        <el-table-column :label="t('common.status')" width="90" align="center">
          <template #default="{ row }">
            <el-switch :model-value="row.status === 'active'" @change="(v) => toggleStatus(row, v)" />
          </template>
        </el-table-column>
        <el-table-column :label="t('common.health')" width="110" align="center">
          <template #default="{ row }">
            <span :class="'gh-badge ' + healthColor(row.health)">{{ healthText(row.health) }}</span>
          </template>
        </el-table-column>
        <el-table-column :label="t('common.action')" width="150" align="center">
          <template #default="{ row }">
            <el-button size="small" @click="openEdit(row)">{{ t('common.edit') }}</el-button>
            <el-button size="small" type="danger" @click="removeRoute(row)">{{ t('common.delete') }}</el-button>
          </template>
        </el-table-column>
      </el-table>
    </div>

    <el-dialog v-model="dialogVisible" :title="isEdit ? t('route.editRoute') : t('route.addRoute')" width="520px">
      <el-form :model="form" label-width="100px" :rules="rules" ref="formRef">
        <el-form-item :label="t('route.name')" prop="name">
          <el-input v-model="form.name" placeholder="Java-Order" />
        </el-form-item>
        <el-form-item :label="t('route.prefix')" prop="prefix">
          <el-input v-model="form.prefix" placeholder="java-order" :disabled="isEdit" />
          <div class="gh-dim">{{ t('route.prefixHint') }}</div>
        </el-form-item>
        <el-form-item :label="t('route.target')" prop="target">
          <el-input v-model="form.target" :placeholder="t('route.targetHint')" />
        </el-form-item>
        <el-form-item :label="t('route.description')">
          <el-input
            v-model="form.description"
            type="textarea"
            :rows="2"
            maxlength="500"
            show-word-limit
            :placeholder="t('route.descriptionHint')"
          />
        </el-form-item>
        <el-form-item :label="t('route.timeout')">
          <el-input-number v-model="form.timeout" :min="1" :max="60" /> <span class="gh-dim">s</span>
        </el-form-item>
        <el-form-item :label="t('route.interval')">
          <el-input-number v-model="form.interval" :min="5" :max="86400" :step="5" /> <span class="gh-dim">s</span>
          <div class="gh-dim">{{ t('route.intervalHint') }}</div>
        </el-form-item>
        <el-form-item :label="t('common.status')">
          <el-radio-group v-model="form.status">
            <el-radio value="active">{{ t('common.enabled') }}</el-radio>
            <el-radio value="inactive">{{ t('common.disabled') }}</el-radio>
          </el-radio-group>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">{{ t('common.cancel') }}</el-button>
        <el-button type="primary" :loading="saving" @click="save">{{ isEdit ? t('common.confirm') : t('common.confirm') }}</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, reactive, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessage, ElMessageBox } from 'element-plus'
import api from '../api'

const { t } = useI18n()
const routes = ref([])
const loading = ref(false)
const dialogVisible = ref(false)
const isEdit = ref(false)
const saving = ref(false)
const formRef = ref()
const form = reactive({ name: '', prefix: '', target: '', description: '', timeout: 5, interval: 30, status: 'active' })

const rules = {
  name: [{ required: true, message: 'Name', trigger: 'blur' }],
  prefix: [{ required: true, message: 'Prefix', trigger: 'blur' }],
  target: [{ required: true, message: 'Target', trigger: 'blur' }]
}

async function load() {
  loading.value = true
  try {
    const res = await api.listRoutes()
    routes.value = res.data || []
  } finally {
    loading.value = false
  }
}

function openCreate() {
  isEdit.value = false
  Object.assign(form, { name: '', prefix: '', target: '', description: '', timeout: 5, interval: 30, status: 'active' })
  dialogVisible.value = true
}
function openEdit(row) {
  isEdit.value = true
  Object.assign(form, { name: row.name, prefix: row.prefix, target: row.target, description: row.description || '', timeout: row.timeout, interval: row.interval || 30, status: row.status })
  dialogVisible.value = true
}

async function save() {
  await formRef.value.validate()
  saving.value = true
  try {
    const res = isEdit.value ? await api.updateRoute(form.prefix, form) : await api.createRoute(form)
    if (res.code === 0) {
      ElMessage.success(t('common.success'))
      dialogVisible.value = false
      load()
    } else {
      ElMessage.error(res.message || t('common.failed'))
    }
  } catch (e) {
    if (e?.response?.data?.message) ElMessage.error(e.response.data.message)
  } finally {
    saving.value = false
  }
}

async function toggleStatus(row, v) {
  try {
    const res = await api.updateRouteStatus(row.prefix, v ? 'active' : 'inactive')
    if (res.code === 0) load()
  } catch (e) {}
}

async function removeRoute(row) {
  await ElMessageBox.confirm(t('route.confirmDelete', { name: row.name }), t('common.warning'), { type: 'warning' })
  const res = await api.deleteRoute(row.prefix)
  if (res.code === 0) {
    ElMessage.success(t('common.success'))
    load()
  }
}

function healthColor(h) {
  if (h === 'healthy') return 'green'
  if (h === 'down' || h === 'unhealthy') return 'red'
  if (h === 'warning') return 'orange'
  return 'gray'
}
function healthText(h) {
  if (h === 'healthy') return t('health.healthy')
  if (h === 'down' || h === 'unhealthy') return t('health.down')
  if (h === 'warning') return t('health.warning')
  return t('health.unknown')
}

onMounted(load)
</script>

<style scoped>
.toolbar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 14px;
}
.row-desc {
  font-size: 12px;
  color: var(--gh-text-dim);
  line-height: 1.5;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}
</style>
