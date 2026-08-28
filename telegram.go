package main

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// TelegramBot 封装 Telegram Bot API 的长轮询 getUpdates。
type TelegramBot struct {
	token  string
	chatID string
	http   *http.Client
	// 待验证的验证码：code -> 过期时间
	codeMu  sync.Mutex
	pending map[string]time.Time
	// 最近收到的指令处理器（由 Server 提供）
	onCommand func(text, chatID string)
	stop      chan struct{}
}

func NewTelegramBot(token, chatID string) *TelegramBot {
	return &TelegramBot{
		token:   token,
		chatID:  chatID,
		http:    &http.Client{Timeout: 15 * time.Second},
		pending: make(map[string]time.Time),
		stop:    make(chan struct{}),
	}
}

// sendMessage 通过 sendMessage API 向指定 chat 发送文本。
func (b *TelegramBot) sendMessage(chatID, text string) error {
	if b.token == "" {
		return fmt.Errorf("TG_BOT_TOKEN 未配置")
	}
	if chatID == "" {
		chatID = b.chatID
	}
	u := "https://api.telegram.org/bot" + b.token + "/sendMessage"
	form := url.Values{}
	form.Set("chat_id", chatID)
	form.Set("text", text)
	form.Set("parse_mode", "HTML")
	form.Set("disable_web_page_preview", "true")
	resp, err := b.http.PostForm(u, form)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != 200 {
		return fmt.Errorf("telegram sendMessage status=%d", resp.StatusCode)
	}
	return nil
}

// Send 发送消息给绑定用户（chatID 为空时用默认）。
func (b *TelegramBot) Send(text string) error {
	return b.sendMessage(b.chatID, text)
}

// SendTo 发送给指定用户。
func (b *TelegramBot) SendTo(chatID, text string) error {
	return b.sendMessage(chatID, text)
}

// generateCode 生成一次性 6 位验证码并返回。
func generateCode() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		// 兑底：用时间戳
		return fmt.Sprintf("%06d", time.Now().UnixNano()%1000000)
	}
	n := (uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])) % 1000000
	return fmt.Sprintf("%06d", n)
}

// SendLoginCode 生成验证码并通过 bot 推送给绑定用户，60 秒有效。
func (b *TelegramBot) SendLoginCode() (string, error) {
	code := generateCode()
	b.codeMu.Lock()
	b.pending[code] = time.Now().Add(60 * time.Second)
	b.codeMu.Unlock()

	go func() {
		time.Sleep(61 * time.Second)
		b.codeMu.Lock()
		delete(b.pending, code)
		b.codeMu.Unlock()
	}()

	err := b.Send(fmt.Sprintf("🔐 验证码：<b>%s</b>\n（60 秒内有效，仅用于本次登录）", code))
	return code, err
}

// ValidateCode 校验验证码是否有效且未被使用（一次性）。
func (b *TelegramBot) ValidateCode(code string) bool {
	if code == "" {
		return false
	}
	b.codeMu.Lock()
	defer b.codeMu.Unlock()
	exp, ok := b.pending[code]
	if !ok {
		return false
	}
	if time.Now().After(exp) {
		delete(b.pending, code)
		return false
	}
	delete(b.pending, code) // 一次性
	return true
}

// Run 启动长轮询 getUpdates 循环，处理收到的指令。
func (b *TelegramBot) Run() {
	if b.token == "" {
		log.Printf("TG_BOT_TOKEN 未配置，TGBot 功能已禁用")
		return
	}
	log.Printf("TGBot 已启用，开始长轮询（chat_id=%s）", b.chatID)
	offset := 0
	for {
		select {
		case <-b.stop:
			return
		default:
		}
		u := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates?timeout=30&offset=%d", b.token, offset)
		resp, err := b.http.Get(u)
		if err != nil {
			log.Printf("TG getUpdates 失败: %v", err)
			time.Sleep(3 * time.Second)
			continue
		}
		var res struct {
			OK     bool `json:"ok"`
			Result []struct {
				UpdateID int `json:"update_id"`
				Message  *struct {
					From struct {
						ID int64 `json:"id"`
					} `json:"from"`
					Chat struct {
						ID int64 `json:"id"`
					} `json:"chat"`
					Text string `json:"text"`
				} `json:"message"`
			} `json:"result"`
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err := json.Unmarshal(body, &res); err != nil || !res.OK {
			log.Printf("TG getUpdates 解析失败: %v", err)
			time.Sleep(3 * time.Second)
			continue
		}
		for _, r := range res.Result {
			offset = r.UpdateID + 1
			if r.Message != nil {
				chatID := fmt.Sprintf("%d", r.Message.Chat.ID)
				text := strings.TrimSpace(r.Message.Text)
				if b.onCommand != nil {
					b.onCommand(text, chatID)
				}
			}
		}
	}
}

// Stop 停止轮询。
func (b *TelegramBot) Stop() {
	close(b.stop)
}
