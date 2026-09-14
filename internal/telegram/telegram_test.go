package telegram

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Kandelsbreit/Steam-Hour-Booster-CODEX/internal/engine"
	"github.com/Kandelsbreit/Steam-Hour-Booster-CODEX/internal/model"
	"github.com/Kandelsbreit/Steam-Hour-Booster-CODEX/internal/storage"
)

func testBot(t *testing.T) *Bot {
	t.Helper()
	s, c, f, err := storage.Open(t.TempDir(), storage.DPAPI{})
	if err != nil {
		t.Fatal(err)
	}
	e := engine.New(s, c, f, nil, nil)
	t.Cleanup(func() { _ = e.Close() })
	return New(e)
}
func TestAllowed(t *testing.T) {
	m := &struct {
		Text string `json:"text"`
		Date int64  `json:"date"`
		Chat struct {
			ID   int64  `json:"id"`
			Type string `json:"type"`
		} `json:"chat"`
		From struct {
			ID  int64 `json:"id"`
			Bot bool  `json:"is_bot"`
		} `json:"from"`
	}{Text: "/status", Date: time.Now().Unix()}
	m.Chat.ID = 123
	m.Chat.Type = "private"
	m.From.ID = 123
	u := update{ID: 1, Message: m}
	if !Allowed(u, "123", time.Now()) {
		t.Fatal("valid private command rejected")
	}
	m.From.ID = 124
	if Allowed(u, "123", time.Now()) {
		t.Fatal("foreign user accepted")
	}
}
func TestSanitizedTelegramErrors(t *testing.T) {
	b := testBot(t)
	if err := b.e.Store.WriteSecret("telegram", []byte("123456:TEST_ONLY_SECRET_VALUE_123456789")); err != nil {
		t.Fatal(err)
	}
	b.request = func(context.Context, string, any, any) (int, error) { return 0, context.DeadlineExceeded }
	err := b.api(context.Background(), "getMe", nil, nil)
	if err == nil || strings.Contains(err.Error(), "TEST_ONLY_SECRET") {
		t.Fatal("network error leaked token", err)
	}
	b.request = func(_ context.Context, _ string, _ any, out any) (int, error) {
		raw, _ := json.Marshal(map[string]any{"ok": false, "error_code": 409, "description": "Conflict"})
		_ = json.Unmarshal(raw, out)
		return 409, nil
	}
	err = b.api(context.Background(), "getUpdates", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "конфликт") || strings.Contains(err.Error(), "TEST_ONLY") {
		t.Fatal("conflict not actionable", err)
	}
}
func TestSaveValidatesWithoutEnablingNetwork(t *testing.T) {
	b := testBot(t)
	c := model.Telegram{DailyTime: "21:00", Daily: true, Errors: true}
	if err := b.Save(c, "bad"); err == nil {
		t.Fatal("bad token accepted")
	}
	if err := b.Save(c, "123456:TEST_ONLY_SECRET_VALUE_123456789"); err != nil {
		t.Fatal(err)
	}
	if !b.e.Store.Has("telegram") {
		t.Fatal("token was not protected")
	}
}
