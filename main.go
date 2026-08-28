// traffic-burner —— 流量消耗器
// 通过内存数据流（不写硬盘）快速消耗云服务器的上传/下载流量。
package main

import (
	"context"
	"crypto/subtle"
	"embed"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
)

//go:embed web
var webFS embed.FS

// Server 持有全局状态。
type Server struct {
	cfg   Config
	stats *Stats
	buf   []byte // 预生成的随机数据缓冲，循环发送（只占内存，不占硬盘）

	sendCancelMu   sync.Mutex
	sendCancelFunc context.CancelFunc // 当前服务端直发任务的取消函数
}

// Config 来自环境变量。
type Config struct {
	Port     string
	AuthUser string
	AuthPass string
}

func loadConfig() Config {
	return Config{
		Port:     getenv("PORT", "8080"),
		AuthUser: os.Getenv("AUTH_USER"),
		AuthPass: os.Getenv("AUTH_PASS"),
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
		cfg:   cfg,
		stats: NewStats(),
		buf:   makeRandomBuffer(16 << 20), // 16MB 内存缓冲
	}

	// 嵌入的前端页面
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatalf("embed web: %v", err)
	}
	fileServer := http.FileServer(http.FS(sub))

	mux := http.NewServeMux()
	mux.Handle("/", srv.withAuth(fileServer))
	mux.HandleFunc("/api/download", srv.withAuth(http.HandlerFunc(srv.handleDownload)))
	mux.HandleFunc("/api/upload", srv.withAuth(http.HandlerFunc(srv.handleUpload)))
	mux.HandleFunc("/api/send", srv.withAuth(http.HandlerFunc(srv.handleSend)))
	mux.HandleFunc("/api/sendstop", srv.withAuth(http.HandlerFunc(srv.handleSendStop)))
	mux.HandleFunc("/api/stats", srv.withAuth(http.HandlerFunc(srv.handleStats)))
	mux.HandleFunc("/api/reset", srv.withAuth(http.HandlerFunc(srv.handleReset)))

	addr := ":" + cfg.Port
	if cfg.AuthUser != "" {
		log.Printf("traffic-burner 启动，地址 http://0.0.0.0:%s （已启用用户名/密码鉴权）", cfg.Port)
	} else {
		log.Printf("traffic-burner 启动，地址 http://0.0.0.0:%s （未设置鉴权！请尽快配置 AUTH_USER/AUTH_PASS）", cfg.Port)
	}
	log.Printf("内存缓冲 %d MB（不写入硬盘）", len(srv.buf)>>20)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}

// withAuth 校验 HTTP Basic Auth（当配置了 AUTH_USER 时）。
func (s *Server) withAuth(next http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.AuthUser != "" && !s.checkAuth(r) {
			w.Header().Set("WWW-Authenticate", `Basic realm="traffic-burner"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	}
}

func (s *Server) checkAuth(r *http.Request) bool {
	user, pass, ok := r.BasicAuth()
	if !ok {
		return false
	}
	uOK := subtle.ConstantTimeCompare([]byte(user), []byte(s.cfg.AuthUser)) == 1
	pOK := subtle.ConstantTimeCompare([]byte(pass), []byte(s.cfg.AuthPass)) == 1
	return uOK && pOK
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
