package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// Cloudflare Speed 测速端点（公开，支持下载/上传）。
const (
	cfSpeedDown   = "https://speed.cloudflare.com/__down"
	cfSpeedUp     = "https://speed.cloudflare.com/__up"
	speedBlock    = int64(10 << 20) // 每次请求 10MB
	speedOnceMax  = int64(50 << 20) // 单次测速累计目标 50MB
	speedHttpTime = 30 * time.Second
)

// SpeedMeter 管理独立的网速测试（不占用/混淆消耗流量的统计）。
type SpeedMeter struct {
	mu          sync.Mutex
	running     bool
	direction   string // "down" / "up"
	continuous  bool
	bytesPerSec float64 // 最近测得的速率 B/s
	totalBytes  int64   // 本次测速累计字节
	startedAt   time.Time
	cancel      context.CancelFunc
}

func NewSpeedMeter() *SpeedMeter {
	return &SpeedMeter{}
}

func (m *SpeedMeter) snapshot() map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	return map[string]any{
		"running":       m.running,
		"direction":     m.direction,
		"continuous":    m.continuous,
		"bytes_per_sec": m.bytesPerSec,
		"total_bytes":   m.totalBytes,
		"started_at":    m.startedAt.Format(time.RFC3339),
	}
}

type speedRequest struct {
	Direction  string `json:"direction"`  // "up" 或 "down"
	Continuous bool   `json:"continuous"` // true=持续（手动停），false=单次自动停
}

// handleSpeedStart 启动测速。POST /api/speed
func (s *Server) handleSpeedStart(w http.ResponseWriter, r *http.Request) {
	var req speedRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "无效请求", http.StatusBadRequest)
		return
	}
	if req.Direction != "up" && req.Direction != "down" {
		http.Error(w, "direction 必须为 up 或 down", http.StatusBadRequest)
		return
	}
	s.speed.mu.Lock()
	if s.speed.running {
		s.speed.mu.Unlock()
		http.Error(w, "已有测速在运行", http.StatusConflict)
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.speed.running = true
	s.speed.direction = req.Direction
	s.speed.continuous = req.Continuous
	s.speed.totalBytes = 0
	s.speed.bytesPerSec = 0
	s.speed.startedAt = time.Now()
	s.speed.cancel = cancel
	s.speed.mu.Unlock()

	go func() {
		if req.Direction == "down" {
			s.speedLoopDown(ctx)
		} else {
			s.speedLoopUp(ctx)
		}
		s.speed.mu.Lock()
		s.speed.running = false
		s.speed.cancel = nil
		s.speed.mu.Unlock()
	}()

	writeJSON(w, map[string]any{"ok": true, "status": s.speed.snapshot()})
}

func (s *Server) handleSpeedStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.speed.snapshot())
}

func (s *Server) handleSpeedStop(w http.ResponseWriter, r *http.Request) {
	s.speed.mu.Lock()
	cancel := s.speed.cancel
	s.speed.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	writeJSON(w, map[string]any{"ok": true, "status": s.speed.snapshot()})
}

func (s *Server) isContinuous() bool {
	s.speed.mu.Lock()
	defer s.speed.mu.Unlock()
	return s.speed.continuous
}

func (s *Server) speedRunning() bool {
	s.speed.mu.Lock()
	defer s.speed.mu.Unlock()
	return s.speed.running
}

// addSpeed 累计传输字节，并按累计值更新速率。
func (s *Server) addSpeed(bytes int64, elapsedSince time.Duration) {
	s.speed.mu.Lock()
	s.speed.totalBytes += bytes
	if elapsedSince > 0 {
		s.speed.bytesPerSec = float64(s.speed.totalBytes) / elapsedSince.Seconds()
	}
	s.speed.mu.Unlock()
}

// speedLoopDown 下载测速循环：持续模式循环直到取消；单次模式累计到 speedOnceMax 后结束。
func (s *Server) speedLoopDown(ctx context.Context) {
	client := &http.Client{Timeout: speedHttpTime}
	started := time.Now()
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return
		}
		consumed, err := s.speedOnceDown(ctx, client, started)
		if err != nil {
			log.Printf("speed down err: %v", err)
			s.speed.mu.Lock()
			s.speed.bytesPerSec = 0
			s.speed.mu.Unlock()
			time.Sleep(1 * time.Second)
			if !s.isContinuous() {
				return
			}
			continue
		}
		total += consumed
		if !s.isContinuous() && total >= speedOnceMax {
			return
		}
	}
}

// speedOnceDown 拉取一个 speedBlock 大小的数据，返回消耗字节。
func (s *Server) speedOnceDown(ctx context.Context, client *http.Client, started time.Time) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfSpeedDown+"?bytes="+strconv.FormatInt(speedBlock, 10), nil)
	if err != nil {
		return 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	buf := make([]byte, 256<<10)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			break
		}
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			total += int64(n)
			if s.isContinuous() {
				s.addSpeed(int64(n), time.Since(started))
			}
		}
		if rerr != nil {
			break
		}
	}
	if total == 0 {
		return 0, io.EOF
	}
	if !s.isContinuous() {
		s.addSpeed(total, time.Since(started))
	}
	return total, nil
}

// speedLoopUp 上传测速循环。
func (s *Server) speedLoopUp(ctx context.Context) {
	client := &http.Client{Timeout: speedHttpTime}
	started := time.Now()
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return
		}
		consumed, err := s.speedOnceUp(ctx, client, started)
		if err != nil {
			log.Printf("speed up err: %v", err)
			s.speed.mu.Lock()
			s.speed.bytesPerSec = 0
			s.speed.mu.Unlock()
			time.Sleep(1 * time.Second)
			if !s.isContinuous() {
				return
			}
			continue
		}
		total += consumed
		if !s.isContinuous() && total >= speedOnceMax {
			return
		}
	}
}

// speedOnceUp 向 /__up 发送一个 speedBlock 大小的数据，返回发送字节。
func (s *Server) speedOnceUp(ctx context.Context, client *http.Client, started time.Time) (int64, error) {
	pr, pw := io.Pipe()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfSpeedUp, pr)
	if err != nil {
		pr.Close()
		return 0, err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.ContentLength = speedBlock
	var sent int64
	go func() {
		for sent < speedBlock {
			if err := ctx.Err(); err != nil {
				pw.Close()
				return
			}
			chunk := s.buf
			if speedBlock-sent < int64(len(chunk)) {
				chunk = chunk[:speedBlock-sent]
			}
			n, werr := pw.Write(chunk)
			if n > 0 {
				sent += int64(n)
			}
			if werr != nil {
				pw.Close()
				return
			}
		}
		pw.Close()
	}()
	resp, err := client.Do(req)
	if err != nil {
		pr.Close()
		return 0, err
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if sent == 0 {
		return 0, io.EOF
	}
	s.addSpeed(sent, time.Since(started))
	return sent, nil
}
