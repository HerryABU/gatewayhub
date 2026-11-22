<template>
  <div class="link-manager">
    <div class="lm-head">
      <div>
        <h2 class="lm-title">{{ t('nav.friendLinks') }}</h2>
        <p class="lm-hint">{{ t('linkmgr.hint') }}</p>
      </div>
      <el-button type="primary" @click="openAdd">+ {{ t('linkmgr.add') }}</el-button>
    </div>

    <el-table :data="links" v-loading="loading" class="lm-table">
      <el-table-column :label="t('linkmgr.name')" width="180">
        <template #default="{ row }">
          <span class="lm-name">{{ row.name }}</span>
        </template>
      </el-table-column>
      <el-table-column :label="t('linkmgr.url')" min-width="280">
        <template #default="{ row }">
          <a class="lm-url" :href="row.url" target="_blank" rel="noopener noreferrer">{{ row.url }} ↗</a>
        </template>
      </el-table-column>
      <el-table-column :label="t('linkmgr.desc')" min-width="220">
        <template #default="{ row }"><span class="lm-desc">{{ row.description }}</span></template>
      </el-table-column>
      <el-table-column :label="t('common.status')" width="90" align="center">
        <template #default><span class="gh-badge green">PUBLIC</span></template>
      </el-table-column>
      <el-table-column width="140" align="right">
        <template #default="{ row }">
          <el-button size="small" @click="openEdit(row)">{{ t('common.edit') }}</el-button>
          <el-button size="small" type="danger" plain @click="remove(row)">{{ t('common.delete') }}</el-button>
        </template>
      </el-table-column>
    </el-table>
    <div v-if="!loading && !links.length" class="lm-empty">{{ t('linkmgr.empty') }}</div>

    <el-dialog v-model="dialog" :title="editing ? t('linkmgr.edit') : t('linkmgr.add')" width="480px">
      <el-form label-width="80px" @submit.prevent>
        <el-form-item :label="t('linkmgr.name')">
          <el-input v-model="form.name" maxlength="40" :placeholder="t('linkmgr.nameHint')" />
        </el-form-item>
        <el-form-item :label="t('linkmgr.url')">
          <el-input v-model="form.url" :placeholder="t('linkmgr.urlHint')" />
        </el-form-item>
        <el-form-item :label="t('linkmgr.desc')">
          <el-input v-model="form.description" maxlength="120" :placeholder="t('linkmgr.descHint')" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialog = false">{{ t('common.cancel') }}</el-button>
        <el-button type="primary" :loading="saving" @click="submit">{{ t('common.confirm') }}</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessage, ElMessageBox } from 'element-plus'
import api from '../api'

const { t } = useI18n()
const links = ref([])
const loading = ref(false)
const saving = ref(false)
const dialog = ref(false)
const editing = ref(false)
const form = ref({ id: '', name: '', url: '', description: '' })

async function load() {
  loading.value = true
  try {
    const res = await api.listFriendLinks()
    if (res.code === 0) links.value = res.data || []
  } catch (e) {
    ElMessage.error(t('common.failed'))
  } finally {
    loading.value = false
  }
}

function openAdd() {
  editing.value = false
  form.value = { id: '', name: '', url: '', description: '' }
  dialog.value = true
}
function openEdit(row) {
  editing.value = true
  form.value = { id: row.id, name: row.name, url: row.url, description: row.description || '' }
  dialog.value = true
}

async function submit() {
  saving.value = true
  try {
    const payload = { name: form.value.name, url: form.value.url, description: form.value.description }
    const res = editing.value
      ? await api.updateFriendLink(form.value.id, payload)
      : await api.createFriendLink(payload)
    if (res.code === 0) {
      ElMessage.success(t('common.success'))
      dialog.value = false
      await load()
    } else {
      ElMessage.error(res.message || t('common.failed'))
    }
  } catch (e) {
    ElMessage.error(t('common.failed'))
  } finally {
    saving.value = false
  }
}

async function remove(row) {
  try {
    await ElMessageBox.confirm(t('linkmgr.deleteConfirm'), t('common.warning'), { type: 'warning' })
  } catch (e) {
    return
  }
  try {
    const res = await api.deleteFriendLink(row.id)
    if (res.code === 0) {
      ElMessage.success(t('common.success'))
      await load()
    } else {
      ElMessage.error(res.message || t('common.failed'))
    }
  } catch (e) {
    ElMessage.error(t('common.failed'))
  }
}

onMounted(load)
</script>

<style scoped>
.link-manager { padding: 4px 0; }
.lm-head { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; margin-bottom: 18px; }
.lm-title { margin: 0 0 6px; font-size: 18px; }
.lm-hint { margin: 0; font-size: 12px; color: var(--gh-text-dim); }
.lm-name { font-weight: 700; }
.lm-url { color: var(--gh-primary); text-decoration: none; font-size: 12px; word-break: break-all; }
.lm-url:hover { text-decoration: underline; }
.lm-desc { font-size: 12px; color: var(--gh-text-dim); }
.lm-empty { margin-top: 16px; text-align: center; padding: 28px; border: 1px dashed var(--gh-border); border-radius: 12px; color: var(--gh-text-dim); font-size: 13px; }
</style>
