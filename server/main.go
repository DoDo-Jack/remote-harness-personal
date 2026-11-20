package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

const maxBody = 1 << 20

// 中继帧可能很大（DSH 的 JS 资源、上传），单独放宽
const maxFrame = 64 << 20

type App struct {
	cfg     *Config
	store   *Store
	codes   *CodeStore
	limiter *Limiter
	pairs   *pairStore
	hub     *Hub
	started time.Time
}

/* ───────────── 中继 ───────────── */

type agentConn struct {
	deviceID string
	userID   string
	ws       *wsConn
}

type clientConn struct {
	deviceID string
	userID   string
	ws       *wsConn
}

type Hub struct {
	mu      sync.RWMutex
	agents  map[string]*agentConn
	clients map[*clientConn]bool

	pmu     sync.Mutex
	pending map[string]func([]byte)

	rmu     sync.RWMutex
	ready   map[string]bool
}

func newHub() *Hub {
	return &Hub{agents: map[string]*agentConn{}, clients: map[*clientConn]bool{}, pending: map[string]func([]byte){}, ready: map[string]bool{}}
}

func (h *Hub) setAgent(a *agentConn) {
	h.mu.Lock()
	if old := h.agents[a.deviceID]; old != nil {
		go old.ws.Close()
	}
	h.agents[a.deviceID] = a
	h.mu.Unlock()
	h.notify(a.deviceID, true)
}

func (h *Hub) removeAgent(a *agentConn) {
	h.mu.Lock()
	if cur := h.agents[a.deviceID]; cur == a {
		delete(h.agents, a.deviceID)
	}
	h.mu.Unlock()
	h.notify(a.deviceID, false)
}

func (h *Hub) addClient(c *clientConn) { h.mu.Lock(); h.clients[c] = true; h.mu.Unlock() }
func (h *Hub) delClient(c *clientConn) { h.mu.Lock(); delete(h.clients, c); h.mu.Unlock() }

func (h *Hub) agentOf(deviceID string) *agentConn {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.agents[deviceID]
}

func (h *Hub) toAgent(deviceID string, data []byte) bool {
	a := h.agentOf(deviceID)
	if a == nil {
		return false
	}
	return a.ws.WriteText(data) == nil
}

func (h *Hub) toClients(deviceID string, data []byte) {
	h.mu.RLock()
	var targets []*clientConn
	for c := range h.clients {
		if c.deviceID == deviceID {
			targets = append(targets, c)
		}
	}
	h.mu.RUnlock()
	for _, c := range targets {
		_ = c.ws.WriteText(data)
	}
}

func (h *Hub) dropDevice(deviceID string) {
	h.mu.Lock()
	if a := h.agents[deviceID]; a != nil {
		go a.ws.Close()
		delete(h.agents, deviceID)
	}
	h.mu.Unlock()
}

func (h *Hub) setReady(deviceID string, v bool) {
	h.rmu.Lock()
	h.ready[deviceID] = v
	h.rmu.Unlock()
}

func (h *Hub) isReady(deviceID string) bool {
	h.rmu.RLock()
	defer h.rmu.RUnlock()
	return h.ready[deviceID]
}

func (h *Hub) wait(id string, fn func([]byte)) {
	h.pmu.Lock()
	h.pending[id] = fn
	h.pmu.Unlock()
}

func (h *Hub) forget(id string) {
	h.pmu.Lock()
	delete(h.pending, id)
	h.pmu.Unlock()
}

func (h *Hub) deliver(id string, data []byte) bool {
	h.pmu.Lock()
	fn, ok := h.pending[id]
	h.pmu.Unlock()
	if ok {
		fn(data)
	}
	return ok
}

func (h *Hub) notify(deviceID string, online bool) {
	raw, _ := json.Marshal(map[string]any{"t": "agent", "online": online})
	h.toClients(deviceID, raw)
}

/* ───────────── HTTP 工具 ───────────── */

type ctxKey int

const ctxUID ctxKey = 1

func jsonOut(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func jsonErr(w http.ResponseWriter, code int, msg string) {
	jsonOut(w, code, map[string]any{"ok": false, "error": msg})
}

func readJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxBody))
	if err != nil {
		return err
	}
	if len(raw) == 0 {
		return errors.New("empty body")
	}
	return json.Unmarshal(raw, v)
}

func clientIP(r *http.Request) string {
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return ip
	}
	if ip := r.Header.Get("X-Forwarded-For"); ip != "" {
		return strings.TrimSpace(strings.Split(ip, ",")[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return strings.TrimSpace(r.URL.Query().Get("token"))
}

/* ───────────── 中间件 ───────────── */

// 浏览器带 cookie、客户端带 Bearer token，两种都认
func (a *App) authedAny(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if uid, ok := verifyToken([]byte(a.cfg.TokenSecret), bearer(r)); ok && a.store.User(uid) != nil {
			next(w, r.WithContext(context.WithValue(r.Context(), ctxUID, uid)))
			return
		}
		if sess := a.readSession(r); sess != nil && a.store.User(sess.UID) != nil {
			next(w, r.WithContext(context.WithValue(r.Context(), ctxUID, sess.UID)))
			return
		}
		jsonErr(w, http.StatusUnauthorized, "unauthorized")
	}
}

func uidOf(r *http.Request) string {
	v, _ := r.Context().Value(ctxUID).(string)
	return v
}

/* ───────────── 账号 ───────────── */

var phoneRe = regexp.MustCompile(`^1[3-9]\d{9}$`)

func (a *App) nodeInfo() map[string]any {
	for _, n := range a.cfg.Pool.Nodes {
		if strings.Contains(n.Host, "dsh.") {
			return map[string]any{"id": n.ID, "name": n.Name, "host": n.Host}
		}
	}
	if len(a.cfg.Pool.Nodes) > 0 {
		n := a.cfg.Pool.Nodes[0]
		return map[string]any{"id": n.ID, "name": n.Name, "host": n.Host}
	}
	return map[string]any{"id": "local", "name": "本地", "host": ""}
}

func maskPhone(p string) string {
	if len(p) != 11 {
		return p
	}
	return p[:3] + "****" + p[7:]
}

func (a *App) handleMe(w http.ResponseWriter, r *http.Request) {
	uid := uidOf(r)
	u := a.store.User(uid)
	if u == nil {
		jsonErr(w, 401, "unauthorized")
		return
	}
	jsonOut(w, 200, map[string]any{
		"ok":      true,
		"user":    map[string]any{"id": u.ID, "phone": maskPhone(u.Phone)},
		"devices": a.store.DevicesOf(uid),
		"pool":    a.cfg.Pool,
	})
}

/* ───────────── 设备 ───────────── */

func (a *App) handleDevices(w http.ResponseWriter, r *http.Request) {
	uid := uidOf(r)
	switch r.Method {
	case http.MethodGet:
		ds := a.store.DevicesOf(uid)
		out := make([]map[string]any, 0, len(ds))
		for _, d := range ds {
			out = append(out, map[string]any{
				"id": d.ID, "name": d.Name, "os": d.OS, "kind": d.Kind,
				"model": d.Model, "chip": d.Chip, "os_version": d.OSVersion,
				"last_seen": d.LastSeen,
				"online":    a.hub.agentOf(d.ID) != nil || (d.Kind == "web" && time.Since(d.LastSeen) < 2*time.Minute),
			})
		}
		jsonOut(w, 200, map[string]any{"ok": true, "devices": out})
	case http.MethodPost:
		var in struct {
			Name      string `json:"name"`
			OS        string `json:"os"`
			Kind      string `json:"kind"`
			Model     string `json:"model"`
			Chip      string `json:"chip"`
			OSVersion string `json:"osVersion"`
		}
		_ = readJSON(r, &in)
		name := strings.TrimSpace(in.Name)
		if name == "" {
			name = "我的 Mac"
		}
		d := a.store.AddDevice(uid, name, in.OS, in.Kind, in.Model, in.Chip, in.OSVersion)
		if d == nil {
			jsonErr(w, 400, "创建失败")
			return
		}
		jsonOut(w, 200, map[string]any{"ok": true, "device": d})
	default:
		jsonErr(w, 405, "method not allowed")
	}
}

func (a *App) handleDeviceByID(w http.ResponseWriter, r *http.Request) {
	uid := uidOf(r)
	id := strings.TrimPrefix(r.URL.Path, "/api/devices/")
	if id == "" {
		jsonErr(w, 404, "not found")
		return
	}
	if r.Method == http.MethodPut || r.Method == http.MethodPatch {
		var in struct {
			Name      string `json:"name"`
			Kind      string `json:"kind"`
			Model     string `json:"model"`
			Chip      string `json:"chip"`
			OSVersion string `json:"osVersion"`
		}
		_ = readJSON(r, &in)
		d := a.store.Device(id)
		if d == nil || d.UserID != uid {
			jsonErr(w, 404, "设备不存在")
			return
		}
		a.store.UpdateDeviceMeta(id, in.Name, in.Kind, in.Model, in.Chip, in.OSVersion)
		jsonOut(w, 200, map[string]any{"ok": true})
		return
	}
	if r.Method != http.MethodDelete {
		jsonErr(w, 405, "method not allowed")
		return
	}
	if !a.store.RemoveDevice(uid, id) {
		jsonErr(w, 404, "设备不存在")
		return
	}
	a.hub.dropDevice(id)
	jsonOut(w, 200, map[string]any{"ok": true})
}

/* ───────────── WebSocket 两端 ───────────── */

func (a *App) handleAgentWS(w http.ResponseWriter, r *http.Request) {
	uid, ok := verifyToken([]byte(a.cfg.TokenSecret), bearer(r))
	if !ok {
		jsonErr(w, 401, "unauthorized")
		return
	}
	ws, err := upgradeWS(w, r)
	if err != nil {
		return
	}
	ws.StartPing(25 * time.Second)

	_, first, err := ws.ReadMessage(maxFrame)
	if err != nil {
		ws.Close()
		return
	}
	var hello struct {
		T        string `json:"t"`
		DeviceID string `json:"deviceId"`
	}
	if json.Unmarshal(first, &hello) != nil || hello.T != "hello" {
		_ = ws.WriteText([]byte(`{"t":"error","error":"expect hello"}`))
		ws.Close()
		return
	}
	d := a.store.Device(hello.DeviceID)
	if d == nil || d.UserID != uid {
		_ = ws.WriteText([]byte(`{"t":"error","error":"unknown device"}`))
		ws.Close()
		return
	}
	agent := &agentConn{deviceID: d.ID, userID: uid, ws: ws}
	a.hub.setAgent(agent)
	a.store.TouchDevice(d.ID)
	log.Printf("agent online device=%s user=%s", d.ID, uid)
	defer func() {
		a.hub.removeAgent(agent)
		ws.Close()
		log.Printf("agent offline device=%s", d.ID)
	}()

	for {
		_, data, err := ws.ReadMessage(maxFrame)
		if err != nil {
			return
		}
		var head struct {
			T string `json:"t"`
		}
		_ = json.Unmarshal(data, &head)
		if head.T == "hello" || head.T == "pong" {
			continue
		}
		if head.T == "state" {
			var st struct {
				DSH bool `json:"dsh"`
			}
			_ = json.Unmarshal(data, &st)
			a.hub.setReady(d.ID, st.DSH)
			continue
		}
		var withID struct{ ID string `json:"id"` }
		_ = json.Unmarshal(data, &withID)
		if withID.ID != "" && a.hub.deliver(withID.ID, data) {
			continue
		}
		a.hub.toClients(d.ID, data)
	}
}

func (a *App) handleClientWS(w http.ResponseWriter, r *http.Request) {
	uid, ok := verifyToken([]byte(a.cfg.TokenSecret), bearer(r))
	if !ok {
		jsonErr(w, 401, "unauthorized")
		return
	}
	ws, err := upgradeWS(w, r)
	if err != nil {
		return
	}
	ws.StartPing(25 * time.Second)

	_, first, err := ws.ReadMessage(maxFrame)
	if err != nil {
		ws.Close()
		return
	}
	var attach struct {
		T        string `json:"t"`
		DeviceID string `json:"deviceId"`
	}
	if json.Unmarshal(first, &attach) != nil || attach.T != "attach" {
		_ = ws.WriteText([]byte(`{"t":"error","error":"expect attach"}`))
		ws.Close()
		return
	}
	d := a.store.Device(attach.DeviceID)
	if d == nil || d.UserID != uid {
		_ = ws.WriteText([]byte(`{"t":"error","error":"unknown device"}`))
		ws.Close()
		return
	}
	client := &clientConn{deviceID: d.ID, userID: uid, ws: ws}
	a.hub.addClient(client)
	online := a.hub.agentOf(d.ID) != nil
	raw, _ := json.Marshal(map[string]any{"t": "attached", "device": d.ID, "online": online})
	_ = ws.WriteText(raw)
	if !online {
		_ = ws.WriteText([]byte(`{"t":"offline","error":"设备不在线"}`))
	} else if a := a.hub.agentOf(d.ID); a != nil {
		_ = a.ws.WriteText([]byte(`{"t":"wake","device":"` + d.ID + `"}`))
	}
	log.Printf("client attached device=%s user=%s", d.ID, uid)
	defer func() {
		a.hub.delClient(client)
		ws.Close()
	}()

	for {
		_, data, err := ws.ReadMessage(maxFrame)
		if err != nil {
			return
		}
		var head struct {
			T string `json:"t"`
		}
		_ = json.Unmarshal(data, &head)
		switch head.T {
		case "attach":
			continue
		case "ping":
			_ = ws.WriteText([]byte(`{"t":"pong"}`))
			continue
		}
		if !a.hub.toAgent(d.ID, data) {
			_ = ws.WriteText([]byte(`{"t":"offline","error":"设备不在线"}`))
		}
	}
}

/* ───────────── 其它 ───────────── */

func (a *App) handlePool(w http.ResponseWriter, r *http.Request) {
	jsonOut(w, 200, map[string]any{"ok": true, "pool": a.cfg.Pool})
}

func (a *App) handleHealth(w http.ResponseWriter, r *http.Request) {
	jsonOut(w, 200, map[string]any{
		"ok":      true,
		"service": "dshhub",
		"uptime":  int(time.Since(a.started).Seconds()),
		"node":    a.nodeInfo(),
	})
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("[dshhub] ")

	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	store, err := openStore(cfg.DataDir)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	app := &App{
		cfg:     cfg,
		store:   store,
		codes:   newCodeStore(),
		limiter: newLimiter(),
		pairs:   newPairStore(),
		hub:     newHub(),
		started: time.Now(),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/agent", app.handleAgentWS)
	mux.HandleFunc("/client", app.handleClientWS)
	mux.HandleFunc("/", app.handleEntry)

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    32 << 10,
	}
	log.Printf("listening on %s (nodes=%d)", cfg.Listen, len(cfg.Pool.Nodes))
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("serve: %v", err)
	}
}
