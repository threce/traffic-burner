package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
)

// SettingsData 持久化的可变配置。
type SettingsData struct {
	AuthUser      string `json:"auth_user"`
	AuthPass      string `json:"auth_pass"`
	TelegramToken string `json:"telegram_token"`
	ChatID        string `json:"chat_id"`
}

// settingsFile 持久化配置文件的路径（默认 /data/settings.json，可用 SETTINGS_FILE 覆盖）。
func settingsFile() string {
	if f := os.Getenv("SETTINGS_FILE"); f != "" {
		return f
	}
	return "/data/settings.json"
}

// loadSettings 从文件加载配置；若文件存在则覆盖环境变量中的对应字段。
// 在 main 启动时调用一次。
func (s *Server) loadSettings() {
	f := settingsFile()
	data, err := os.ReadFile(f)
	if err != nil {
		// 文件不存在，使用环境变量默认
		return
	}
	var sd SettingsData
	if err := json.Unmarshal(data, &sd); err != nil {
		log.Printf("解析 %s 失败: %v，忽略", f, err)
		return
	}
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()
	if sd.AuthUser != "" {
		s.cfg.AuthUser = sd.AuthUser
	}
	if sd.AuthPass != "" {
		s.cfg.AuthPass = sd.AuthPass
	}
	if sd.TelegramToken != "" {
		s.cfg.TelegramToken = sd.TelegramToken
	}
	if sd.ChatID != "" {
		s.cfg.ChatID = sd.ChatID
	}
	// 同步 TG bot 的 token/chat_id
	s.tg.token = s.cfg.TelegramToken
	s.tg.chatID = s.cfg.ChatID
	log.Printf("已从 %s 加载持久化设置", f)
}

// saveSettings 将当前可变配置保存到持久化文件。
func (s *Server) saveSettings() error {
	s.cfgMu.RLock()
	sd := SettingsData{
		AuthUser:      s.cfg.AuthUser,
		AuthPass:      s.cfg.AuthPass,
		TelegramToken: s.cfg.TelegramToken,
		ChatID:        s.cfg.ChatID,
	}
	s.cfgMu.RUnlock()

	f := settingsFile()
	if err := os.MkdirAll(filepath.Dir(f), 0755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(sd, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(f, b, 0644)
}

// settingsResponse 返回给前端的设置（敏感字段打码）。
type settingsResponse struct {
	AuthUser      string `json:"auth_user"`
	AuthPassMask  string `json:"auth_pass_mask"`
	TelegramToken string `json:"telegram_token"` // 已打码
	ChatID        string `json:"chat_id"`
	SettingsFile  string `json:"settings_file"`
}

// handleSettingsGet 读取当前设置（敏感字段打码）。
func (s *Server) handleSettingsGet(w http.ResponseWriter, r *http.Request) {
	s.cfgMu.RLock()
	resp := settingsResponse{
		AuthUser:      s.cfg.AuthUser,
		AuthPassMask:  maskSecret(s.cfg.AuthPass),
		TelegramToken: maskSecret(s.cfg.TelegramToken),
		ChatID:        s.cfg.ChatID,
		SettingsFile:  settingsFile(),
	}
	s.cfgMu.RUnlock()
	writeJSON(w, resp)
}

// settingsUpdate 前端提交的设置更新。
type settingsUpdate struct {
	AuthUser      string `json:"auth_user"`      // 空=不修改
	AuthPass      string `json:"auth_pass"`      // 空=不修改
	TelegramToken string `json:"telegram_token"` // 空=不修改
	ChatID        string `json:"chat_id"`        // 空=不修改
}

// handleSettingsUpdate 更新设置并持久化。
func (s *Server) handleSettingsUpdate(w http.ResponseWriter, r *http.Request) {
	var up settingsUpdate
	if err := json.NewDecoder(r.Body).Decode(&up); err != nil {
		http.Error(w, "无效请求", http.StatusBadRequest)
		return
	}
	s.cfgMu.Lock()
	if up.AuthUser != "" {
		s.cfg.AuthUser = up.AuthUser
	}
	if up.AuthPass != "" {
		s.cfg.AuthPass = up.AuthPass
	}
	if up.TelegramToken != "" {
		s.cfg.TelegramToken = up.TelegramToken
	}
	if up.ChatID != "" {
		s.cfg.ChatID = up.ChatID
	}
	// 同步 TG bot
	s.tg.token = s.cfg.TelegramToken
	s.tg.chatID = s.cfg.ChatID
	s.cfgMu.Unlock()

	if err := s.saveSettings(); err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": "保存失败: " + err.Error()})
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// maskSecret 打码显示敏感字段（保留前 4 位 + ***）。
func maskSecret(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 4 {
		return "****"
	}
	return s[:4] + "****"
}
