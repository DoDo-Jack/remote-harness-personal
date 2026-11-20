package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"math/big"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

/* ───────────── 浏览器会话 ───────────── */

type session struct {
	UID    string
	Device string
	Exp    int64
}

func (a *App) issueSession(w http.ResponseWriter, uid, device string) {
	payload := base64.RawURLEncoding.EncodeToString([]byte(uid + "|" + device + "|" + time.Now().Add(30*24*time.Hour).Format(time.RFC3339)))
	tok := payload + "." + macHex([]byte(a.cfg.TokenSecret), payload)
	http.SetCookie(w, &http.Cookie{
		Name: "rh_sess", Value: tok, Path: "/", HttpOnly: true,
		Secure: true, SameSite: http.SameSiteLaxMode, MaxAge: 30 * 24 * 3600,
	})
}

func (a *App) readSession(r *http.Request) *session {
	c, err := r.Cookie("rh_sess")
	if err != nil {
		return nil
	}
	i := strings.LastIndex(c.Value, ".")
	if i <= 0 {
		return nil
	}
	payload, sig := c.Value[:i], c.Value[i+1:]
	if macHex([]byte(a.cfg.TokenSecret), payload) != sig {
		return nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return nil
	}
	parts := strings.Split(string(raw), "|")
	if len(parts) != 3 {
		return nil
	}
	exp, err := time.Parse(time.RFC3339, parts[2])
	if err != nil || time.Now().After(exp) {
		return nil
	}
	return &session{UID: parts[0], Device: parts[1], Exp: exp.Unix()}
}

/* ───────────── 配对码 ───────────── */

type pairCode struct {
	UID    string
	Device string
	Exp    time.Time
}

type pairStore struct {
	mu       sync.Mutex
	codes    map[string]pairCode
	attempts map[string]int
}

func newPairStore() *pairStore {
	return &pairStore{codes: map[string]pairCode{}, attempts: map[string]int{}}
}

func (p *pairStore) issue(uid, device string) string {
	code := leftPad(0, 6)
	for i := 0; i < 6; i++ {
		code = code[:i] + string(rune('0'+randInt(10))) + code[i+1:]
	}
	p.mu.Lock()
	p.codes[code] = pairCode{UID: uid, Device: device, Exp: time.Now().Add(10 * time.Minute)}
	p.mu.Unlock()
	return code
}

func (p *pairStore) take(code string) (pairCode, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	c, ok := p.codes[code]
	if !ok {
		p.attempts[code]++
		if p.attempts[code] > 50 {
			delete(p.attempts, code)
		}
		return pairCode{}, false
	}
	if time.Now().After(c.Exp) {
		delete(p.codes, code)
		return pairCode{}, false
	}
	delete(p.codes, code)
	delete(p.attempts, code)
	return c, true
}

/* ───────────── 登录页 ───────────── */

const loginHTML = `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1,viewport-fit=cover">
<meta name="color-scheme" content="light dark">
<script>try{document.documentElement.dataset.theme=localStorage.getItem('rh-theme')||'dark'}catch(e){}</script>
<title>远程 Harness · 登录</title>
<link rel="icon" href="/icon.png">
<link rel="apple-touch-icon" href="/icon.png">
<style>
:root{--bg:#080a0f;--card:#0f141d;--line:#1f2735;--text:#e9edf5;--muted:#8b95a8;--dim:#5b6474;--green:#35d69b}
*{box-sizing:border-box}
body{margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;
 background:radial-gradient(900px 520px at 15% -10%,#12233f 0%,transparent 55%),
            radial-gradient(800px 480px at 90% 0%,#1d1533 0%,transparent 50%),var(--bg);
 color:var(--text);font:16px/1.6 -apple-system,BlinkMacSystemFont,"PingFang SC",sans-serif;padding:24px}
.box{width:100%;max-width:380px;position:relative;z-index:1}
.brand{display:flex;align-items:center;gap:9px;margin-bottom:26px;font-weight:600;font-size:15px;letter-spacing:.02em}
.brand i{width:8px;height:8px;border-radius:50%;background:var(--green);box-shadow:0 0 0 4px rgba(53,214,155,.12)}
.card{background:linear-gradient(180deg,#121824,#0f141d);border:1px solid var(--line);border-radius:18px;padding:26px}
h1{font-size:21px;margin:0 0 4px;letter-spacing:-.01em}
p.sub{margin:0 0 22px;color:var(--muted);font-size:13px}
label{display:block;font-size:12px;color:var(--muted);margin:14px 0 6px}
input{width:100%;background:#0b1018;border:1px solid #232c3c;border-radius:11px;color:#fff;
 padding:12px 13px;font-size:15px;font-family:inherit;outline:none;transition:border-color .15s,box-shadow .15s}
input:focus{border-color:rgba(53,214,155,.65);box-shadow:0 0 0 3px rgba(53,214,155,.12)}
button{width:100%;margin-top:20px;padding:13px;border:0;border-radius:11px;background:var(--green);
 color:#052017;font-size:15px;font-weight:700;font-family:inherit;cursor:pointer;transition:filter .15s}
button:hover{filter:brightness(1.05)}
button:disabled{opacity:.6;cursor:default}
.msg{min-height:20px;margin-top:12px;font-size:12.5px;text-align:center;color:#ff8a8a}
.msg.ok{color:var(--green)}
.switch{margin-top:18px;text-align:center;font-size:13px;color:var(--dim)}
.switch a{color:#7fb0ff;text-decoration:none;cursor:pointer}
.hide{display:none}
.foot{margin-top:18px;text-align:center;font-size:11.5px;color:#3f4756}
html[data-theme="light"] body{background:#f5f6f8;color:#101418}
html[data-theme="light"] .card{background:#fff;border-color:#e3e7ee}
html[data-theme="light"] h1{color:#101418}
html[data-theme="light"] p.sub,html[data-theme="light"] label{color:#5b6472}
html[data-theme="light"] input{background:#fff;border-color:#d9dee7;color:#101418}
html[data-theme="light"] .brand{color:#101418}
html[data-theme="light"] .switch{color:#8a93a2}
html[data-theme="light"] .foot{color:#9aa3b2}
.aurora{position:fixed;inset:-18%;z-index:0;pointer-events:none;opacity:.85;
 background:
  radial-gradient(38% 42% at 22% 28%, rgba(53,214,155,.62), transparent 62%),
  radial-gradient(44% 46% at 80% 20%, rgba(56,132,255,.58), transparent 64%),
  radial-gradient(42% 40% at 68% 80%, rgba(150,92,255,.52), transparent 62%),
  radial-gradient(38% 40% at 26% 82%, rgba(34,211,238,.46), transparent 60%);
 filter:blur(70px) saturate(1.5);
 animation:breathe 7s ease-in-out infinite alternate, hue 18s linear infinite;}
@keyframes breathe{0%{opacity:.5;transform:scale(1)}100%{opacity:1;transform:scale(1.08)}}
@keyframes hue{0%{filter:blur(70px) saturate(1.5) hue-rotate(0deg)}100%{filter:blur(70px) saturate(1.5) hue-rotate(48deg)}}
@media (prefers-reduced-motion: reduce){.aurora{animation:none}}
.themebtn{position:fixed;top:16px;right:16px;background:transparent;border:1px solid #2a3548;color:#8b95a8;
 border-radius:9px;font-size:12px;padding:5px 10px;cursor:pointer;font-family:inherit}
html[data-theme="light"] .themebtn{border-color:#d9dee7;color:#5b6472}
</style></head><body>
<div class="aurora"></div>
<button class="themebtn" onclick="(function(){var e=document.documentElement;var n=e.dataset.theme==='light'?'dark':'light';e.dataset.theme=n;localStorage.setItem('rh-theme',n)})()">外观</button>
<div class="box">
  <div style="text-align:center;margin-bottom:22px">
    <img src="/icon.png" width="64" height="64" style="border-radius:16px" alt="">
    <div style="margin-top:12px;font-size:14px;font-weight:600;letter-spacing:.02em">远程 Harness</div>
  </div>
  <div class="card">
    <h1>登录</h1>
    <p class="sub">使用配置文件里设置的用户名和密码</p>

    <form id="form" autocomplete="on">
      <label>用户名</label>
      <input id="u" name="username" autocapitalize="none" autocorrect="off" spellcheck="false" placeholder="config.json 里的 user">
      <label>密码</label>
      <input id="p" name="password" type="password" placeholder="config.json 里的 pass">
      <button id="go" type="submit">登录</button>
    </form>

    <div class="msg" id="msg"></div>
  </div>
  <div class="foot">个人版 · 数据只在你自己的服务器上</div>
</div>
<script>
const $ = id => document.getElementById(id);
$('form').addEventListener('submit', async (e) => {
  e.preventDefault();
  const user = $('u').value.trim(), pass = $('p').value;
  const msg = $('msg');
  msg.className = 'msg'; msg.textContent = '';
  if (!user || !pass) { msg.textContent = '用户名和密码都要填'; return; }
  $('go').disabled = true;
  const r = await fetch('/_hub/api/login/password', {method:'POST',
    headers:{'content-type':'application/json'}, body: JSON.stringify({user, pass})});
  const j = await r.json().catch(() => ({}));
  $('go').disabled = false;
  if (r.ok) { msg.className = 'msg ok'; msg.textContent = '登录成功，正在进入…'; location.href = '/'; return; }
  msg.textContent = j.error || '登录失败';
});
</script>
</body></html>`

func (a *App) handleEntry(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" {
		a.handleHealth(w, r)
		return
	}
	if r.URL.Path == "/icon.png" || r.URL.Path == "/favicon.ico" || r.URL.Path == "/apple-touch-icon.png" {
		http.ServeFile(w, r, "/opt/dshhub/icon.png")
		return
	}
	if strings.HasPrefix(r.URL.Path, "/_hub/") {
		a.handleHubAPI(w, r)
		return
	}
	sess := a.readSession(r)
	if sess == nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(loginHTML))
		return
	}
	if sess.Device == "" {
		a.renderDevices(w, r, sess)
		return
	}
	if r.Header.Get("Upgrade") == "websocket" {
		a.proxyWS(w, r, sess)
		return
	}
	a.proxyHTTP(w, r, sess)
}

/* ───────────── 账号 API ───────────── */

func (a *App) handleHubAPI(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(r.URL.Path, "/_hub")
	r.URL.Path = p
	switch {
	case p == "/api/me":
		a.authedAny(a.handleMe)(w, r)
	case p == "/api/devices":
		a.authedAny(a.handleDevices)(w, r)
	case strings.HasPrefix(p, "/api/devices/"):
		a.authedAny(a.handleDeviceByID)(w, r)
	case p == "/api/pair/code":
		a.handlePairCode(w, r)
	case p == "/api/state":
		a.authedAny(a.handleState)(w, r)
	case p == "/devices":
		if sess := a.readSession(r); sess != nil {
			a.renderDevices(w, r, sess)
			return
		}
		http.Redirect(w, r, "/", 302)
	case p == "/wait":
		a.renderWait(w, r)
	case p == "/api/theme/bg":
		a.authedAny(a.handleThemeBG)(w, r)
	case p == "/api/theme/opacity":
		a.authedAny(a.handleThemeOpacity)(w, r)
	case p == "/api/theme/bg/delete":
		a.authedAny(a.handleThemeDelete)(w, r)
	case p == "/appearance":
		a.renderAppearance(w, r)
	case strings.HasPrefix(p, "/bg/"):
		a.serveBG(w, r)
	case p == "/api/takeover":
		a.handleTakeover(w, r)
	case p == "/api/login/password":
		a.handlePasswordLogin(w, r)
	case p == "/select":
		a.handleSelectDevice(w, r)
	case p == "/logout":
		a.handleLogout(w, r)
	case p == "/api/pair":
		a.handlePair(w, r)
	default:
		jsonErr(w, 404, "not found")
	}
}

func (a *App) handlePairCode(w http.ResponseWriter, r *http.Request) {
	uid, ok := verifyToken([]byte(a.cfg.TokenSecret), bearer(r))
	if !ok {
		jsonErr(w, 401, "unauthorized")
		return
	}
	var in struct {
		DeviceID string `json:"deviceId"`
	}
	_ = readJSON(r, &in)
	d := a.store.Device(in.DeviceID)
	if d == nil || d.UserID != uid {
		jsonErr(w, 404, "device not found")
		return
	}
	code := a.pairs.issue(uid, d.ID)
	jsonOut(w, 200, map[string]any{"ok": true, "code": code, "expiresIn": 600})
}

func (a *App) handlePair(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, 405, "method not allowed")
		return
	}
	var in struct {
		Code string `json:"code"`
	}
	if err := readJSON(r, &in); err != nil {
		jsonErr(w, 400, "bad body")
		return
	}
	if !a.limiter.Allow("pair:"+clientIP(r), time.Second, 300) {
		jsonErr(w, 429, "试得太快，等几秒再试")
		return
	}
	c, ok := a.pairs.take(strings.TrimSpace(in.Code))
	if !ok {
		jsonErr(w, 401, "配对码不对或已过期")
		return
	}
	a.issueSession(w, c.UID, c.Device)
	jsonOut(w, 200, map[string]any{"ok": true, "device": c.Device})
}

/* ───────────── 反向代理 ───────────── */

var hopHeaders = map[string]bool{
	"connection": true, "keep-alive": true, "proxy-authenticate": true,
	"proxy-authorization": true, "te": true, "trailer": true,
	"transfer-encoding": true, "upgrade": true, "content-length": true,
}

func (a *App) proxyHTTP(w http.ResponseWriter, r *http.Request, sess *session) {
	a.recordWebSession(r, sess.UID)
	body, _ := io.ReadAll(io.LimitReader(r.Body, 32<<20))
	if r.Body != nil {
		r.Body.Close()
	}
	headers := map[string]string{}
	for k, v := range r.Header {
		if hopHeaders[strings.ToLower(k)] || strings.EqualFold(k, "Host") {
			continue
		}
		headers[k] = strings.Join(v, ", ")
	}
	path := r.URL.Path
	if r.URL.RawQuery != "" {
		path += "?" + r.URL.RawQuery
	}
	id := "h_" + randomHex(8)
	frame, _ := json.Marshal(map[string]any{
		"t": "req", "id": id, "method": r.Method, "path": path,
		"headers": headers, "body": base64.StdEncoding.EncodeToString(body),
	})
	ch := make(chan []byte, 1)
	a.hub.wait(id, func(data []byte) {
		select {
		case ch <- data:
		default:
		}
	})
	defer a.hub.forget(id)
	if !a.hub.toAgent(sess.Device, frame) {
		if strings.Contains(r.Header.Get("Accept"), "text/html") {
			w.Header().Set("X-DSH-State", "offline")
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(200)
			_, _ = w.Write([]byte(statePage("offline")))
			return
		}
		jsonErr(w, 503, "电脑客户端不在线")
		return
	}
	select {
	case data := <-ch:
		var res struct {
			Status  int               `json:"status"`
			Headers map[string]string `json:"headers"`
			Body    string            `json:"body"`
			Code    string            `json:"code"`
		}
		if err := json.Unmarshal(data, &res); err != nil {
			jsonErr(w, 502, "bad response")
			return
		}
		// Harness自己的 401（token 过期/旧实例 cookie）也不要甩给手机
		if res.Status == 401 && strings.Contains(r.Header.Get("Accept"), "text/html") {
			w.Header().Set("X-DSH-State", "dsh_unreadable")
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(200)
			_, _ = w.Write([]byte(statePage("dsh_unreadable")))
			return
		}
		if res.Code != "" {
			if strings.Contains(r.Header.Get("Accept"), "text/html") {
				w.Header().Set("X-DSH-State", res.Code)
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.WriteHeader(200)
				_, _ = w.Write([]byte(statePage(res.Code)))
				return
			}
			jsonErr(w, 503, res.Code)
			return
		}
		for k, v := range res.Headers {
			if hopHeaders[strings.ToLower(k)] {
				continue
			}
			switch strings.ToLower(k) {
			case "location":
				v = rewriteLocation(v)
			case "set-cookie":
				v = rewriteCookie(v)
			}
			w.Header().Add(k, v)
		}
		w.Header().Set("X-DSH-State", "ok")
		if w.Header().Get("Content-Type") == "" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
		}
		w.WriteHeader(res.Status)
		if raw, err := base64.StdEncoding.DecodeString(res.Body); err == nil {
			_, _ = w.Write(raw)
		}
	case <-time.After(90 * time.Second):
		jsonErr(w, 504, "电脑端响应超时")
	}
}

func rewriteLocation(v string) string {
	if strings.Contains(v, "127.0.0.1") || strings.Contains(v, "localhost") {
		if i := strings.Index(v, "/"); i >= 0 {
			return v[i:]
		}
	}
	return v
}

func rewriteCookie(v string) string {
	parts := strings.Split(v, ";")
	out := []string{}
	// 去掉 Domain，Path 统一成 /
	for _, p := range parts {
		t := strings.TrimSpace(p)
		low := strings.ToLower(t)
		if strings.HasPrefix(low, "domain=") {
			continue
		}
		if strings.HasPrefix(low, "path=") {
			continue
		}
		out = append(out, t)
	}
	out = append(out, "Path=/")
	return strings.Join(out, "; ")
}

func (a *App) proxyWS(w http.ResponseWriter, r *http.Request, sess *session) {
	ws, err := upgradeWS(w, r)
	if err != nil {
		return
	}
	ws.StartPing(25 * time.Second)
	id := "w_" + randomHex(8)
	path := r.URL.Path
	if r.URL.RawQuery != "" {
		path += "?" + r.URL.RawQuery
	}
	a.hub.wait(id, func(data []byte) {
		var f struct {
			T    string `json:"t"`
			Data string `json:"data"`
			Kind string `json:"kind"`
		}
		if json.Unmarshal(data, &f) != nil {
			return
		}
		switch f.T {
		case "ws-msg":
			raw, err := base64.StdEncoding.DecodeString(f.Data)
			if err != nil {
				return
			}
			op := 0x1
			if f.Kind == "binary" {
				op = 0x2
			}
			_ = ws.WriteMessage(op, raw)
		case "ws-close", "ws-open-err":
			ws.Close()
		}
	})
	defer a.hub.forget(id)

	cookie := r.Header.Get("Cookie")
	if cookie != "" {
		cookie = strings.Join(filterOutSession(strings.Split(cookie, ";")), ";")
	}
	open, _ := json.Marshal(map[string]any{"t": "ws-open", "id": id, "path": path, "cookie": cookie})
	if !a.hub.toAgent(sess.Device, open) {
		_ = ws.WriteText([]byte(`{"t":"err","error":"电脑客户端不在线"}`))
		ws.Close()
		return
	}
	for {
		op, data, err := ws.ReadMessage(maxBody)
		if err != nil {
			closeFrame, _ := json.Marshal(map[string]any{"t": "ws-close", "id": id})
			a.hub.toAgent(sess.Device, closeFrame)
			return
		}
		kind := "binary"
		if op == 0x1 {
			kind = "text"
		}
		frame, _ := json.Marshal(map[string]any{"t": "ws-msg", "id": id, "kind": kind,
			"data": base64.StdEncoding.EncodeToString(data)})
		if !a.hub.toAgent(sess.Device, frame) {
			return
		}
	}
}

func macHex(secret []byte, payload string) string {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(payload))
	return hex.EncodeToString(m.Sum(nil))
}

func randInt(n int) int {
	v, _ := rand.Int(rand.Reader, big.NewInt(int64(n)))
	return int(v.Int64())
}

/* ───────────── 用户名密码登录 ───────────── */

func (a *App) handlePasswordLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, 405, "method not allowed")
		return
	}
	var in struct {
		User string `json:"user"`
		Pass string `json:"pass"`
	}
	if err := readJSON(r, &in); err != nil {
		jsonErr(w, 400, "bad body")
		return
	}
	if !a.limiter.Allow("login:"+clientIP(r), time.Second, 30) {
		jsonErr(w, 429, "试得太快，等几秒")
		return
	}
	in.User = strings.TrimSpace(in.User)
	if in.User != a.cfg.User || in.Pass != a.cfg.Pass {
		jsonErr(w, 401, "用户名或密码不对（在 config.json 里修改）")
		return
	}
	uid := "u_local"
	if u := a.store.User(uid); u == nil {
		a.store.CreateUserNamed("本地用户")
		for id := range a.store.Users() {
			uid = id
		}
	}
	a.issueSession(w, uid, "")
	jsonOut(w, 200, map[string]any{
		"ok": true, "user": map[string]any{"id": uid, "name": a.cfg.User},
		"token": signToken([]byte(a.cfg.TokenSecret), uid, 365*24*time.Hour),
	})
}

const serverVersion = "1.0.0"

const uiCSS = `
:root{--bg:#080a0f;--card:#0f141d;--card2:#121826;--line:#1f2735;--text:#e9edf5;--muted:#8b95a8;--dim:#5b6474;--green:#35d69b}
*{box-sizing:border-box}
body{margin:0;min-height:100vh;background:radial-gradient(900px 520px at 12% -12%,#12233f 0%,transparent 55%),
 radial-gradient(760px 460px at 92% -6%,#1d1533 0%,transparent 50%),var(--bg);
 color:var(--text);font:16px/1.6 -apple-system,BlinkMacSystemFont,"PingFang SC",sans-serif}
.wrap{max-width:460px;margin:0 auto;padding:26px 20px 48px}
header{display:flex;align-items:center;justify-content:space-between;margin-bottom:22px}
.brand{display:flex;align-items:center;gap:8px;font-size:13.5px;font-weight:600;letter-spacing:.02em}
.brand i{width:7px;height:7px;border-radius:50%;background:var(--green);box-shadow:0 0 0 3px rgba(53,214,155,.12)}
.exit{color:var(--dim);font-size:12.5px;text-decoration:none}
h1{font-size:22px;margin:0 0 2px;letter-spacing:-.01em}
.sub{color:var(--muted);font-size:12.5px;margin:0 0 20px}
.card{background:linear-gradient(180deg,var(--card2),var(--card));border:1px solid var(--line);border-radius:16px;padding:16px}
.section{font-size:10.5px;letter-spacing:.16em;color:var(--dim);text-transform:uppercase;margin:26px 0 10px;font-weight:600}
.device{display:flex;align-items:center;gap:13px;background:var(--card);border:1px solid var(--line);
 border-radius:14px;padding:13px 15px;margin-bottom:9px;text-decoration:none;color:inherit;transition:border-color .15s}
.device:hover{border-color:#33405a}
.ico{font-size:17px;width:20px;text-align:center;opacity:.9}
.info{flex:1;min-width:0}
.name{font-size:14.5px;font-weight:600;letter-spacing:-.01em}
.meta{font-size:11px;color:var(--dim);margin-top:2px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.pill{font-size:10.5px;padding:2px 9px;border-radius:99px;border:1px solid transparent;white-space:nowrap}
.dot{width:9px;height:9px;border-radius:50%;display:inline-block;flex:0 0 auto}
.dot.on{background:#35d69b;box-shadow:0 0 0 3px rgba(53,214,155,.15)}
.dot.off{background:#0b0d11;border:1px solid #2a3342}
.dtxt{font-size:11px;color:var(--dim);margin-left:6px}
.btn{display:block;width:100%;padding:12px;border:0;border-radius:11px;background:var(--green);color:#052017;
 font-size:14.5px;font-weight:700;font-family:inherit;cursor:pointer;text-align:center;text-decoration:none}
.btn.ghost{background:transparent;border:1px solid #2a3548;color:#cfd7e5;font-weight:600}
input{width:100%;background:#0b1018;border:1px solid #232c3c;border-radius:11px;color:#fff;
 padding:12px 13px;font-size:15px;font-family:inherit;outline:none;letter-spacing:.05em}
input:focus{border-color:rgba(53,214,155,.6);box-shadow:0 0 0 3px rgba(53,214,155,.1)}
.msg{min-height:18px;font-size:12.5px;text-align:center;margin-top:10px;color:#ff8a8a}
.msg.ok{color:var(--green)}
html[data-theme="light"]{--bg:#f5f6f8;--card:#ffffff;--card2:#ffffff;--line:#e3e7ee;--text:#101418;--muted:#5b6472;--dim:#8a93a2;--green:#12a26e}
html[data-theme="light"] body{background:radial-gradient(900px 520px at 12% -12%,#e8f1ff 0%,transparent 55%),
 radial-gradient(760px 460px at 92% -6%,#f3ecff 0%,transparent 50%),var(--bg)}
.aurora{position:fixed;inset:-18%;z-index:0;pointer-events:none;opacity:.85;
 background:
  radial-gradient(38% 42% at 22% 28%, rgba(53,214,155,.62), transparent 62%),
  radial-gradient(44% 46% at 80% 20%, rgba(56,132,255,.58), transparent 64%),
  radial-gradient(42% 40% at 68% 80%, rgba(150,92,255,.52), transparent 62%),
  radial-gradient(38% 40% at 26% 82%, rgba(34,211,238,.46), transparent 60%);
 filter:blur(70px) saturate(1.5);
 animation:breathe 7s ease-in-out infinite alternate, hue 18s linear infinite;}
@keyframes breathe{0%{opacity:.5;transform:scale(1) }100%{opacity:1;transform:scale(1.10)}}
@keyframes hue{0%{filter:blur(70px) saturate(1.5) hue-rotate(0deg)}100%{filter:blur(70px) saturate(1.5) hue-rotate(48deg)}}
@media (prefers-reduced-motion: reduce){.aurora{animation:none}}
.bg{position:fixed;inset:0;background-size:cover;background-position:center;pointer-events:none;z-index:0;
 filter:saturate(0); transition:opacity .12s linear}
.wrap{position:relative;z-index:1}
*{transition:background-color .12s linear,color .12s linear,border-color .12s linear}
.theme{background:transparent;border:1px solid var(--line);color:var(--muted);border-radius:9px;
 font-size:12px;padding:4px 10px;cursor:pointer;font-family:inherit}
`

func (a *App) renderDevices(w http.ResponseWriter, r *http.Request, sess *session) {
	u := a.store.User(sess.UID)
	if u == nil {
		http.SetCookie(w, &http.Cookie{Name: "rh_sess", Value: "", Path: "/", MaxAge: -1})
		http.Redirect(w, r, "/", 302)
		return
	}
	var rows strings.Builder
	for _, d := range a.store.DevicesOf(u.ID) {
		online := a.hub.agentOf(d.ID) != nil
		if d.Kind == "web" {
			online = time.Since(d.LastSeen) < 2*time.Minute
		}
		ago := ""
		if !d.LastSeen.IsZero() {
			mins := int(time.Since(d.LastSeen).Minutes())
			switch {
			case mins < 1:
				ago = "刚刚"
			case mins < 60:
				ago = itoa(mins) + " 分钟前"
			default:
				ago = itoa(mins/60) + " 小时前"
			}
		}
		pill := `<span class="dot off"></span><span class="dtxt">离线` + func() string {
			if ago != "" {
				return " · " + ago
			}
			return ""
		}() + `</span>`
		if online {
			pill = `<span class="dot on"></span><span class="dtxt">在线</span>`
		}
		meta := strings.TrimSpace(strings.Join([]string{d.Model, d.Chip, d.OSVersion}, " · "))
		if meta == "" {
			meta = d.OS
		}
		if d.Kind == "web" {
			// 网页登录记录：只展示，不能当电脑连
			when := ""
			if d.LastSeen.Year() > 1 {
				when = " · 最后在线 " + d.LastSeen.Local().Format("01-02 15:04")
			}
			rows.WriteString(`<div class="device" style="cursor:default">` +
				`<span class="ico">📱</span>` +
				`<span class="info"><span class="name">` + htmlEscape(d.Name) + `</span>` +
				`<span class="meta">网页登录` + htmlEscape(when) + `</span></span>` +
				`<span style="display:flex;align-items:center">` + pill + `</span></div>`)
			continue
		}
		rows.WriteString(`<a class="device" href="/_hub/select?device=` + d.ID + `">` +
			`<span class="ico">🖥</span>` +
			`<span class="info"><span class="name">` + htmlEscape(d.Name) + `</span>` +
			`<span class="meta">` + htmlEscape(meta) + `</span></span>` +
			`<span style="display:flex;align-items:center">` + pill + `</span></a>`)
	}
	if rows.Len() == 0 {
		rows.WriteString(`<div class="card" style="text-align:center;color:var(--muted);font-size:13px">` +
			`还没有电脑<br><span style="font-size:12px;color:var(--dim)">在电脑上打开客户端，用同一个账号登录</span></div>`)
	}

days, _, active := a.store.MemberDaysLeft(u.ID)
	memberCard := ""
	if active {
		until := ""
		if _, t, ok := a.store.MemberDaysLeft(u.ID); ok {
			until = t.Format("2006-01-02")
		}
		memberCard = `<a class="card" href="/_hub/member" style="display:block;text-decoration:none;color:inherit;border-color:rgba(53,214,155,.35)">` +
			`<div style="display:flex;justify-content:space-between;align-items:center">` +
			`<span style="font-size:12px;color:var(--muted)">👑 会员</span>` +
			`<span style="font-size:11.5px;color:#7fb0ff">续费 ›</span></div>` +
			`<div style="margin-top:8px"><span style="font:600 30px/1.1 -apple-system">` + itoa(days) + `</span>` +
			`<span style="font-size:13px;color:var(--muted);margin-left:4px">天</span></div>` +
			`<div style="font-size:11.5px;color:var(--dim);margin-top:3px">` + until + ` 到期</div></a>`
	} else {
		memberCard = `<a class="card" href="/_hub/member" style="display:block;text-decoration:none;color:inherit">` +
			`<div style="display:flex;justify-content:space-between;align-items:center">` +
			`<span style="font-size:13px;color:var(--muted)">👑 开通会员</span>` +
			`<span style="font-size:11.5px;color:#7fb0ff">¥6/月起 ›</span></div></a>`
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(`<!doctype html><html lang="zh-CN"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1,viewport-fit=cover">
<meta name="color-scheme" content="light dark"><title>我的设备 · 远程 Harness</title><script>try{document.documentElement.dataset.theme=localStorage.getItem('rh-theme')||'dark'}catch(e){}</script><style>` + uiCSS + `</style></head><body>
<div class="wrap">
  <header><div class="brand"><img src="/icon.png" width="20" height="20" style="border-radius:5px" alt="">远程 Harness</div>
  <div style="display:flex;gap:10px;align-items:center"><button class="theme" onclick="toggleTheme()">明暗</button><a class="exit" href="/_hub/appearance">外观</a><a class="exit" href="/_hub/logout">退出</a></div></header>
  <h1>` + htmlEscape(u.Name) + `</h1>
  <p class="sub">已登录 · 选择一台电脑开始</p>
  ` + memberCard + `
  <div class="section">我的设备</div>
  ` + rows.String() + `
  <div style="margin-top:26px;text-align:center;font-size:11px;color:var(--dim)">远程 Harness 服务端 v` + serverVersion + `</div>
</div>
<script>
(function(){
  var t = localStorage.getItem('rh-theme') || 'dark';
  document.documentElement.dataset.theme = t;
})();
function toggleTheme(){
  var el = document.documentElement;
  var next = el.dataset.theme === 'light' ? 'dark' : 'light';
  el.dataset.theme = next; localStorage.setItem('rh-theme', next);
}
function toggleTheme(){
  var el = document.documentElement;
  var next = el.dataset.theme === 'light' ? 'dark' : 'light';
  el.dataset.theme = next;
  localStorage.setItem('rh-theme', next);
}
</script></body></html>`))
}

func (a *App) handleSelectDevice(w http.ResponseWriter, r *http.Request) {
	sess := a.readSession(r)
	if sess == nil {
		http.Redirect(w, r, "/", 302)
		return
	}
	id := r.URL.Query().Get("device")
	d := a.store.Device(id)
	if d == nil || d.UserID != sess.UID {
		http.Redirect(w, r, "/", 302)
		return
	}
	a.issueSession(w, sess.UID, d.ID)
	http.Redirect(w, r, "/", 302)
}

func (a *App) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: "rh_sess", Value: "", Path: "/", MaxAge: -1})
	http.Redirect(w, r, "/", 302)
}

func htmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}

/* ───────────── 手机端接管 ───────────── */

func (a *App) handleTakeover(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, 405, "method not allowed")
		return
	}
	sess := a.readSession(r)
	if sess == nil || sess.Device == "" {
		jsonErr(w, 401, "请先登录并选择电脑")
		return
	}
	id := "t_" + randomHex(8)
	ch := make(chan []byte, 1)
	a.hub.wait(id, func(data []byte) {
		select {
		case ch <- data:
		default:
		}
	})
	defer a.hub.forget(id)
	frame, _ := json.Marshal(map[string]any{"t": "takeover", "id": id})
	if !a.hub.toAgent(sess.Device, frame) {
		if strings.Contains(r.Header.Get("Accept"), "text/html") {
			w.Header().Set("X-DSH-State", "offline")
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(200)
			_, _ = w.Write([]byte(statePage("offline")))
			return
		}
		jsonErr(w, 503, "电脑客户端不在线")
		return
	}
	select {
	case data := <-ch:
		var res struct {
			T string `json:"t"`
		}
		json.Unmarshal(data, &res)
		if res.T == "takeover-ok" {
			jsonOut(w, 200, map[string]any{"ok": true})
		} else {
			jsonErr(w, 500, "电脑上启动失败，请手动打开 Harness")
		}
	case <-time.After(45 * time.Second):
		jsonErr(w, 504, "电脑没有响应")
	}
}

func filterOutSession(parts []string) []string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		t := strings.TrimSpace(p)
		if strings.HasPrefix(t, "rh_sess=") {
			continue
		}
		out = append(out, t)
	}
	return out
}

/* ───────────── 会员 ───────────── */

const (
	priceMonth = 6
	priceYear  = 60
	daysMonth  = 30
	daysYear   = 365
)

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}

func (a *App) bgLayer(uid string) string {
	out := `<div class="aurora"></div>`
	if !a.hasBG(uid) {
		return out
	}
	op := 35
	if u := a.store.User(uid); u != nil && u.BGOpacity > 0 {
		op = u.BGOpacity
	}
	out += `<div class="bg" style="background-image:url('/_hub/bg/` + uid + `.png');opacity:` + itoa(op) + `%"></div>`
	return out
}

func (a *App) renderAppearance(w http.ResponseWriter, r *http.Request) {
	sess := a.readSession(r)
	if sess == nil {
		http.Redirect(w, r, "/", 302)
		return
	}
	u := a.store.User(sess.UID)
	if u == nil {
		http.Redirect(w, r, "/", 302)
		return
	}
	op := 35
	if u.BGOpacity > 0 {
		op = u.BGOpacity
	}
	has := a.hasBG(sess.UID)
	preview := ""
	if has {
		preview = `<div style="border-radius:12px;overflow:hidden;border:1px solid var(--line);margin-bottom:12px">
		  <img src="/_hub/bg/` + sess.UID + `.png" style="display:block;width:100%;opacity:` + itoa(op) + `%"></div>`
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(`<!doctype html><html lang="zh-CN"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1,viewport-fit=cover">
<meta name="color-scheme" content="light dark">
<script>try{document.documentElement.dataset.theme=localStorage.getItem('rh-theme')||'dark'}catch(e){}</script>
<title>外观 · 远程 Harness</title><style>` + uiCSS + `
.preview{width:100%;border-radius:12px;border:1px solid var(--line);margin-bottom:10px;display:block}
.row{display:flex;align-items:center;gap:10px;font-size:12.5px;color:var(--muted);margin:10px 0}
input[type=range]{flex:1;accent-color:var(--green)}
</style></head><body>
<div class="wrap">
  ` + a.bgLayer(sess.UID) + `
  <header><div class="brand"><img src="/icon.png" width="20" height="20" style="border-radius:5px" alt="">远程 Harness</div><a class="exit" href="/">返回</a></header>
  <h1>外观</h1>
  <p class="sub">背景图会被转成单色点阵，只保留轮廓，用主题色画点</p>
  ` + preview + `
  <div class="section">背景图</div>
  <div class="card">
    <input type="file" id="f" accept="image/*" style="font-size:13px">
    <div class="row"><span>浓淡</span><input type="range" id="op" min="0" max="100" value="` + itoa(op) + `"><span id="opv">` + itoa(op) + `%</span></div>
    <div class="msg" id="m"></div>
  </div>
  <div class="section">操作</div>
  <div class="card">
    <button class="btn ghost" onclick="del()">移除背景图</button>
  </div>
</div>
<script>
const f=document.getElementById('f'), op=document.getElementById('op'), opv=document.getElementById('opv'), m=document.getElementById('m');
f.addEventListener('change', async ()=>{
  const file=f.files[0]; if(!file) return;
  m.className='msg'; m.textContent='正在处理…';
  const fd=new FormData(); fd.append('file',file); fd.append('opacity', op.value);
  const r=await fetch('/_hub/api/theme/bg',{method:'POST',body:fd});
  const j=await r.json().catch(()=>({}));
  if(r.ok){ m.className='msg ok'; m.textContent='已应用'; setTimeout(()=>location.reload(),600); }
  else { m.textContent=j.error||'处理失败'; }
});
let t=null;
op.addEventListener('input', ()=>{ opv.textContent=op.value+'%';
  clearTimeout(t); t=setTimeout(async()=>{ await fetch('/_hub/api/theme/opacity',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({opacity:+op.value})}); },300);
});
async function del(){ await fetch('/_hub/api/theme/bg/delete',{method:'POST'}); location.reload(); }
</script></body></html>`))
}


// 把每一次网页登录记成一条“登录记录”（kind=web），掉线后仍保留
func (a *App) recordWebSession(r *http.Request, uid string) {
	ua := r.Header.Get("User-Agent")
	name := "网页"
	switch {
	case strings.Contains(ua, "iPhone"):
		name = "iPhone 网页"
	case strings.Contains(ua, "iPad"):
		name = "iPad 网页"
	case strings.Contains(ua, "Android"):
		name = "Android 网页"
	case strings.Contains(ua, "Macintosh"):
		name = "Mac 网页"
	case strings.Contains(ua, "Windows"):
		name = "Windows 网页"
	}
	a.store.EnsureWebDevice(uid, name)
}

func (a *App) handleState(w http.ResponseWriter, r *http.Request) {
	dev := ""
	if sess := a.readSession(r); sess != nil {
		dev = sess.Device
	} else if uid, ok := verifyToken([]byte(a.cfg.TokenSecret), bearer(r)); ok {
		if id := r.URL.Query().Get("device"); id != "" {
			if d := a.store.Device(id); d != nil && d.UserID == uid {
				dev = id
			}
		}
	}
	if dev == "" {
		jsonErr(w, 401, "unauthorized")
		return
	}
	jsonOut(w, 200, map[string]any{
		"ok":           true,
		"agent_online": a.hub.agentOf(dev) != nil,
		"dsh_ready":    a.hub.isReady(dev),
	})
}

// 手机端看到的"还没连上"页面：不是 JSON，是一张正经的状态页
func statePage(code string) string {
	title, desc, action := "", "", ""
	switch code {
	case "offline":
		title = "电脑客户端没有运行"
		desc = "那台电脑上的「远程 Harness」没开，或者退出了。打开它之后这里会自动继续。"
		action = "retry"
	case "dsh_not_running":
		title = "Harness没有运行"
		desc = "电脑在线，但上面的Harness没开。点下面的按钮，让电脑把它启动起来。"
		action = "takeover"
	case "dsh_unreadable":
		title = "Harness在运行，但读不到入口"
		desc = "电脑上的Harness已经开着，但它这次启动没有交出链接。点接管会重启一次并接管。"
		action = "takeover"
	default:
		title = "暂时连不上"
		desc = "稍等一下再试。"
		action = "retry"
	}
	btn := `<button class="btn" id="act" onclick="retry()">重新检查</button>`
	if action == "takeover" {
		btn = `<button class="btn" id="act" onclick="takeover()">接管电脑上的Harness</button>`
	}
	return `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1,viewport-fit=cover">
<meta name="color-scheme" content="light dark">
<script>try{document.documentElement.dataset.theme=localStorage.getItem('rh-theme')||'dark'}catch(e){}</script>
<title>` + title + `</title><style>` + uiCSS + `
.center{min-height:78vh;display:flex;flex-direction:column;align-items:center;justify-content:center;text-align:center}
.big{width:76px;height:76px;border-radius:22px;margin-bottom:18px}
h1{font-size:19px;margin:0 0 8px}
p.d{color:var(--muted);font-size:13.5px;max-width:300px;margin:0 0 22px;line-height:1.65}
.steps{margin-top:18px;font-size:12px;color:var(--dim);max-width:300px;text-align:left}
.steps div{margin:6px 0}
#act{width:auto;min-width:240px;padding:13px 22px;font-size:14.5px}
</style></head><body>
<div class="wrap"><div class="center">
  <img class="big" src="/icon.png" alt="">
  <h1>` + title + `</h1>
  <p class="d">` + desc + `</p>
  ` + btn + `
  <a href="/_hub/devices" style="margin-top:18px;font-size:12.5px;color:var(--muted);text-decoration:none">回到设备列表</a>
  <div class="steps" id="steps"></div>
</div></div>
<script>
function retry(){ location.reload(); }
// 每 3 秒自动检查一次，好了自己进去
async function watch(){
  try{
    const r=await fetch('/_hub/api/state',{headers:{'accept':'application/json'}});
    const j=await r.json();
    if(j.dsh_ready && j.agent_online){ location.replace('/'); return; }
    const st=document.getElementById('steps');
    if(st) st.textContent = j.agent_online ? '电脑在线，Harness还没就绪' : '电脑客户端不在线';
  }catch(e){}
  setTimeout(watch,3000);
}
watch();
async function takeover(){
  const b=document.querySelector('button'); b.disabled=true; b.textContent='正在让电脑启动…';
  const r=await fetch('/_hub/api/takeover',{method:'POST'});
  if(r.ok){ location.href='/_hub/wait'; return; }
  const j=await r.json().catch(()=>({}));
  b.disabled=false; b.textContent='接管电脑上的Harness';
  document.getElementById('steps').textContent = j.error || '接管失败，请在电脑上手动打开Harness';
}
</script></body></html>`
}

// 等待页：轮询状态，好了自动跳转
func (a *App) renderWait(w http.ResponseWriter, r *http.Request) {
	sess := a.readSession(r)
	if sess == nil {
		http.Redirect(w, r, "/", 302)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(`<!doctype html><html lang="zh-CN"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1,viewport-fit=cover">
<meta name="color-scheme" content="light dark">
<script>try{document.documentElement.dataset.theme=localStorage.getItem('rh-theme')||'dark'}catch(e){}</script>
<title>正在连接 · 远程 Harness</title><style>` + uiCSS + `
.center{min-height:78vh;display:flex;flex-direction:column;align-items:center;justify-content:center;text-align:center}
.big{width:76px;height:76px;border-radius:22px;margin-bottom:18px}
h1{font-size:19px;margin:0 0 8px}
p.d{color:var(--muted);font-size:13.5px;margin:0 0 20px}
.dot{display:inline-block;width:6px;height:6px;border-radius:50%;background:var(--green);margin:0 3px;
 animation:blink 1.2s infinite}
.dot:nth-child(2){animation-delay:.2s}.dot:nth-child(3){animation-delay:.4s}
@keyframes blink{0%,80%,100%{opacity:.25}40%{opacity:1}}
</style></head><body>
<div class="wrap"><div class="center">
  <img class="big" src="/icon.png" alt="">
  <h1>正在启动Harness</h1>
  <p class="d">电脑正在准备，通常 10 秒左右<span class="dot"></span><span class="dot"></span><span class="dot"></span></p>
  <a class="btn ghost" href="/" style="width:auto;padding:10px 18px">返回</a>
  <div class="steps" id="s"></div>
</div></div>
<script>
let n=0;
async function poll(){
  n++;
  try{
    const r=await fetch('/_hub/api/state',{headers:{'accept':'application/json'}});
    const j=await r.json();
    if(j.dsh_ready && j.agent_online){ location.replace('/'); return; }
    document.getElementById('s').textContent = j.agent_online ? '电脑在线，Harness还没就绪' : '电脑客户端不在线';
  }catch(e){}
  if(n>60){ document.getElementById('s').textContent='超时了，请回到设备列表重试'; return; }
  setTimeout(poll,1500);
}
poll();
</script></body></html>`))
}

// 手机端点了“刷新”：让电脑那边把 Harness cookie 也清掉，两边一起重置
