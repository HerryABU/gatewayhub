<template>
  <div class="wizard-page">
    <div class="wizard-box gh-panel">
      <div class="wiz-header">
        <div class="gh-logo" style="width:48px;height:48px;font-size:26px">🔗</div>
        <div class="gh-brand-name" style="font-size:24px">{{ t('wizard.title') }}</div>
        <div class="gh-dim">{{ t('wizard.subtitle') }}</div>
      </div>

      <el-steps :active="step" align-center finish-status="success" style="margin:22px 0 26px">
        <el-step :title="t('wizard.stepWelcome')" />
        <el-step :title="t('wizard.stepAdmin')" />
        <el-step :title="t('wizard.stepDB')" />
        <el-step :title="t('wizard.stepDone')" />
      </el-steps>

      <!-- Step 0: 欢迎 + 站点 + 语言 -->
      <div v-if="step === 0" class="wiz-body">
        <el-form label-width="140px" label-position="left">
          <el-form-item :label="t('wizard.language')">
            <el-radio-group v-model="form.language">
              <el-radio-button value="zh-CN">简体中文</el-radio-button>
              <el-radio-button value="en-US">English</el-radio-button>
            </el-radio-group>
          </el-form-item>
          <el-form-item :label="t('wizard.siteName')">
            <el-input v-model="form.site_name" placeholder="GatewayHub" />
            <div class="gh-dim">{{ t('wizard.siteNameHint') }}</div>
          </el-form-item>
        </el-form>
      </div>

      <!-- Step 1: 管理员 -->
      <div v-else-if="step === 1" class="wiz-body">
        <el-form label-width="140px" label-position="left">
          <el-form-item :label="t('wizard.adminUsername')">
            <el-input v-model="form.admin_username" placeholder="admin" />
          </el-form-item>
          <el-form-item :label="t('wizard.adminPassword')">
            <el-input v-model="form.admin_password" type="password" show-password placeholder="••••••" />
          </el-form-item>
          <el-form-item :label="t('wizard.adminEmail')">
            <el-input v-model="form.admin_email" placeholder="admin@example.com" />
          </el-form-item>
        </el-form>
      </div>

      <!-- Step 2: 数据库 -->
      <div v-else-if="step === 2" class="wiz-body">
        <el-form label-width="160px" label-position="left">
          <el-form-item :label="t('wizard.dbDriver')">
            <el-radio-group v-model="dbMode">
              <el-radio value="keep">{{ t('wizard.dbKeep') }}</el-radio>
              <el-radio value="configure">{{ t('wizard.dbConfigure') }}</el-radio>
            </el-radio-group>
          </el-form-item>
          <template v-if="dbMode === 'configure'">
            <el-form-item :label="t('migration.driver')">
              <el-radio-group v-model="form.db.driver">
                <el-radio-button value="mysql">MySQL</el-radio-button>
                <el-radio-button value="sqlite">SQLite</el-radio-button>
              </el-radio-group>
            </el-form-item>
            <template v-if="form.db.driver === 'mysql'">
              <el-form-item :label="t('migration.host')"><el-input v-model="form.db.host" placeholder="127.0.0.1" /></el-form-item>
              <el-form-item :label="t('migration.port')"><el-input-number v-model="form.db.port" :min="1" :max="65535" /></el-form-item>
              <el-form-item :label="t('migration.user')"><el-input v-model="form.db.user" placeholder="root" /></el-form-item>
              <el-form-item :label="t('migration.password')"><el-input v-model="form.db.password" type="password" show-password /></el-form-item>
              <el-form-item :label="t('migration.database')"><el-input v-model="form.db.database" placeholder="gatewayhub" /></el-form-item>
            </template>
            <template v-else>
              <el-form-item :label="t('migration.path')"><el-input v-model="form.db.path" placeholder="gateway.db" /></el-form-item>
            </template>
          </template>
        </el-form>
        <div class="gh-tip info">{{ t('wizard.dbNote') }}</div>
      </div>

      <!-- Step 3: 完成 -->
      <div v-else class="wiz-body">
        <div class="done-box">
          <div class="done-icon">✅</div>
          <div class="gh-title" style="font-size:18px">{{ form.site_name || 'GatewayHub' }}</div>
          <div class="gh-dim">{{ form.admin_username }} · {{ form.language }}</div>
          <div class="gh-tip warn" style="margin-top:16px">{{ t('wizard.finishHint') }}</div>
        </div>
      </div>

      <div class="wiz-footer">
        <el-button v-if="step > 0" @click="step--">{{ t('wizard.prev') }}</el-button>
        <el-button v-if="step < 3" type="primary" @click="nextStep">{{ t('wizard.next') }}</el-button>
        <el-button v-else type="primary" :loading="submitting" @click="submit">{{ t('wizard.finish') }}</el-button>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, reactive } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessage } from 'element-plus'
import api from '../api'
import { setLocale } from '../i18n'

const { t } = useI18n()
const step = ref(0)
const dbMode = ref('keep')
const submitting = ref(false)
const form = reactive({
  site_name: 'GatewayHub',
  language: 'zh-CN',
  admin_username: 'admin',
  admin_password: '',
  admin_email: '',
  db: { driver: 'mysql', host: '127.0.0.1', port: 3306, user: 'root', password: '', database: 'gatewayhub', path: '' }
})

function nextStep() {
  if (step.value === 0 && !form.site_name) {
    ElMessage.warning(t('wizard.siteName'))
    return
  }
  if (step.value === 1 && (form.admin_username.length < 3 || form.admin_password.length < 6)) {
    ElMessage.warning(t('wizard.adminUsername') + ' / ' + t('wizard.adminPassword'))
    return
  }
  step.value++
}

async function submit() {
  submitting.value = true
  try {
    const payload = {
      site_name: form.site_name,
      language: form.language,
      admin_username: form.admin_username,
      admin_password: form.admin_password,
      admin_email: form.admin_email,
      db: dbMode.value === 'configure' ? form.db : {}
    }
    const res = await api.setupConfigure(payload)
    if (res.code === 0) {
      setLocale(form.language)
      ElMessage.success(t('common.success'))
      setTimeout(() => location.reload(), 800)
    } else {
      ElMessage.error(res.message || t('common.failed'))
    }
  } catch (e) {
    ElMessage.error(e?.response?.data?.message || t('common.failed'))
  } finally {
    submitting.value = false
  }
}
</script>

<style scoped>
.wizard-page {
  min-height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 24px;
}
.wizard-box {
  width: 100%;
  max-width: 600px;
  padding: 34px 36px;
}
.wiz-header { text-align: center; display: flex; flex-direction: column; align-items: center; gap: 6px; }
.wiz-body { min-height: 220px; }
.wiz-footer {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
  margin-top: 20px;
}
.done-box { text-align: center; padding: 20px 0; }
.done-icon { font-size: 48px; margin-bottom: 10px; }
</style>
