package main

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// handleTelegramCommand 处理来自 Telegram 的指令，实现在聊天中完全控制消耗流量。
// 支持指令：
//
//	/help 或 /start        显示帮助
//	/status               查看统计
//	/burn up|down|both <size> 启动浏览器同款消耗（服务端直发到指定目标；需带目标）
//	/send <target> <threads> <seconds>       裸 TCP 直发到 host:port
//	/send <http-url> <threads> <seconds>     HTTP 直发到对方 /api/upload
//	/stop                 停止服务端直发
//	/reset                清零统计
//	/bind                 将当前聊天绑定为本机默认用户
func (s *Server) handleTelegramCommand(text, chatID string) {
	// 首条消息自动绑定当前 chat 为本机默认用户
	if s.cfg.ChatID == "" {
		s.tg.chatID = chatID
		s.cfg.ChatID = chatID
		s.reply(chatID, "✅ 已自动绑定当前聊天（chat_id="+chatID+"）为本机默认用户。")
	}

	fields := strings.Fields(text)
	if len(fields) == 0 {
		return
	}
	cmd := strings.TrimPrefix(strings.ToLower(fields[0]), "/")
	args := fields[1:]

	switch cmd {
	case "help", "start":
		s.reply(chatID, s.helpText())
	case "status":
		s.reply(chatID, s.statusText())
	case "bind":
		s.tg.chatID = chatID
		s.cfg.ChatID = chatID
		s.reply(chatID, "✅ 已绑定 chat_id="+chatID)
	case "send":
		s.cmdSend(chatID, args)
	case "burn":
		s.cmdBurn(chatID, args)
	case "speed":
		s.cmdSpeed(chatID, args)
	case "stop":
		s.sendStopNow()
		s.reply(chatID, "⏹ 已请求停止所有服务端直发。")
	case "reset":
		s.stats.reset()
		s.reply(chatID, "🧹 统计已清零。")
	default:
		s.reply(chatID, "❓ 未知指令。发送 /help 查看帮助。")
	}
}

func (s *Server) reply(chatID, text string) {
	if err := s.tg.SendTo(chatID, text); err != nil {
		// 失败静默，避免刷屏
	}
}

func (s *Server) helpText() string {
	return `<b>🤖 Traffic Burner 指令</b>

/status — 查看实时统计
/burn &lt;up|down|both&gt; &lt;MB|GB&gt; &lt;host:port&gt; — 启动指定目标直发（HTTTP/裸TCP自动识别）
/send &lt;host:port&gt; &lt;threads&gt; &lt;seconds&gt; — 裸 TCP 直发
/send &lt;http://url&gt; &lt;threads&gt; &lt;seconds&gt; — HTTP 直发到对方 /api/upload
/stop — 停止直发
/reset — 清零统计
/bind — 绑定当前聊天为本机用户
/help — 帮助`
}

func (s *Server) statusText() string {
	snap := s.stats.snapshot()
	up := snap["upload_bytes"].(int64)
	down := snap["download_bytes"].(int64)
	conns := snap["active_conns"].(int64)
	upSec := snap["uptime_seconds"].(int64)
	run := "非运行"
	if snap["send_running"].(bool) {
		run = fmt.Sprintf("运行中(%s)", snap["send_target"].(string))
	}
	return fmt.Sprintf("<b>📊 Traffic Burner 统计</b>\n\n⬆ 上传: %s\n⬇ 下载: %s\n🔌 连接: %d\n⏳ 运行: %ds\n🚀 直发: %s",
		humanBytes(up), humanBytes(down), conns, upSec, run)
}

// sendCtx 根据秒数创建上下文：seconds==0 表示连续（无超时，手动取消），>0 限时。
func (s *Server) sendCtx(seconds int) (context.Context, context.CancelFunc) {
	if seconds == 0 {
		return context.WithCancel(context.Background())
	}
	return context.WithTimeout(context.Background(), time.Duration(seconds)*time.Second)
}

// cmdSend：/send <target> <threads> <seconds>
func (s *Server) cmdSend(chatID string, args []string) {
	if len(args) < 3 {
		s.reply(chatID, "用法: /send <host:port 或 http://url> <threads> <seconds>")
		return
	}
	target := args[0]
	threads, err1 := strconv.Atoi(args[1])
	seconds, err2 := strconv.Atoi(args[2])
	if err1 != nil || err2 != nil || threads < 1 || threads > 64 || seconds < 0 || seconds > 3600 {
		s.reply(chatID, "参数无效：threads 1-64，seconds 0-3600（0=连续，手动 /stop 才停）。")
		return
	}
	mode := "tcp"
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
		mode = "http"
	} else if strings.Contains(target, ":") {
		mode = "tcp"
	}
	if s.stats.isSendRunning() {
		s.reply(chatID, "⚠️ 已有直发任务在运行，请先 /stop。")
		return
	}
	s.stats.markSend(target, threads, true)
	ctx, cancel := s.sendCtx(seconds)
	s.setSendCancel(cancel)
	go s.runSend(ctx, target, threads, mode, "", "")
	s.reply(chatID, fmt.Sprintf("🚀 已启动直发：%d 线程 → %s（%s），%s。", threads, target, mode, fmtDur(seconds)))
}
// size 支持如 10MB / 2GB。
func (s *Server) cmdBurn(chatID string, args []string) {
	if len(args) < 2 {
		s.reply(chatID, "用法: /burn <up|down|both> <大小，如100MB/2GB> <host:port 或 http://url>")
		return
	}
	target := ""
	if len(args) >= 3 {
		target = args[2]
	}
	if target == "" {
		s.reply(chatID, "请指定目标地址（host:port 或 http://url）。")
		return
	}
	threads := 8
	seconds := 60
	mode := "tcp"
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
		mode = "http"
	}
	if s.stats.isSendRunning() {
		s.reply(chatID, "⚠️ 已有直发任务在运行，请先 /stop。")
		return
	}
	s.stats.markSend(target, threads, true)
	ctx, cancel := s.sendCtx(seconds)
	s.setSendCancel(cancel)
	go s.runSend(ctx, target, threads, mode, "", "")
	s.reply(chatID, fmt.Sprintf("🚀 已启动直发：%d 线程 → %s（%s），持续 %d 秒。", threads, target, mode, seconds))
}

// cmdSpeed：/speed <up|down> —— 用 Cloudflare 单次测速并回复结果。
func (s *Server) cmdSpeed(chatID string, args []string) {
	dir := "down"
	if len(args) > 0 && (args[0] == "up" || args[0] == "down") {
		dir = args[0]
	}
	s.speed.mu.Lock()
	if s.speed.running {
		s.speed.mu.Unlock()
		s.reply(chatID, "⚠️ 已有测速进行中，请稍后再试。")
		return
	}
	s.speed.mu.Unlock()

	s.reply(chatID, "⏱ Cloudflare 测速中（"+dir+"）…")
	client := &http.Client{Timeout: 60 * time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	start := time.Now()
	var bytes int64
	var err error
	if dir == "up" {
		bytes, err = s.speedOnceUp(ctx, client, start)
	} else {
		bytes, err = s.speedOnceDown(ctx, client, start)
	}
	if err != nil {
		s.reply(chatID, "❌ 测速失败："+err.Error())
		return
	}
	elapsed := time.Since(start)
	rate := float64(bytes) / elapsed.Seconds()
	s.reply(chatID, fmt.Sprintf("📊 %s 测速结果：%s<br>用时 %.1f 秒", dirLabel(dir), humanBytes(int64(rate))+"/s", elapsed.Seconds()))
}

func dirLabel(dir string) string {
	if dir == "up" {
		return "上传"
	}
	return "下载"
}

// fmtDur 友好的时长描述。
func fmtDur(seconds int) string {
	if seconds == 0 || seconds == -1 {
		return "连续（手动 /stop 才停）"
	}
	return fmt.Sprintf("持续 %d 秒", seconds)
}

// sendStopNow 取消当前直发。
func (s *Server) sendStopNow() {
	s.sendCancelMu.Lock()
	cancel := s.sendCancelFunc
	s.sendCancelFunc = nil
	s.sendCancelMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// humanBytes 人类可读字节。
func humanBytes(b int64) string {
	if b < 1024 {
		return fmt.Sprintf("%d B", b)
	}
	units := []string{"KB", "MB", "GB", "TB", "PB"}
	v := float64(b)
	i := -1
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	return fmt.Sprintf("%.1f %s", v, units[i])
}
