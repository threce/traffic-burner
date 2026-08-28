package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"
)

type loginRequest struct {
	User string `json:"user"`
	Pass string `json:"pass"`
	Code string `json:"code"`
}

// handleLoginCode 生成验证码并通过 TGBot 推送（无需登录即可点“获取验证码”）。
// POST /api/login-code
func (s *Server) handleLoginCode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.cfg.TelegramToken == "" {
		writeJSON(w, map[string]any{"ok": false, "error": "TGBot 未配置，无法发送验证码"})
		return
	}
	code, err := s.tg.SendLoginCode()
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": "验证码发送失败: " + err.Error()})
		return
	}
	_ = code
	writeJSON(w, map[string]any{"ok": true, "message": "验证码已通过 Telegram 发送"})
}

// handleLogin 校验用户名/密码 + Telegram 验证码，成功返回 session token。
// POST /api/login
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "无效请求", http.StatusBadRequest)
		return
	}
	// 用户名/密码用恒定时间比较
	uOK := subtle.ConstantTimeCompare([]byte(req.User), []byte(s.cfg.AuthUser)) == 1
	pOK := subtle.ConstantTimeCompare([]byte(req.Pass), []byte(s.cfg.AuthPass)) == 1
	if !uOK || !pOK {
		writeJSON(w, map[string]any{"ok": false, "error": "用户名或密码错误"})
		return
	}
	// TGBot 验证码
	if s.cfg.TelegramToken != "" && !s.tg.ValidateCode(req.Code) {
		writeJSON(w, map[string]any{"ok": false, "error": "验证码无效或已过期"})
		return
	}
	token := genToken()
	s.sessMu.Lock()
	s.sessions[token] = time.Now().Add(24 * time.Hour)
	s.sessMu.Unlock()
	writeJSON(w, map[string]any{"ok": true, "token": token})
}

// handleLogout 注销当前 session。
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if token := bearerToken(r); token != "" {
		s.sessMu.Lock()
		delete(s.sessions, token)
		s.sessMu.Unlock()
	}
	writeJSON(w, map[string]any{"ok": true})
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && h[:7] == "Bearer " {
		return h[7:]
	}
	return ""
}

func genToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// serveLoginPage 返回独立的登录页 /login（无需登录即可访问）。
func (s *Server) serveLoginPage(w http.ResponseWriter, r *http.Request) {
	data, err := webFS.ReadFile("web/login.html")
	if err != nil {
		http.Error(w, "login page not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(data)
}
