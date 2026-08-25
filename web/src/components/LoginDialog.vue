<template>
  <el-dialog v-model="visible" :title="t('login.title')" width="420px" :close-on-click-modal="false">
    <el-form :model="form" label-width="90px" @submit.prevent>
      <el-form-item :label="t('login.username')">
        <el-input v-model="form.username" placeholder="admin" autocomplete="username" />
      </el-form-item>
      <el-form-item :label="t('login.password')">
        <el-input
          v-model="form.password"
          type="password"
          show-password
          placeholder="admin123"
          autocomplete="current-password"
          @keyup.enter="submit"
        />
      </el-form-item>
      <el-form-item>
        <el-checkbox v-model="form.remember">{{ t('login.remember') }}</el-checkbox>
      </el-form-item>
    </el-form>
    <div class="login-hint">{{ t('login.hint') }}</div>
    <template #footer>
      <el-button @click="visible = false">{{ t('common.cancel') }}</el-button>
      <el-button type="primary" :loading="loading" @click="submit">{{ t('login.submit') }}</el-button>
    </template>
  </el-dialog>
</template>

<script setup>
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessage } from 'element-plus'
import { useRouter } from 'vue-router'
import api from '../api'
import { setAuth } from '../store'

const { t } = useI18n()
const visible = defineModel({ type: Boolean, default: false })
const router = useRouter()
const form = ref({ username: 'admin', password: '', remember: false })
const loading = ref(false)

async function submit() {
  if (!form.value.username || !form.value.password) {
    ElMessage.warning(t('login.username') + ' / ' + t('login.password'))
    return
  }
  loading.value = true
  try {
    const res = await api.login(form.value)
    if (res.code === 0) {
      setAuth(res.data)
      ElMessage.success(t('common.success'))
      visible.value = false
      router.push('/dashboard')
    } else {
      ElMessage.error(res.message || t('common.failed'))
    }
  } catch (e) {
    ElMessage.error(e?.response?.data?.message || t('common.failed'))
  } finally {
    loading.value = false
  }
}
</script>

<style scoped>
.login-hint {
  font-size: 12px;
  color: var(--gh-text-dim);
  padding: 0 4px 8px;
}
</style>
