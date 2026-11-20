package main

import (
	"image/color"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const bgDir = "/opt/dshhub/bg"

func bgPath(uid string) string { return filepath.Join(bgDir, uid+".png") }

func (a *App) hasBG(uid string) bool {
	_, err := os.Stat(bgPath(uid))
	return err == nil
}

// POST /_hub/api/theme/bg  (multipart: file, opacity)
func (a *App) handleThemeBG(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, 405, "method not allowed")
		return
	}
	uid := uidOf(r)
	if err := r.ParseMultipartForm(12 << 20); err != nil {
		jsonErr(w, 400, "上传失败")
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		jsonErr(w, 400, "没有选择图片")
		return
	}
	defer file.Close()
	if err := os.MkdirAll(bgDir, 0o750); err != nil {
		jsonErr(w, 500, "服务端错误")
		return
	}
	tmp, err := os.CreateTemp("", "rhbg-*")
	if err != nil {
		jsonErr(w, 500, "服务端错误")
		return
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := io.Copy(tmp, io.LimitReader(file, 12<<20)); err != nil {
		tmp.Close()
		jsonErr(w, 400, "上传失败")
		return
	}
	tmp.Close()

	// 点阵化：主题绿
	tint := color.RGBA{0x35, 0xD6, 0x9B, 0xFF}
	if err := writeHalftone(tmpPath, bgPath(uid), 1400, 900, 7, tint); err != nil {
		jsonErr(w, 400, "这张图片读不了")
		return
	}
	op := 35
	if v := r.FormValue("opacity"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 && n <= 100 {
			op = n
		}
	}
	a.store.SetBGOpacity(uid, op)
	jsonOut(w, 200, map[string]any{"ok": true, "opacity": op})
}

func (a *App) handleThemeOpacity(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, 405, "method not allowed")
		return
	}
	var in struct {
		Opacity int `json:"opacity"`
	}
	_ = readJSON(r, &in)
	if in.Opacity < 0 {
		in.Opacity = 0
	}
	if in.Opacity > 100 {
		in.Opacity = 100
	}
	a.store.SetBGOpacity(uidOf(r), in.Opacity)
	jsonOut(w, 200, map[string]any{"ok": true})
}

func (a *App) handleThemeDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, 405, "method not allowed")
		return
	}
	_ = os.Remove(bgPath(uidOf(r)))
	jsonOut(w, 200, map[string]any{"ok": true})
}

// GET /_hub/bg/<uid>.png
func (a *App) serveBG(w http.ResponseWriter, r *http.Request) {
	uid := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/_hub/bg/"), ".png")
	sess := a.readSession(r)
	if sess == nil || sess.UID != uid {
		// 允许客户端用 token 访问自己那张
		if u, ok := verifyToken([]byte(a.cfg.TokenSecret), bearer(r)); !ok || u != uid {
			http.NotFound(w, r)
			return
		}
	}
	f, err := os.Open(bgPath(uid))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = io.Copy(w, f)
}
