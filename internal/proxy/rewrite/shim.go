package rewrite

import (
	"encoding/json"
	"regexp"
	"strings"
)

// prefixShimTemplate 是注入到后端 HTML <head> 最前面的「运行期前缀兜底」脚本。
// __P__ 会被替换成 JSON 字符串形式的网关前缀（如 "/go-nvs"）。
//
// 为什么必须要有它：
// 静态改写只能改「看起来像 URL 的字符串」，而 JS 里以 "/" 开头的字符串既可能是
// URL，也可能是**请求体数据**。典型反例（NVS 实测）：/api/batch 的请求体是
// {id, method, path} 数组，path 值形如 "/api/nav-layout"。旧实现把这类字面量
// 改成 "./api/nav-layout"，网关看着「更像相对路径」，后端却回
// "path 必须以 /api/ 开头" —— 11 个子请求全失败，顶部导航整条消失。
//
// 所以策略是分工：
//   - 静态改写只碰「资源类」字面量（/assets、/static、/uploads…），它们是真正的 URL；
//   - 所有真正发出去的请求（fetch / XHR / sendBeacon / EventSource / WebSocket）、
//     导航（pushState / replaceState）与 DOM 上的 src/href，统一在这里补前缀。
//
// 只碰请求，不碰数据。
// 刻意不碰 __dynamic_base__（vite-plugin-dynamic-base 约定，openlist / alist 在用）：
// 那是应用的**运行态**，不是可改写的外部配置。
//
// 曾经在这里做过「空值兜底」（留空就返回网关前缀），实测是错的：
// openlist 的 index.html 用 if(window.__dynamic_base__) 判断要不要进 CDN 分支——
// 空串时原样设置属性，非空时拼 __dynamic_base__ + item.attrs.src。
// 而 preloads 里有一条 script 只有 data-src、没有 src，被伪造成真值后拼出
// "/openlist" + undefined = /openlistundefined，DOM 上凭空多一个 404 脚本。
//
// 伪造应用内部状态＝篡改目标程序运行态，比改配置更隐蔽、更难排查。
// 正确做法：让它保持应用自己写的值（空就是空），补前缀交给
// 已钩住的 Element.setAttribute 与 HTMLScriptElement/Link/Image 的属性 setter。
const prefixShimTemplate = `(function(){
  var P=__P__;
  var O_AUTH=__O__;
  var WX_APPID=__A__;
  if(!P||P==="/")return;
  var PS=P+"/";
  window.__GATEWAY_PREFIX__=P;
  // 分层欺骗 + SDK 透传：wxLogin.js 从原 CDN 正常加载（透传），只在它把
  // window.WxLogin 赋值时拦截并包装构造参数 —— 把 appid 换成官方值、回调
  // 交给 fixOAuth 处理。DeepSeek 实测：反代地址是 IP，前端判为 dev 环境用
  // 测试 appid(wx58…) → 微信拒；换成官方 appid(wx932…) 后二维码正常。
  // 预设 setter：shim 在 <head> 最先执行，此时 SDK 尚未加载。
  if(WX_APPID){
    try{
      var _wx;
      Object.defineProperty(window,"WxLogin",{configurable:true,
        get:function(){return _wx},
        set:function(fn){
          if(typeof fn!=="function"){_wx=fn;return}
          _wx=function(opts){
            try{
              if(opts&&typeof opts==="object"){
                opts.appid=WX_APPID;
                if(typeof opts.redirect_uri==="string")opts.redirect_uri=fixOAuth(opts.redirect_uri);
              }
            }catch(e){}
            return fn.apply(this,arguments);
          };
        }});
    }catch(e){}
  }
  // 修 OAuth 回调地址：第三方登录 SDK（微信 wxLogin 等）把本站回调 URL
  // 嵌在「第三方域名 URL」的 redirect_uri 查询参数里。前端常用
  // location.origin + "/api/oauth/callback" 拼出 http://gw/api/...，
  // 漏掉网关前缀 → 微信回 "redirect_uri 参数错误"、回调打到网关根 404。
  // 只改 redirect_uri 这一个参数，且仅当它的 origin 与本站相同；
  // 其它查询参数（state / 业务数据）一律不碰，避免污染数据。
  function fixOAuth(u){
    try{
      var uu=new URL(u);
      if(uu.origin===location.origin)return u;
      var ru=uu.searchParams.get("redirect_uri");
      if(!ru)return u;
      var r;
      try{r=new URL(ru,location.href)}catch(e){return u}
      if(r.origin!==location.origin)return u;
      // 回源模式：把回调换成原站 origin，让微信/Apple 开放平台的
      // 域名白名单校验通过（登录流程在原站完成），路径保持后端原始路径。
      if(O_AUTH){
        uu.searchParams.set("redirect_uri",O_AUTH+r.pathname+(r.search||"")+(r.hash||""));
        return uu.toString();
      }
      var fixed=fix(r.pathname+(r.search||"")+(r.hash||""));
      if(fixed===r.pathname+(r.search||"")+(r.hash||""))return u;
      uu.searchParams.set("redirect_uri",location.origin+fixed);
      return uu.toString();
    }catch(e){return u}
  }
  function fix(u){
    if(typeof u!=="string"||!u)return u;
    // 第三方域名的完整 URL 原样放行，但要先修里面的 OAuth 回调参数。
    if(u.charCodeAt(0)!==47){
      try{
        var abs=new URL(u,location.href);
        if(abs.origin!==location.origin)return fixOAuth(u);
      }catch(e){}
    }
    // 同源绝对 URL 先剥回站点内路径。
    // vite 的 __vitePreload 用 new URL(dep, import.meta.url) 解析依赖，dep 是根相对
    // 的 "/assets/x.css"，new URL 会把它规约成 http://gw/assets/x.css —— 已经带上
    // origin，后面再加前缀就无从下手，请求直接落到网关根 → 全站 404。
    // 只剥 location.origin，跨域（CDN / 图床）一律原样放行。
    var O=location.origin;
    if(u.indexOf(O+"/")===0)u=u.slice(O.length);
    if(u.charCodeAt(0)!==47)return u;
    if(u.charCodeAt(1)===47)return u;
    // 两层改写叠在一起时收敛成一层（静态改写已加过前缀、运行期又拼一次），
    // 请求会落到 /prefix/prefix/assets/*。这里幂等收敛，不改最终语义。
    while(u.indexOf(P+P+"/")===0)u=u.slice(P.length);
    if(u===P||u.indexOf(PS)===0)return u;
    return P+u;
  }
  try{
    var of=window.fetch;
    if(of)window.fetch=function(input,init){
      try{
        if(typeof input==="string"){input=fix(input);}
        else if(input&&typeof input.url==="string"&&input.url.indexOf(location.origin+"/")===0){
          var p=input.url.slice(location.origin.length),fp=fix(p);
          if(fp!==p)input=new Request(location.origin+fp,input);
        }
      }catch(e){}
      return of.call(this,input,init);
    };
  }catch(e){}
  try{
    var xo=XMLHttpRequest.prototype.open;
    XMLHttpRequest.prototype.open=function(){
      var a=[].slice.call(arguments);
      if(a.length>1)a[1]=fix(a[1]);
      return xo.apply(this,a);
    };
  }catch(e){}
  try{
    if(navigator.sendBeacon){
      var sb=navigator.sendBeacon.bind(navigator);
      navigator.sendBeacon=function(u,d){return sb(fix(u),d);};
    }
  }catch(e){}
  try{
    var OE=window.EventSource;
    if(OE){var NE=function(u,c){return new OE(fix(u),c);};NE.prototype=OE.prototype;window.EventSource=NE;}
  }catch(e){}
  try{
    var OW=window.WebSocket;
    if(OW){var NW=function(u,p){return new OW(fix(u),p);};NW.prototype=OW.prototype;window.WebSocket=NW;}
  }catch(e){}
  try{
    var ph=history.pushState,rh=history.replaceState;
    history.pushState=function(s,t,u){return ph.call(this,s,t,fix(u));};
    history.replaceState=function(s,t,u){return rh.call(this,s,t,fix(u));};
  }catch(e){}
  try{
    var sa=Element.prototype.setAttribute;
    Element.prototype.setAttribute=function(n,v){
      if(typeof v==="string"&&(n==="src"||n==="href"||n==="poster"||n==="data-src"||n==="data-url"))v=fix(v);
      return sa.call(this,n,v);
    };
  }catch(e){}
  try{
    var hook=function(proto,name){
      if(!proto)return;
      var d=Object.getOwnPropertyDescriptor(proto,name);
      if(!d||!d.set)return;
      Object.defineProperty(proto,name,{configurable:true,enumerable:d.enumerable,get:d.get,set:function(v){d.set.call(this,fix(v));}});
    };
    hook(window.HTMLScriptElement&&HTMLScriptElement.prototype,"src");
    hook(window.HTMLImageElement&&HTMLImageElement.prototype,"src");
    hook(window.HTMLLinkElement&&HTMLLinkElement.prototype,"href");
    hook(window.HTMLSourceElement&&HTMLSourceElement.prototype,"src");
    hook(window.HTMLIFrameElement&&HTMLIFrameElement.prototype,"src");
    hook(window.HTMLMediaElement&&HTMLMediaElement.prototype,"src");
  }catch(e){}
})();`

// 判断"是否已注入 shim"必须匹配**完整的 script 标签**，不能只匹配字符串：
// 后端页面若在注释或 JS 字符串里提到 data-gateway-prefix-shim（很常见——前端自己
// 检测代理环境），旧写法会误判成"已注入"而完全跳过 shim，fetch 全部漏前缀。
// 直接用 shimBlockRe（下方定义），不再单独维护一个松散的正则。

// shimBlockRe 匹配完整的 shim script 块，跨系统嵌套时用它把内层注入的局部前缀
// 更新成外层知道的完整链：NVS(/gwh/) → GWh(/go-nvs/) 这类双向代理下，
// 若外层只因「已有 shim」就跳过，页面会留下 base=/gwh/ 与 P=/go-nvs 不一致，
// 相对资源被解析到 /gwh/assets 而不是 /gwh/go-nvs/assets，整站 404。
var shimBlockRe = regexp.MustCompile(`(?s)<script[^>]*data-gateway-prefix-shim[^>]*>.*?</script>`)

// prefixShim 生成带具体前缀的注入片段。
func (rw *Rewriter) prefixShim() string {
	p := rw.gatewayPath()
	if p == "/" || p == "" {
		return ""
	}
	enc, err := json.Marshal(p)
	if err != nil {
		return ""
	}
	js := strings.Replace(prefixShimTemplate, "__P__", string(enc), 1)
	// __O__ 是「SDK 回调回源」的原站 origin；未开启时注入空串（JS 里 falsy，走补前缀逻辑）。
	originJSON := []byte("null")
	if rw.origin != "" {
		if encO, err := json.Marshal(rw.origin); err == nil {
			originJSON = encO
		}
	}
	js = strings.Replace(js, "__O__", string(originJSON), 1)
	// __A__ 是微信登录欺骗用的官方 appid；空则注入 null（不 hook WxLogin）。
	appidJSON := []byte("null")
	if rw.wxAppid != "" {
		if encA, err := json.Marshal(rw.wxAppid); err == nil {
			appidJSON = encA
		}
	}
	js = strings.Replace(js, "__A__", string(appidJSON), 1)
	return "<script data-gateway-prefix-shim=\"1\">" + js + "</script>"
}

// injectShim 把兜底脚本插到 <head> 之后。
// 必须早于任何应用脚本执行，故与 <base> 一起注入在 head 开头。
// 已有 shim 时不再新增，但会更新为当前前缀（跨系统嵌套时内层只写了局部前缀）。
func (rw *Rewriter) injectShim(s string) string {
	// 先把内层注入的 shim 整块摘掉：它必须在 <head> 开头重新插入，
	// 否则内层 shim 若位于 body 尾部，外层原地替换后仍晚于应用脚本执行，
	// 应用启动时的 fetch 就已经漏掉前缀了。
	shim := rw.prefixShim()
	if shim == "" {
		return s
	}
	if shimBlockRe.MatchString(s) {
		// 前缀没变就原样返回：PrepareHTML 的幂等判定依赖「不额外制造命中」。
		if shimBlockRe.FindString(s) == shim {
			return s
		}
		s = shimBlockRe.ReplaceAllString(s, "")
	}
	if headTagRe.MatchString(s) {
		return headTagRe.ReplaceAllStringFunc(s, func(m string) string {
			return m + "\n" + shim
		})
	}
	return shim + "\n" + s
}

// InjectShim 是给「路径隔离引擎」复用的入口（strip + html_rewrite 时的响应体改写）。
//
// 隔离引擎自己的字面量改写只碰 HTML 属性与 CSS，绝不碰 JS 运行期路径——
// 这避免污染请求体数据，但也意味着 fetch('/api/x') / axios('/api') 会打到网关根路径。
// 因此 strip 模式下必须补这层运行期兜底，两条入口（数据库路径路由 / 隔离引擎）行为才一致。
// 返回是否真的注入（已注入过或前缀为空时返回 false）。
func InjectShim(prefix string, html []byte) ([]byte, bool) {
	p := "/" + strings.Trim(prefix, "/")
	rw := &Rewriter{prefix: strings.Trim(prefix, "/")}
	if p == "/" {
		return html, false
	}
	out := rw.injectShim(string(html))
	if out == string(html) {
		return html, false
	}
	return []byte(out), true
}
