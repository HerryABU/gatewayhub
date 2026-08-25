// 部署根（base path）动态推导 —— 兼容自建反向代理 /{name}/ 子路径部署。
//
// 原理：生产构建后本模块被打包进 /assets/index-xxx.js（Vite 默认 assetsDir=assets，
// 产物集中在部署根下一层）。import.meta.url 始终指向「模块脚本自身的绝对 URL」
// （与当前文档 URL 无关，即使从 /gw/login 深层路由刷新，脚本 URL 仍是 /gw/assets/...），
// 因此「脚本 URL 去掉文件名、再去掉 assets 目录」即恒等于部署根。
//
// 无需任何配置、无需后端注入、严禁硬编码 {name}；无前缀部署时自动退化为 '/'。
// 开发模式（vite dev）由 dev server 根路径 + /api 代理保证，恒为 '/'
export const basePath = import.meta.env.DEV
  ? '/'
  : (() => {
      const u = new URL(import.meta.url)
      const parts = u.pathname.split('/').filter(Boolean)
      parts.pop() // 脚本文件名
      if (parts.length && parts[parts.length - 1] === 'assets') {
        parts.pop() // Vite assetsDir（部署根的下一层）
      }
      let p = '/' + parts.join('/')
      if (!p.endsWith('/')) p += '/'
      return p
    })()

// 拼接部署根下的路径段（joinBase('api') → '/gw/api' 或 '/api'）
export function joinBase(seg) {
  return basePath + String(seg || '').replace(/^\/+/, '')
}
