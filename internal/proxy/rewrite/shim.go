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
// 只碰请求，不碰数据。
const prefixShimTemplate = `(function(){
  var P=__P__;
  if(!P||P==="/")return;
  var PS=P+"/";
  window.__GATEWAY_PREFIX__=P;
  function fix(u){
    if(typeof u!=="string"||!u)return u;
    if(u.charCodeAt(0)!==47)return u;
    if(u.charCodeAt(1)===47)return u;
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

// shimScriptRe 用于判断页面里是否已经注入过（例如后端自己在 <head> 里回显了上游 HTML）。
var shimScriptRe = regexp.MustCompile(`data-gateway-prefix-shim`)

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
	return "<script data-gateway-prefix-shim=\"1\">" + js + "</script>"
}

// injectShim 把兜底脚本插到 <head> 之后（若已有则跳过）。
// 必须早于任何应用脚本执行，故与 <base> 一起注入在 head 开头。
func (rw *Rewriter) injectShim(s string) string {
	if shimScriptRe.MatchString(s) {
		return s
	}
	shim := rw.prefixShim()
	if shim == "" {
		return s
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
