<template>
  <div class="login-page">
    <div class="login-bg" aria-hidden="true">
      <i class="orb orb-1"></i>
      <i class="orb orb-2"></i>
      <i class="grid-lines"></i>
    </div>

    <div class="login-wrap">
      <a class="login-back" href="#" @click.prevent="goHome">← {{ t('login.backHome') }}</a>

      <div class="login-card">
        <div class="login-brand">
          <div class="login-logo">🔗</div>
          <div>
            <div class="login-title">{{ t('login.title') }}</div>
            <div class="login-sub">{{ siteName }}</div>
          </div>
        </div>

        <el-form :model="form" @submit.prevent>
          <el-form-item :label="t('login.username')">
            <el-input
              v-model="form.username"
              size="large"
              placeholder="admin"
              autocomplete="username"
              :prefix-icon="User"
            />
          </el-form-item>
          <el-form-item :label="t('login.password')">
            <el-input
              v-model="form.password"
              size="large"
              type="password"
              show-password
              placeholder="••••••••"
              autocomplete="current-password"
              :prefix-icon="Lock"
              @keyup.enter="submit"
            />
          </el-form-item>
          <el-form-item>
            <el-checkbox v-model="form.remember">{{ t('login.remember') }}</el-checkbox>
          </el-form-item>
        </el-form>

        <el-button
          type="primary"
          size="large"
          class="login-submit"
          :loading="loading"
          @click="submit"
        >
          {{ t('login.submit') }} →
        </el-button>

        <div class="login-hint">
          <span class="hint-dot"></span>{{ t('login.hint') }}
        </div>
      </div>

      <div class="login-foot">© {{ year }} {{ siteName }} · {{ appVersion }}</div>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { ElMessage } from 'element-plus'
import { User, Lock } from '@element-plus/icons-vue'
import api from '../api'
import { setAuth, isAuthed } from '../store'
import { basePath } from '../base'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const form = ref({ username: 'admin', password: '', remember: false })
const loading = ref(false)
const siteName = ref('GatewayHub')
const appVersion = ref(t('app.version'))
const year = new Date().getFullYear()

onMounted(async () => {
  // 已登录直接进控制台，不重复显示登录页
  if (isAuthed()) {
    router.replace({ path: '/dashboard' })
    return
  }
  try {
    const res = await api.getSettings()
    if (res.code === 0) {
      if (res.data.site_name) siteName.value = res.data.site_name
      if (res.data.version) appVersion.value = 'v' + res.data.version
    }
  } catch (e) {}
})

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
      // 支持 ?redirect=/dashboard，登录后回到原目标
      const redirect = route.query.redirect
      router.replace(typeof redirect === 'string' && redirect.startsWith('/') ? redirect : '/dashboard')
    } else {
      ElMessage.error(res.message || t('common.failed'))
    }
  } catch (e) {
    ElMessage.error(e?.response?.data?.message || t('common.failed'))
  } finally {
    loading.value = false
  }
}

function goHome() {
  window.location.assign(basePath)
}
</script>

<style scoped>
.login-page { position: relative; min-height: 100vh; display: grid; place-items: center; overflow: hidden; padding: 24px; }
.login-bg { position: absolute; inset: 0; pointer-events: none; }
.login-bg .orb { position: absolute; border-radius: 50%; filter: blur(70px); opacity: 0.45; animation: float 10s ease-in-out infinite; }
.orb-1 { width: 460px; height: 460px; left: -120px; top: -140px; background: radial-gradient(circle, rgba(var(--gh-primary-rgb), 0.55), transparent 70%); }
.orb-2 { width: 420px; height: 420px; right: -100px; bottom: -160px; background: radial-gradient(circle, rgba(59, 130, 246, 0.45), transparent 70%); animation-delay: -4s; }
.grid-lines {
  position: absolute; inset: 0;
  background-image:
    linear-gradient(rgba(var(--gh-primary-rgb), 0.05) 1px, transparent 1px),
    linear-gradient(90deg, rgba(var(--gh-primary-rgb), 0.05) 1px, transparent 1px);
  background-size: 44px 44px;
  mask-image: radial-gradient(ellipse 70% 60% at 50% 50%, #000 30%, transparent 75%);
  -webkit-mask-image: radial-gradient(ellipse 70% 60% at 50% 50%, #000 30%, transparent 75%);
}
@keyframes float { 0%, 100% { transform: translateY(0) scale(1); } 50% { transform: translateY(-16px) scale(1.05); } }

.login-wrap { position: relative; z-index: 1; width: 100%; max-width: 420px; }
.login-back {
  display: inline-block; margin-bottom: 14px; font-size: 13px;
  color: var(--gh-text-dim); text-decoration: none; transition: 0.2s;
}
.login-back:hover { color: var(--gh-primary); }

.login-card {
  padding: 30px 32px 24px;
  border: 1px solid var(--gh-border); border-radius: 18px;
  background: color-mix(in srgb, var(--gh-panel-solid) 92%, transparent);
  backdrop-filter: blur(16px);
  box-shadow: 0 20px 60px rgba(0, 0, 0, 0.18), var(--gh-glow);
}
.login-brand { display: flex; align-items: center; gap: 12px; margin-bottom: 26px; }
.login-logo {
  width: 44px; height: 44px; flex: none; display: grid; place-items: center;
  border-radius: 12px; font-size: 21px;
  background: linear-gradient(140deg, rgba(var(--gh-primary-rgb), 0.22), rgba(37, 99, 235, 0.16));
  border: 1px solid var(--gh-border-strong);
}
.login-title { font-size: 19px; font-weight: 800; letter-spacing: 0.3px; }
.login-sub { font-size: 11px; color: var(--gh-text-dim); text-transform: uppercase; letter-spacing: 1px; margin-top: 2px; }
.login-submit { width: 100%; margin-top: 4px; height: 44px; font-weight: 700; }
.login-hint {
  display: flex; align-items: center; gap: 7px; justify-content: center;
  margin-top: 18px; font-size: 11px; color: var(--gh-text-dim);
}
.hint-dot { width: 6px; height: 6px; border-radius: 50%; background: var(--gh-orange); }
.login-foot { margin-top: 18px; text-align: center; font-size: 11px; color: var(--gh-text-dim); }

@media (max-width: 480px) {
  .login-card { padding: 24px 20px 20px; }
}
</style>
