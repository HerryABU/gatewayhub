<template>
  <div>
    <el-form label-width="100px">
      <el-form-item :label="t('settings.siteName')">
        <el-input v-model="form.site_name" />
      </el-form-item>
      <el-form-item :label="t('settings.language')">
        <el-radio-group v-model="form.language">
          <el-radio-button value="zh-CN">简体中文</el-radio-button>
          <el-radio-button value="en-US">English</el-radio-button>
        </el-radio-group>
      </el-form-item>
    </el-form>
    <div style="text-align:right">
      <el-button type="primary" :loading="saving" @click="save">{{ t('settings.save') }}</el-button>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessage } from 'element-plus'
import api from '../api'
import { setLocale } from '../i18n'

const emit = defineEmits(['done'])
const { t } = useI18n()
const form = ref({ site_name: 'GatewayHub', language: 'zh-CN' })
const saving = ref(false)

onMounted(async () => {
  try {
    const res = await api.getSettings()
    if (res.code === 0) {
      form.value.site_name = res.data.site_name || 'GatewayHub'
      form.value.language = res.data.language || 'zh-CN'
    }
  } catch (e) {}
})

async function save() {
  saving.value = true
  try {
    const res = await api.updateSettings(form.value)
    if (res.code === 0) {
      setLocale(form.value.language)
      ElMessage.success(t('common.success'))
      emit('done')
    } else {
      ElMessage.error(res.message || t('common.failed'))
    }
  } catch (e) {
    ElMessage.error(t('common.failed'))
  } finally {
    saving.value = false
  }
}
</script>
