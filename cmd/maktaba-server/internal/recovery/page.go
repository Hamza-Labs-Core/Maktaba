package recovery

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"html"
	"io"
	"net/http"
	"sort"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/pagetheme"
)

// pageCSS is the console's look on top of the server's own pages' theme (dark by default,
// light when the system is).
const pageCSS = `body{margin:0;padding:24px;max-width:960px;margin-inline:auto}
h1{font-size:1.5rem;margin:0 0 4px}h2{font-size:1.1rem;margin:24px 0 8px}
.intro{color:var(--mk-muted);margin:0 0 16px}
.strip{padding:10px 14px;border-radius:8px;background:#7c2d12;color:#fff;margin:12px 0}
.ok{color:#4ade80}.bad{color:#f87171}.warn{color:#fbbf24}.muted{color:var(--mk-muted)}
table{width:100%;border-collapse:collapse}td,th{padding:6px 8px;border-bottom:1px solid #27272a;text-align:start;vertical-align:top}
button{font:inherit;padding:6px 12px;border-radius:6px;border:1px solid #3f3f46;background:#18181b;color:inherit;cursor:pointer}
button:hover{background:#27272a}.actions{display:flex;flex-wrap:wrap;gap:8px;margin:8px 0}
pre{white-space:pre-wrap;word-break:break-word;font-size:.8rem;max-height:420px;overflow:auto;background:#111113;padding:8px;border-radius:6px}
input,select{font:inherit;padding:5px 8px;border-radius:6px;border:1px solid #3f3f46;background:#0f0f11;color:inherit}
#msg{min-height:1.2em;margin:8px 0}`

// pageJS renders the state and the logs, and sends the actions (with their confirms).
const pageJS = `(function(){
var W=JSON.parse(document.getElementById('words').textContent);
function t(k,v){var s=W[k]||k;for(var n in (v||{}))s=s.split('{'+n+'}').join(v[n]);return s}
var H={'X-Maktaba-Recovery':'1','Content-Type':'application/json'};
var minutes=15,lines=[];
function el(tag,txt,cls){var e=document.createElement(tag);if(txt!=null)e.textContent=txt;if(cls)e.className=cls;return e}
function size(n){if(n<0)return '?';var u=['B','KB','MB','GB','TB'],i=0;while(n>=1024&&i<4){n/=1024;i++}return n.toFixed(i?1:0)+' '+u[i]}
function cls(s){return s==='ok'||s==='off'?'ok':(s==='starting'||s==='degraded'?'warn':'bad')}
function say(s){document.getElementById('msg').textContent=s}
function act(body,need){
  if(!confirm(t('recovery.action.confirm')))return;
  body.confirm=1;
  if(need>1){if(!confirm(t('recovery.action.confirmAgain')))return;body.confirm=2}
  fetch('/recovery/api/action',{method:'POST',headers:H,body:JSON.stringify(body),credentials:'same-origin'})
   .then(function(r){return r.json().then(function(j){return {r:r,j:j}})})
   .then(function(x){
     if(x.r.status===428){body.confirm=0;return act(body,x.j.confirm)}
     say(x.r.ok?t('recovery.action.done'):t('recovery.action.failed',{reason:x.j.error||x.r.status}));load()})
   .catch(function(e){say(t('recovery.action.failed',{reason:String(e)}))})}
function load(){
  fetch('/recovery/api/state',{credentials:'same-origin'}).then(function(r){return r.json()}).then(function(s){
    var st=s.status||{},g=st.components||{};
    document.getElementById('safe').hidden=!s.safe_mode;
    document.getElementById('well').hidden=st.overall!=='ok';
    var gl=document.getElementById('groups');gl.textContent='';
    Object.keys(g).forEach(function(k){var li=el('li');li.appendChild(el('span',t('status.group.'+k)+': '));
      li.appendChild(el('strong',t('status.state.'+g[k]),cls(g[k])));gl.appendChild(li)});
    var tb=document.getElementById('comps');tb.textContent='';
    (s.components||[]).forEach(function(c){var tr=el('tr');
      tr.appendChild(el('td',c.id));tr.appendChild(el('td',t('status.state.'+c.state)+(c.reason?' ('+c.reason+')':''),cls(c.state)));
      var d=t('recovery.restarts',{n:c.restarts});if(c.last_exit)d+=' · '+t('recovery.lastExit',{at:c.last_exit,code:c.last_code});
      var td=el('td',d,'muted');if(c.last_error){td.appendChild(el('br'));td.appendChild(el('code',c.last_error,'bad'))}tr.appendChild(td);
      var b=el('td');if(c.can_restart){var bt=el('button',t('recovery.action.restart'));bt.onclick=function(){act({action:'restart',component:c.id},1)};b.appendChild(bt)}
      tr.appendChild(b);tb.appendChild(tr)});
    document.getElementById('disk').textContent=t('recovery.disk',{free:size(s.disk_free),logs:size(s.logs_bytes)});
    var a=document.getElementById('acts');a.textContent='';
    function btn(k,body,need,vars){var b=el('button',t(k,vars));b.onclick=function(){act(body,need)};a.appendChild(b)}
    btn('recovery.action.restartAll',{action:'restart_all'},1);
    if(s.safe_mode){btn('recovery.action.leaveSafe',{action:'leave_safe'},1);btn('safe.stay',{action:'stay_safe'},1)}
    else btn('recovery.action.safeMode',{action:'safe_mode'},1);
    if(s.rollback)btn('recovery.action.rollback',{action:'rollback'},2,{version:s.rollback});
    var r=el('button',t('recovery.action.restore'));r.onclick=restore;a.appendChild(r);
    var d=el('a',t('recovery.action.diagnostics'));d.href='/recovery/api/diagnostics';a.appendChild(el('span',' '));a.appendChild(d);
  }).catch(function(e){say(String(e))})}
function restore(){
  fetch('/recovery/api/backups',{credentials:'same-origin'}).then(function(r){return r.json()}).then(function(j){
    var l=j.backups||[];if(!l.length){say(t('recovery.backups.none'));return}
    var names=l.map(function(b){return b.name});var n=prompt(t('recovery.action.restore')+'\n'+names.join('\n'),names[0]);
    if(n)act({action:'restore',backup:n},2)})}
function logs(){
  fetch('/recovery/api/logs?minutes='+minutes,{credentials:'same-origin'}).then(function(r){return r.json()}).then(function(j){
    lines=(j.lines||[]);show()})}
function show(){var q=document.getElementById('q').value.toLowerCase();var p=document.getElementById('log');
  var out=lines.map(function(l){return JSON.stringify(l)}).filter(function(s){return !q||s.toLowerCase().indexOf(q)>=0});
  p.textContent=out.length?out.join('\n'):t('recovery.logs.none')}
document.getElementById('q').oninput=show;
document.getElementById('more').onclick=function(){minutes+=15;logs()};
load();logs();setInterval(load,10000);
})();`

var (
	pageCSP = "default-src 'none'; style-src '" + hash(pagetheme.CSS+pageCSS) + "'; script-src '" + hash(pageJS) +
		"'; connect-src 'self'; img-src 'self' data:; form-action 'self'; frame-ancestors 'none'; base-uri 'none'"
)

func hash(s string) string {
	h := sha256.Sum256([]byte(s))
	return "sha256-" + base64.StdEncoding.EncodeToString(h[:])
}

func head(lang, title string) string {
	dir := "ltr"
	if lang == "ar" {
		dir = "rtl"
	}
	return "<!doctype html><html lang=\"" + lang + "\" dir=\"" + dir + "\"><head><meta charset=\"utf-8\">" +
		"<meta name=\"viewport\" content=\"width=device-width,initial-scale=1\">" + pagetheme.MetaColorScheme +
		"<title>" + html.EscapeString(title) + "</title><style>" + pagetheme.CSS + pageCSS + "</style></head><body>"
}

func (h *Handler) page(w http.ResponseWriter, lang string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", pageCSP)
	words := map[string]string{}
	keys := make([]string, 0, len(Words))
	for k := range Words {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		words[k] = T(lang, k)
	}
	wj, _ := json.Marshal(words)
	e := html.EscapeString
	_, _ = io.WriteString(w, head(lang, T(lang, "recovery.title"))+
		"<h1>"+e(T(lang, "recovery.title"))+"</h1><p class=\"intro\">"+e(T(lang, "recovery.intro"))+"</p>"+
		"<div id=\"safe\" class=\"strip\" hidden>"+e(T(lang, "safe.strip"))+"</div>"+
		"<p id=\"well\" class=\"ok\" hidden>"+e(T(lang, "recovery.allWell"))+"</p>"+
		"<ul id=\"groups\"></ul><div id=\"acts\" class=\"actions\"></div><div id=\"msg\"></div>"+
		"<h2>"+e(T(lang, "recovery.components"))+"</h2><table><tbody id=\"comps\"></tbody></table>"+
		"<p id=\"disk\" class=\"muted\"></p>"+
		"<h2>"+e(T(lang, "recovery.logs.title"))+"</h2><div class=\"actions\"><input id=\"q\" placeholder=\""+
		e(T(lang, "recovery.logs.filter"))+"\"><button id=\"more\">"+e(T(lang, "recovery.logs.more"))+"</button></div>"+
		"<pre id=\"log\"></pre>"+
		// the words for the script, as JSON (no markup can end the element: < is escaped)
		"<script type=\"application/json\" id=\"words\">"+jsonForScript(wj)+"</script>"+
		"<script>"+pageJS+"</script></body></html>")
}

// jsonForScript keeps JSON inside a script element: "<" written as <.
func jsonForScript(b []byte) string {
	out := make([]byte, 0, len(b))
	for _, c := range b {
		if c == '<' {
			out = append(out, []byte(`<`)...)
			continue
		}
		out = append(out, c)
	}
	return string(out)
}

func signInPage(lang, msg string) string {
	e := html.EscapeString
	return head(lang, T(lang, "recovery.title")) + "<h1>" + e(T(lang, "recovery.title")) + "</h1><p>" + e(msg) +
		"</p></body></html>"
}
