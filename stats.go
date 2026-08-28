package main

import (
	"math/rand"
	"sync"
	"sync/atomic"
	"time"
)

// Stats 全局流量统计。
// 方向定义：
//   - UploadBytes   服务器 -> 公网（出站），由“浏览器下载”或“服务端直发”产生
//   - DownloadBytes 公网 -> 服务器（入站），由“浏览器上传”产生
type Stats struct {
	UploadBytes   atomic.Int64 // 出站（消耗“上传流量”）
	DownloadBytes atomic.Int64 // 入站（消耗“下载流量”）
	ActiveConns   atomic.Int64 // 当前活跃连接数
	StartedAt     time.Time
	mu            sync.Mutex
	sendStartedAt time.Time // 服务端直发任务开始时间
	sendRunning   bool
	sendTarget    string
	sendThreads   int
}

func NewStats() *Stats {
	return &Stats{StartedAt: time.Now()}
}

func (s *Stats) addUpload(n int64)   { s.UploadBytes.Add(n) }
func (s *Stats) addDownload(n int64) { s.DownloadBytes.Add(n) }

func (s *Stats) connOpen()  { s.ActiveConns.Add(1) }
func (s *Stats) connClose() { s.ActiveConns.Add(-1) }

// markSend 标记服务端直发任务状态。
func (s *Stats) markSend(target string, threads int, running bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sendTarget = target
	s.sendThreads = threads
	s.sendRunning = running
	if running {
		s.sendStartedAt = time.Now()
	}
}

// snapshot 返回统计快照（供 JSON 输出）。
func (s *Stats) snapshot() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return map[string]any{
		"upload_bytes":    s.UploadBytes.Load(),
		"download_bytes":  s.DownloadBytes.Load(),
		"active_conns":    s.ActiveConns.Load(),
		"uptime_seconds":  int64(time.Since(s.StartedAt).Seconds()),
		"send_running":    s.sendRunning,
		"send_target":     s.sendTarget,
		"send_threads":    s.sendThreads,
		"send_started_at": s.sendStartedAt.Format(time.RFC3339),
	}
}

func (s *Stats) reset() {
	s.UploadBytes.Store(0)
	s.DownloadBytes.Store(0)
	s.mu.Lock()
	s.sendRunning = false
	s.mu.Unlock()
}

// makeRandomBuffer 用快速伪随机数生成器填充缓冲区。
// 只用于产生流量数据，不需要加密级随机性。
func makeRandomBuffer(size int) []byte {
	buf := make([]byte, size)
	r := rand.New(rand.NewSource(0x123456789abcdef))
	r.Read(buf)
	return buf
}
