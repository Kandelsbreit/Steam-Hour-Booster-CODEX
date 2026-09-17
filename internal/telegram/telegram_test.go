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

// addAccount registers an account in the engine under test.
func addAccount(t *testing.T, b *Bot, name string) string {
	t.Helper()
	id, err := b.e.Add(name)
	if err != nil {
		t.Fatalf("add account: %v", err)
	}
	return id
}

func TestStatusShowsSteamTwoWeekHours(t *testing.T) {
	b := testBot(t)
	id := addAccount(t, b, "test_user")
	h := 90.0
	c, f := b.e.Data()
	f.Accounts[id].Library = []model.Game{{AppID: 730, Name: "Counter-Strike <2> & Valve", TwoWeeks: &h}, {AppID: 570, Name: "Dota 2"}}
	f.Accounts[id].LibraryAt = time.Now().UnixMilli()
	if err := b.e.Restore(c, f, nil); err != nil {
		t.Fatal(err)
	}
	out := b.Execute("/status")
	if !strings.Contains(out, "test_user") || !strings.Contains(out, "Steam за 2 недели: 1.5 ч") {
		t.Fatalf("two-week hours missing: %s", out)
	}
	if !strings.Contains(out, "Counter-Strike &lt;2&gt; &amp; Valve") {
		t.Fatalf("name not escaped: %s", out)
	}
	if strings.Contains(out, "<2>") {
		t.Fatal("raw HTML injection survived")
	}
}

func TestDiagnosticsDiffersFromStatus(t *testing.T) {
	b := testBot(t)
	addAccount(t, b, "test_user")
	diag := b.Execute("/diagnostics")
	if !strings.Contains(diag, "Диагностика") || !strings.Contains(diag, "Версия:") || !strings.Contains(diag, "Аптайм:") {
		t.Fatalf("diagnostics content missing: %s", diag)
	}
	status := b.Execute("/status")
	if diag == status {
		t.Fatal("/diagnostics duplicates /status")
	}
}

func TestGoalsCommandWithProgress(t *testing.T) {
	b := testBot(t)
	id := addAccount(t, b, "test_user")
	if err := b.e.Edit(id, "goal-save", model.Preset{}, model.Game{}, model.Goal{AppID: 730, Hours: 100, Basis: "local"}); err != nil {
		t.Fatal(err)
	}
	out := b.Execute("/goals")
	if !strings.Contains(out, "App 730") || !strings.Contains(out, "100") || !strings.Contains(out, "0 ч") {
		t.Fatalf("goal progress missing: %s", out)
	}
}

func TestWelcomeHelpRefreshAndUnknown(t *testing.T) {
	b := testBot(t)
	addAccount(t, b, "test_user")
	if !strings.Contains(b.Execute("/start"), "Steam Hours Booster") {
		t.Fatal("welcome missing")
	}
	for _, cmd := range []string{"/help", "/unknown"} {
		out := b.Execute(cmd)
		if !strings.Contains(out, "/hours") || !strings.Contains(out, "/status") || !strings.Contains(out, "/refresh") {
			t.Fatalf("help missing commands: %s", out)
		}
	}
	out := b.Execute("/refresh")
	if !strings.Contains(out, "Обновление библиотеки") || !strings.Contains(out, "test_user") {
		t.Fatalf("refresh report missing: %s", out)
	}
}

func TestHoursCommand(t *testing.T) {
	b := testBot(t)
	id := addAccount(t, b, "user1")
	id2 := addAccount(t, b, "user2")
	h1 := 120.0 // 2.0 hrs
	h2 := 180.0 // 3.0 hrs
	c, f := b.e.Data()
	f.Accounts[id].Library = []model.Game{{AppID: 730, Name: "CS2", TwoWeeks: &h1}}
	f.Accounts[id2].Library = []model.Game{{AppID: 570, Name: "Dota 2", TwoWeeks: &h2}}
	if err := b.e.Restore(c, f, nil); err != nil {
		t.Fatal(err)
	}

	// First /hours call
	out := b.Execute("/hours")
	if !strings.Contains(out, "user1 — 2.0 ч (первый замер)") || !strings.Contains(out, "user2 — 3.0 ч (первый замер)") {
		t.Fatalf("missing accounts in hours output: %s", out)
	}

	// Increase CS2 playtime to 180 mins (+1.0 hr)
	h1New := 180.0
	c, f = b.e.Data()
	f.Accounts[id].Library[0].TwoWeeks = &h1New
	if err := b.e.Restore(c, f, nil); err != nil {
		t.Fatal(err)
	}

	// Second /hours call should reflect diff
	out2 := b.Execute("/hours")
	if !strings.Contains(out2, "user1 — 3.0 ч (+1.0 ч)") {
		t.Fatalf("missing diff in output: %s", out2)
	}
	if !strings.Contains(out2, "user2 — 3.0 ч (+0.0 ч)") {
		t.Fatalf("missing zero diff in output: %s", out2)
	}
}

func TestSplitMessage(t *testing.T) {
	if got := splitMessage("короткая строка"); len(got) != 1 || got[0] != "короткая строка" {
		t.Fatal(got)
	}
	var sb strings.Builder
	for i := 0; i < 400; i++ {
		sb.WriteString(strings.Repeat("а", 30) + "\n")
	}
	parts := splitMessage(sb.String())
	joined := strings.Join(parts, "\n")
	for _, p := range parts {
		if len([]rune(p)) > 3800 {
			t.Fatalf("part too long: %d", len([]rune(p)))
		}
	}
	if strings.ReplaceAll(sb.String(), "\n", "") != strings.ReplaceAll(joined, "\n", "") {
		t.Fatal("content lost in split")
	}
	only := strings.Repeat("ж", 5000)
	parts = splitMessage(only)
	if strings.Join(parts, "") != only {
		t.Fatal("oversize line truncated")
	}
}

func TestMuteBlocksNotificationsAndUnmuteRestores(t *testing.T) {
	b := testBot(t)
	c := model.Telegram{DailyTime: "21:00", Daily: true, Errors: true}
	if err := b.Save(c, ""); err != nil {
		t.Fatal(err)
	}
	// Enabled with a saved token and chat id; verification is skipped because
	// Enabled=false in the saved config, and the run loop is not started here.
	if err := b.e.Store.WriteSecret("telegram", []byte("123456:TEST_ONLY_SECRET_VALUE_123456789")); err != nil {
		t.Fatal(err)
	}
	cfg := b.e.TelegramConfig()
	cfg.Enabled = true
	cfg.ChatID = "123"
	if err := b.e.SetTelegram(cfg); err != nil {
		t.Fatal(err)
	}
	t0 := b.e.TelegramConfig()
	t0.MuteUntil = time.Now().Add(time.Hour).UnixMilli()
	if err := b.e.SetTelegram(t0); err != nil {
		t.Fatal(err)
	}
	b.Notify(model.Notice{Time: time.Now().UnixMilli(), Message: "Сбой сети"})
	b.mu.Lock()
	queued := len(b.queue)
	b.mu.Unlock()
	if queued != 0 {
		t.Fatal("muted notification delivered")
	}
	if out := b.Execute("/unmute"); !strings.Contains(out, "включены") {
		t.Fatalf("unmute failed: %s", out)
	}
	if b.e.TelegramConfig().MuteUntil != 0 {
		t.Fatal("mute window kept")
	}
	b.Notify(model.Notice{Time: time.Now().UnixMilli(), Message: "Сбой сети"})
	b.mu.Lock()
	queued = len(b.queue)
	b.mu.Unlock()
	if queued != 1 {
		t.Fatal("notification lost after unmute")
	}
}

func TestConnectNoticeRequiresSwitch(t *testing.T) {
	b := testBot(t)
	if err := b.e.Store.WriteSecret("telegram", []byte("123456:TEST_ONLY_SECRET_VALUE_123456789")); err != nil {
		t.Fatal(err)
	}
	cfg := b.e.TelegramConfig()
	cfg.Enabled = true
	cfg.ChatID = "123"
	if err := b.e.SetTelegram(cfg); err != nil {
		t.Fatal(err)
	}
	cfg.NotifyConnect = false
	if err := b.e.SetTelegram(cfg); err != nil {
		t.Fatal(err)
	}
	b.Notify(model.Notice{Time: time.Now().UnixMilli(), Message: "Вход аккаунта test_user выполнен", Kind: "connect"})
	b.mu.Lock()
	queued := len(b.queue)
	b.mu.Unlock()
	if queued != 0 {
		t.Fatal("connect notice delivered with switch off")
	}
	cfg.NotifyConnect = true
	if err := b.e.SetTelegram(cfg); err != nil {
		t.Fatal(err)
	}
	b.Notify(model.Notice{Time: time.Now().UnixMilli(), Message: "Вход аккаунта test_user выполнен", Kind: "connect"})
	b.mu.Lock()
	queued = len(b.queue)
	b.mu.Unlock()
	if queued != 1 {
		t.Fatal("connect notice lost with switch on")
	}
}

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

func TestSaveChecksBotAndChatBeforeEnabling(t *testing.T) {
	b := testBot(t)
	var methods []string
	b.request = func(_ context.Context, address string, _ any, out any) (int, error) {
		for _, method := range []string{"getMe", "getWebhookInfo", "getChat"} {
			if strings.HasSuffix(address, "/"+method) {
				methods = append(methods, method)
			}
		}
		raw, _ := json.Marshal(map[string]any{"ok": true, "result": map[string]any{}})
		_ = json.Unmarshal(raw, out)
		return 200, nil
	}
	c := model.Telegram{Enabled: true, ChatID: "123", DailyTime: "21:00", Daily: true, Errors: true}
	if err := b.Save(c, "123456:TEST_ONLY_SECRET_VALUE_123456789"); err != nil {
		t.Fatal(err)
	}
	b.Stop()
	if !strings.Contains(strings.Join(methods, ","), "getMe") || !strings.Contains(strings.Join(methods, ","), "getChat") {
		t.Fatal("credentials and chat were not checked", methods)
	}
}
