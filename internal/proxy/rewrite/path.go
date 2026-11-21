package rewrite

import "strings"

func (rw *Rewriter) rewritePath(raw string) string {
	if raw == "" || raw[0] != '/' || strings.HasPrefix(raw, "//") {
		return raw // 绝对 URL / 相对路径 / 协议相对，不处理
	}
	// 已带前缀则跳过，避免重复改写（如 /prefix/api/x）
	if raw == rw.gatewayPath() || strings.HasPrefix(raw, rw.gatewayPath()+"/") {
		return raw
	}
	root := strings.TrimSuffix(rw.root, "/")
	if root == "" {
		// 后端在根目录：/x → /prefix/x
		if raw == "/" {
			return rw.gatewayPath() + "/"
		}
		return rw.gatewayPath() + raw
	}
	// 后端在子路径：/{root}/x → /prefix/x
	if raw == root {
		return rw.gatewayPath()
	}
	if strings.HasPrefix(raw, root+"/") {
		return rw.gatewayPath() + strings.TrimPrefix(raw, root)
	}
	// 非 root 开头的根路径（站点根资源，如 /assets、/static）：同样加网关前缀，避免 404
	return rw.gatewayPath() + raw
}

// rewriteRelative 把「以 / 开头」的根绝对路径改写为「文档相对」路径（"./x"），
// 配合注入的 <base href="/{prefix}/"> 由浏览器解析。
// 用于 HTML 属性、内联样式/脚本、JS 的 API 调用字符串（fetch/axios 按 document base 解析）。
func (rw *Rewriter) rewriteRelative(raw string) string {
	if raw == "" || raw[0] != '/' || strings.HasPrefix(raw, "//") {
		return raw // 绝对 URL / 相对路径 / 协议相对，不处理
	}
	// 已带网关前缀则跳过（后端可能自己生成了 /prefix/...）
	if raw == rw.gatewayPath() || strings.HasPrefix(raw, rw.gatewayPath()+"/") {
		return raw
	}
	root := strings.TrimSuffix(rw.root, "/")
	if root != "" {
		// 后端子路径：/{root}/x → ./x（base 之下直接命中）
		if raw == root {
			return "./"
		}
		if strings.HasPrefix(raw, root+"/") {
			return "." + strings.TrimPrefix(raw, root)
		}
	}
	return "." + raw
}

// relativeToFile 把根绝对路径改写为「相对 cssPath 所在目录」的相对路径（外链 CSS 用）。
// 目标网关路径 = /{prefix} + raw，相对 cssPath 目录计算，如 /prefix/fonts/x.woff ← /prefix/assets/index.css → ../fonts/x.woff。
func (rw *Rewriter) relativeToFile(raw, cssPath string) string {
	if raw == "" || raw[0] != '/' || strings.HasPrefix(raw, "//") {
		return raw
	}
	if raw == rw.gatewayPath() || strings.HasPrefix(raw, rw.gatewayPath()+"/") {
		return raw
	}
	return relPath(pathDir(cssPath), rw.gatewayPath()+raw)
}

// pathDir 返回 URL 风格路径的目录部分（/ 分隔）
func pathDir(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		if i == 0 {
			return "/"
		}
		return p[:i]
	}
	return "."
}

// relPath 计算从 baseDir 到 target 的 URL 相对路径（如 /app/assets → /app/fonts/x.woff → ../fonts/x.woff）
func relPath(baseDir, target string) string {
	base := strings.Split(baseDir, "/")
	tgt := strings.Split(target, "/")
	i := 0
	for i < len(base) && i < len(tgt) && base[i] == tgt[i] {
		i++
	}
	var parts []string
	for j := i; j < len(base); j++ {
		parts = append(parts, "..")
	}
	for j := i; j < len(tgt); j++ {
		parts = append(parts, tgt[j])
	}
	if len(parts) == 0 {
		return "./"
	}
	if parts[0] == ".." {
		return "../" + strings.Join(parts[1:], "/")
	}
	return "./" + strings.Join(parts, "/")
}

// modify 作为 httputil.ReverseProxy.ModifyResponse，重写响应头与响应体。
// 性能设计（透明代理 + 逼近源站速度）：
//   - 只对 HTML/CSS/JS 文本做内容重写，图片/字体/视频等二进制直接透传。
//   - 预检用「先粗后细」扫描（先找 '/' 再找前缀），避免对每个前缀做全量扫描。
//   - 无需重写时用原始字节 raw 原样透传（不重新压缩），所有正则包级预编译。
