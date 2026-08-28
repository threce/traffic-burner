// traffic-burner —— 流量消耗器
// 通过内存数据流（不写硬盘）快速消耗云服务器的上传/下载流量。
package main

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"
)

//go:embed web
var webFS embed.FS

// Server 持有全局状态。
type Server struct {
	cfg   Config
	cfgMu sync.RWMutex // 保护 cfg（设置页可热更新）
	stats *Stats
	buf   []byte // 预生成的随机数据缓冲，循环发送（只占内存，不占硬盘）
	tg    *TelegramBot

	sendCancelMu   sync.Mutex
	sendCancelFunc context.CancelFunc // 当前服务端直发任务的取消函数

	sessMu   sync.Mutex
	sessions map[string]time.Time // session token -> 过期时间
}

// Config 来自环境变量。
type Config struct {
	Port          string
	AuthUser      string
	AuthPass      string
	TelegramToken string
	ChatID        string
}

func loadConfig() Config {
	return Config{
		Port:          getenv("PORT", "8080"),
		AuthUser:      getenv("AUTH_USER", "admin"),
		AuthPass:      getenv("AUTH_PASS", "changeme"),
		TelegramToken: os.Getenv("TG_BOT_TOKEN"),
		ChatID:        os.Getenv("TG_CHAT_ID"),
	}
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	cfg := loadConfig()
	srv := &Server{
		cfg:      cfg,
		stats:    NewStats(),
		buf:      makeRandomBuffer(16 << 20), // 16MB 内存缓冲
		tg:       NewTelegramBot(cfg.TelegramToken, cfg.ChatID),
		sessions: make(map[string]time.Time),
	}

	// Telegram 指令处理
	srv.tg.onCommand = srv.handleTelegramCommand
	go srv.tg.Run()

	// 从持久化文件加载用户设置（若存在则覆盖环境变量值）
	srv.loadSettings()

	// 嵌入的前端页面
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatalf("embed web: %v", err)
	}
	fileServer := http.FileServer(http.FS(sub))

	mux := http.NewServeMux()
	mux.HandleFunc("/login", srv.serveLoginPage)
	mux.HandleFunc("/api/login-code", srv.handleLoginCode)
	mux.HandleFunc("/api/login", srv.handleLogin)
	mux.HandleFunc("/api/logout", srv.withAuth(srv.handleLogout))
	mux.HandleFunc("/api/settings", srv.withAuth(http.HandlerFunc(srv.handleSettingsGet)))
	mux.HandleFunc("/api/settings-update", srv.withAuth(http.HandlerFunc(srv.handleSettingsUpdate)))
	mux.Handle("/", fileServer)
	mux.HandleFunc("/api/download", srv.withAuth(http.HandlerFunc(srv.handleDownload)))
	mux.HandleFunc("/api/upload", srv.withAuth(http.HandlerFunc(srv.handleUpload)))
	mux.HandleFunc("/api/send", srv.withAuth(http.HandlerFunc(srv.handleSend)))
	mux.HandleFunc("/api/sendstop", srv.withAuth(http.HandlerFunc(srv.handleSendStop)))
	mux.HandleFunc("/api/stats", srv.withAuth(http.HandlerFunc(srv.handleStats)))
	mux.HandleFunc("/api/reset", srv.withAuth(http.HandlerFunc(srv.handleReset)))

	addr := ":" + cfg.Port
	log.Printf("traffic-burner 启动，地址 http://0.0.0.0:%s （登录=用户名/密码 + TGBot 验证码）", cfg.Port)
	log.Printf("内存缓冲 %d MB（不写入硬盘）", len(srv.buf)>>20)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}

// withAuth 通过 Bearer token 校验登录态。
func (s *Server) withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.checkSession(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// checkSession 校验 Authorization。支持两种方式：
//  1. Bearer <token> —— 前端登录后的 session token
//  2. Basic <base64> —— 用户名/密码直接匹配（便于服务端直发/脚本调用）
func (s *Server) checkSession(r *http.Request) bool {
	h := r.Header.Get("Authorization")
	if len(h) >= 8 && h[:7] == "Bearer " {
		token := h[7:]
		s.sessMu.Lock()
		defer s.sessMu.Unlock()
		exp, ok := s.sessions[token]
		if !ok {
			return false
		}
		if time.Now().After(exp) {
			delete(s.sessions, token)
			return false
		}
		return true
	}
	return s.checkBasicAuth(h)
}

// checkBasicAuth 校验 HTTP Basic Auth（用户名/密码与配置一致）。
func (s *Server) checkBasicAuth(authHeader string) bool {
	user, pass, ok := parseBasicAuth(authHeader)
	if !ok {
		return false
	}
	s.cfgMu.RLock()
	uOK := subtle.ConstantTimeCompare([]byte(user), []byte(s.cfg.AuthUser)) == 1
	pOK := subtle.ConstantTimeCompare([]byte(pass), []byte(s.cfg.AuthPass)) == 1
	s.cfgMu.RUnlock()
	return uOK && pOK
}

// parseBasicAuth 解析 "Basic base64(user:pass)" 请求头。
func parseBasicAuth(header string) (user, pass string, ok bool) {
	const prefix = "Basic "
	if len(header) < len(prefix) || header[:len(prefix)] != prefix {
		return "", "", false
	}
	dec, err := base64.StdEncoding.DecodeString(header[len(prefix):])
	if err != nil {
		return "", "", false
	}
	for i := 0; i < len(dec); i++ {
		if dec[i] == ':' {
			return string(dec[:i]), string(dec[i+1:]), true
		}
	}
	return "", "", false
}

// 解析 ?bytes=N（-1 表示不限/持续，默认 -1）
func queryBytes(r *http.Request) int64 {
	v := r.URL.Query().Get("bytes")
	if v == "" {
		return -1
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return -1
	}
	return n
}
