package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// handleDownload 流式发送内存随机数据（不写盘）。
// GET /api/download?bytes=N&id=k
//   - bytes=-1 或省略：无限发送直到客户端断开或 stop
//   - 该方向产生“服务器出站流量”（消耗上传流量）
func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	bytes := queryBytes(r)
	if bytes < -1 {
		http.Error(w, "bytes 必须 >= -1", http.StatusBadRequest)
		return
	}
	s.stats.connOpen()
	defer s.stats.connClose()

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	if bytes >= 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(bytes, 10))
	}
	w.WriteHeader(http.StatusOK)

	remaining := bytes
	for {
		if remaining == 0 {
			break
		}
		chunk := s.buf
		if remaining > 0 && remaining < int64(len(chunk)) {
			chunk = chunk[:remaining]
		}
		n, err := w.Write(chunk)
		if n > 0 {
			s.stats.addUpload(int64(n))
			if remaining > 0 {
				remaining -= int64(n)
			}
		}
		if err != nil {
			return // 客户端断开
		}
		if remaining == 0 {
			break
		}
	}
}

// handleUpload 接收请求体并直接在内存丢弃（不写盘）。
// POST /api/upload?id=k
//   - 该方向产生“服务器入站流量”（消耗下载流量）
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	s.stats.connOpen()
	defer s.stats.connClose()
	n, err := io.Copy(io.Discard, r.Body)
	if err != nil {
		log.Printf("upload 中断: %v", err)
	}
	s.stats.addDownload(n)
	w.WriteHeader(http.StatusOK)
	io.WriteString(w, "ok")
}

// sendRequest 服务端直发任务参数。
type sendRequest struct {
	Target     string `json:"target"`      // host:port（或 http://host:port）
	Threads    int    `json:"threads"`     // 并发连接数 1-64
	Seconds    int    `json:"seconds"`     // 持续秒数 1-3600（0=连续）
	Mode       string `json:"mode"`        // "tcp"（默认）或 "http"
	TargetUser string `json:"target_user"` // http 模式下目标 Basic Auth 用户名（可选）
	TargetPass string `json:"target_pass"` // http 模式下目标 Basic Auth 密码（可选）
}

// handleSend 服务端主动向目标地址发送数据。
// POST /api/send  {"target":"1.2.3.4:443","threads":8,"seconds":60}
// seconds=0 表示连续不限时，手动 /stop 才停。
func (s *Server) handleSend(w http.ResponseWriter, r *http.Request) {
	var req sendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "无效的 JSON", http.StatusBadRequest)
		return
	}
	if req.Threads < 1 || req.Threads > 64 {
		http.Error(w, "threads 必须在 1-64 之间", http.StatusBadRequest)
		return
	}
	if req.Seconds < 0 || req.Seconds > 3600 {
		http.Error(w, "seconds 必须 0-3600（0=连续不限时，手动停止才停）", http.StatusBadRequest)
		return
	}
	mode := req.Mode
	if mode == "" {
		mode = "tcp"
	}
	if mode != "tcp" && mode != "http" {
		http.Error(w, "mode 必须是 tcp 或 http", http.StatusBadRequest)
		return
	}
	if mode == "tcp" {
		if _, _, err := net.SplitHostPort(req.Target); err != nil {
			http.Error(w, "tcp 模式下 target 必须是 host:port 格式", http.StatusBadRequest)
			return
		}
	} else {
		if !strings.HasPrefix(req.Target, "http://") && !strings.HasPrefix(req.Target, "https://") {
			http.Error(w, "http 模式下 target 必须是 http://host:port 或 https://host:port", http.StatusBadRequest)
			return
		}
	}
	if s.stats.isSendRunning() {
		http.Error(w, "已有直发任务在运行", http.StatusConflict)
		return
	}

	s.stats.markSend(req.Target, req.Threads, true)
	var ctx context.Context
	var cancel context.CancelFunc
	if req.Seconds == 0 {
		// 连续模式下不限时，仅靠手动 stop 取消
		ctx, cancel = context.WithCancel(context.Background())
	} else {
		ctx, cancel = context.WithTimeout(context.Background(), time.Duration(req.Seconds)*time.Second)
	}
	s.setSendCancel(cancel)
	go s.runSend(ctx, req.Target, req.Threads, mode, req.TargetUser, req.TargetPass)
	w.WriteHeader(http.StatusAccepted)
	io.WriteString(w, "started")
}

// handleSendStop 取消当前正在运行的服务端直发任务。
func (s *Server) handleSendStop(w http.ResponseWriter, r *http.Request) {
	s.sendCancelMu.Lock()
	cancel := s.sendCancelFunc
	s.sendCancelFunc = nil
	s.sendCancelMu.Unlock()
	if cancel != nil {
		cancel()
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) setSendCancel(cancel context.CancelFunc) {
	s.sendCancelMu.Lock()
	defer s.sendCancelMu.Unlock()
	s.sendCancelFunc = cancel
}

func (s *Server) clearSendCancel() {
	s.sendCancelMu.Lock()
	defer s.sendCancelMu.Unlock()
	s.sendCancelFunc = nil
}

func (s *Stats) isSendRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sendRunning
}

// runSend 每个线程一个连接，持续发送内存缓冲，直到超时或被取消。
func (s *Server) runSend(ctx context.Context, target string, threads int, mode, targetUser, targetPass string) {
	defer s.stats.markSend("", 0, false)
	defer s.clearSendCancel()

	var wg sync.WaitGroup
	for i := 0; i < threads; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if mode == "http" {
				s.sendHTTP(ctx, target, targetUser, targetPass)
			} else {
				s.sendOne(ctx, target)
			}
		}()
	}
	wg.Wait()
	log.Printf("服务端直发结束 target=%s mode=%s threads=%d", target, mode, threads)
}

// sendOne 裸 TCP 写内存缓冲。出站流量计入“上传流量”。
func (s *Server) sendOne(ctx context.Context, target string) {
	d := net.Dialer{}
	conn, err := d.DialContext(ctx, "tcp", target)
	if err != nil {
		log.Printf("直发连接失败 %s: %v", target, err)
		return
	}
	defer conn.Close()
	s.stats.connOpen()
	defer s.stats.connClose()

	deadline, _ := ctx.Deadline()
	conn.SetWriteDeadline(deadline)
	for {
		n, err := conn.Write(s.buf)
		if n > 0 {
			s.stats.addUpload(int64(n))
		}
		if err != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		default:
		}
	}
}

// sendHTTP 向目标 HTTP 端点持续发送 POST 请求体。
func (s *Server) sendHTTP(ctx context.Context, target, targetUser, targetPass string) {
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: false}}
	const blockSize = 64 << 20
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if !s.sendHTTPOnce(ctx, client, target, targetUser, targetPass, blockSize) {
			return
		}
	}
}

func (s *Server) sendHTTPOnce(ctx context.Context, client *http.Client, target, targetUser, targetPass string, blockSize int64) bool {
	pr, pw := io.Pipe()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, pr)
	if err != nil {
		pr.Close()
		return false
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Content-Length", strconv.FormatInt(blockSize, 10))
	if targetUser != "" {
		req.SetBasicAuth(targetUser, targetPass)
	}
	sent := int64(0)
	go func() {
		for sent < blockSize {
			if err := ctx.Err(); err != nil {
				pw.Close()
				return
			}
			chunk := s.buf
			if blockSize-sent < int64(len(chunk)) {
				chunk = chunk[:blockSize-sent]
			}
			n, err := pw.Write(chunk)
			if n > 0 {
				sent += int64(n)
				s.stats.addUpload(int64(n))
			}
			if err != nil {
				pw.Close()
				return
			}
		}
		pw.Close()
	}()
	resp, err := client.Do(req)
	if err != nil {
		pr.Close()
		return false
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return true
}

// handleStats 返回全局统计 JSON。
func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.stats.snapshot())
}

// handleReset 清零统计（正在进行的连接不受影响）。
func (s *Server) handleReset(w http.ResponseWriter, r *http.Request) {
	s.stats.reset()
	writeJSON(w, map[string]any{"ok": true})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(v)
}
