import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  plugins: [vue()],
  // base 用相对路径 './'：构建产物内所有资源引用（js/css/img/动态 chunk）均为相对路径，
  // 配合前端运行时基于 import.meta.url 推导部署根，可部署在任意子路径 /{name}/ 下
  // （自建反向代理 /{name}/ → 网关，前缀由反代决定，严禁硬编码）。
  base: './',
  server: {
    port: 5173,
    proxy: {
      '/api': {
        target: 'http://localhost:8088',
        changeOrigin: true
      }
    }
  },
  build: {
    outDir: 'dist',
    emptyOutDir: false,
    chunkSizeWarningLimit: 2000
  }
})
