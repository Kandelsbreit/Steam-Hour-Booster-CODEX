package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Kandelsbreit/Steam-Hour-Booster-CODEX/internal/engine"
	"github.com/Kandelsbreit/Steam-Hour-Booster-CODEX/internal/model"
	"github.com/Kandelsbreit/Steam-Hour-Booster-CODEX/internal/netx"
)

type Request func(context.Context, string, any, any) (int, error)
type Bot struct {
	e         *engine.Engine
	request   Request
	control   sync.Mutex
	mu        sync.Mutex
	cancel    context.CancelFunc
	done      chan struct{}
	status    string
	queue     []model.Notice
	lastAlert time.Time
}

func New(e *engine.Engine) *Bot   { return &Bot{e: e, request: netx.JSON, status: "Выключен"} }
func (b *Bot) setStatus(s string) { b.mu.Lock(); b.status = s; b.mu.Unlock() }
func (b *Bot) Snapshot() map[string]any {
	c := b.e.TelegramConfig()
	raw, _ := json.Marshal(c)
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	b.mu.Lock()
	out["status"] = b.status
	b.mu.Unlock()
	out["hasToken"] = b.e.Store.Has("telegram")
	return out
}
func (b *Bot) stop() {
	if b.cancel != nil {
		b.cancel()
		<-b.done
		b.cancel = nil
	}
	b.mu.Lock()
	b.status = "Выключен"
	b.queue = nil
	b.mu.Unlock()
}
func (b *Bot) Stop()    { b.control.Lock(); defer b.control.Unlock(); b.stop() }
func (b *Bot) Restart() { b.control.Lock(); defer b.control.Unlock(); b.restart() }
func (b *Bot) restart() {
	b.stop()
	if !b.e.TelegramConfig().Enabled {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	b.cancel = cancel
	b.done = make(chan struct{})
	b.setStatus("Подключение…")
	go func() { defer close(b.done); b.run(ctx) }()
}

var tokenRE = regexp.MustCompile(`^\d{5,20}:[A-Za-z0-9_-]{20,100}$`)
var chatRE = regexp.MustCompile(`^\d{1,20}$`)

func (b *Bot) Save(c model.Telegram, token string) error {
	b.control.Lock()
	defer b.control.Unlock()
	token = strings.TrimSpace(token)
	c.ChatID = strings.TrimSpace(c.ChatID)
	if token != "" && !tokenRE.MatchString(token) {
		return errors.New("Неверный токен Telegram-бота")
	}
	if c.ChatID != "" && !chatRE.MatchString(c.ChatID) {
		return errors.New("Используй ID личного чата (положительное число)")
	}
	if !model.ValidTime(c.DailyTime) {
		return errors.New("Укажи время ежедневной сводки")
	}
	if c.Enabled && (c.ChatID == "" || (token == "" && !b.e.Store.Has("telegram"))) {
		return errors.New("Укажи токен бота и ID личного чата")
	}
	old := b.e.TelegramConfig()
	c.Offset = old.Offset
	c.LastDaily = old.LastDaily
	if token != "" || c.ChatID != old.ChatID {
		c.Offset = 0
		c.LastDaily = ""
	}
	if token == "" {
		c.MuteUntil = old.MuteUntil
	}
	previous, previousErr := b.e.Store.ReadSecret("telegram")
	b.stop()
	if token != "" {
		if err := b.e.Store.WriteSecret("telegram", []byte(token)); err != nil {
			b.restart()
			return err
		}
	}
	if c.Enabled {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		err := b.verify(ctx, c.ChatID)
		cancel()
		if err != nil {
			if previousErr == nil {
				_ = b.e.Store.WriteSecret("telegram", previous)
			} else if token != "" {
				_ = b.e.Store.Forget("telegram")
			}
			b.restart()
			return err
		}
	}
	if err := b.e.SetTelegram(c); err != nil {
		b.restart()
		return err
	}
	b.restart()
	return nil
}

func (b *Bot) verify(ctx context.Context, chat string) error {
	if err := b.api(ctx, "getMe", map[string]any{}, nil); err != nil {
		return err
	}
	var hook struct {
		URL string `json:"url"`
	}
	if err := b.api(ctx, "getWebhookInfo", map[string]any{}, &hook); err != nil {
		return err
	}
	if hook.URL != "" {
		return errors.New("Telegram: у бота настроен webhook; отключи его или используй другого бота")
	}
	if err := b.api(ctx, "getChat", map[string]any{"chat_id": chat}, nil); err != nil {
		return err
	}
	return nil
}

type apiError struct {
	Code, Retry int
	Message     string
}

func (e *apiError) Error() string { return e.Message }

func (b *Bot) api(ctx context.Context, method string, body any, out any) error {
	token, err := b.e.Store.ReadSecret("telegram")
	if err != nil {
		return errors.New("Токен Telegram недоступен в хранилище Windows")
	}
	defer clear(token)
	var response struct {
		OK         bool            `json:"ok"`
		Code       int             `json:"error_code"`
		Result     json.RawMessage `json:"result"`
		Parameters struct {
			Retry int `json:"retry_after"`
		} `json:"parameters"`
	}
	status, err := b.request(ctx, "https://api.telegram.org/bot"+string(token)+"/"+method, body, &response)
	if err != nil {
		return err
	}
	if !response.OK {
		code := response.Code
		if code == 0 {
			code = status
		}
		msg := fmt.Sprintf("Telegram отклонил запрос (код %d)", code)
		switch code {
		case 401:
			msg = "Telegram: неверный или отозванный токен"
		case 403:
			msg = "Telegram: бот заблокирован пользователем"
		case 409:
			msg = "Telegram: конфликт опроса — закрой другую программу с этим ботом и проверь webhook"
		case 429:
			msg = "Telegram: превышена частота запросов"
		case 400:
			msg = "Telegram: неверный запрос или ID чата; напиши боту /start"
		}
		return &apiError{code, response.Parameters.Retry, msg}
	}
	if out != nil && json.Unmarshal(response.Result, out) != nil {
		return errors.New("Telegram: повреждённый ответ")
	}
	return nil
}

// Notify queues an alert for delivery from the polling loop. Goal messages are
// always delivered when the bot is on, error notices follow the switch and the
// rate limit, "connect" notices additionally require NotifyConnect, and
// everything respects the mute window set by /mute.
func (b *Bot) Notify(n model.Notice) {
	c := b.e.TelegramConfig()
	if !c.Enabled {
		return
	}
	goal := strings.HasPrefix(n.Message, "Цель ")
	switch {
	case goal:
	case n.Kind == "connect":
		if !c.NotifyConnect {
			return
		}
	default:
		if !c.Errors {
			return
		}
	}
	now := time.Now()
	if c.MuteUntil > now.UnixMilli() {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if !goal && time.Since(b.lastAlert) < 5*time.Minute {
		return
	}
	if !goal {
		b.lastAlert = now
	}
	if len(b.queue) < 20 {
		b.queue = append(b.queue, model.Notice{Time: n.Time, Message: n.Message})
	}
}

func wait(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

type update struct {
	ID      int64 `json:"update_id"`
	Message *struct {
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
	} `json:"message"`
}

func Allowed(u update, chat string, now time.Time) bool {
	m := u.Message
	return m != nil && m.Text != "" && m.Chat.Type == "private" && !m.From.Bot && strconv.FormatInt(m.Chat.ID, 10) == chat && strconv.FormatInt(m.From.ID, 10) == chat && now.Unix()-m.Date <= 120 && m.Date <= now.Unix()+30
}
func (b *Bot) run(ctx context.Context) {
	failures := 0
	validated := false
	for ctx.Err() == nil {
		err := func() error {
			if !validated {
				if err := b.api(ctx, "getMe", map[string]any{}, nil); err != nil {
					return err
				}
				var hook struct {
					URL string `json:"url"`
				}
				if err := b.api(ctx, "getWebhookInfo", map[string]any{}, &hook); err != nil {
					return err
				}
				if hook.URL != "" {
					return errors.New("Telegram: у бота настроен webhook; используй отдельного бота или отключи webhook в прежней программе")
				}
				validated = true
				b.setStatus("Подключён")
			}
			c := b.e.TelegramConfig()
			var updates []update
			if err := b.api(ctx, "getUpdates", map[string]any{"offset": c.Offset, "timeout": 25, "allowed_updates": []string{"message"}}, &updates); err != nil {
				return err
			}
			for _, u := range updates {
				if err := b.e.UpdateTelegramProgress(u.ID+1, ""); err != nil {
					return errors.New("Telegram: не удалось сохранить позицию команд")
				}
				if !Allowed(u, c.ChatID, time.Now()) {
					continue
				}
				if err := b.sendParts(ctx, c.ChatID, b.Execute(u.Message.Text)); err != nil {
					return err
				}
			}
			b.mu.Lock()
			n := min(10, len(b.queue))
			messages := append([]model.Notice{}, b.queue[:n]...)
			b.mu.Unlock()
			if n > 0 {
				lines := []string{}
				for _, m := range messages {
					lines = append(lines, "⚠️ "+esc(m.Message))
				}
				if err := b.sendParts(ctx, c.ChatID, strings.Join(lines, "\n")); err != nil {
					return err
				}
				b.mu.Lock()
				b.queue = b.queue[n:]
				b.mu.Unlock()
			}
			now := time.Now()
			day := now.Format("2006-01-02")
			if c.Daily && c.LastDaily != day && now.Format("15:04") >= c.DailyTime {
				if err := b.sendParts(ctx, c.ChatID, section("Ежедневная сводка")+"\n"+b.statusReport("")); err != nil {
					return err
				}
				if err := b.e.UpdateTelegramProgress(0, day); err != nil {
					return err
				}
			}
			return nil
		}()
		if ctx.Err() != nil {
			return
		}
		delay := time.Second
		if err != nil {
			failures++
			delay = time.Duration(min(900, 30*(1<<min(failures-1, 5)))) * time.Second
			var api *apiError
			if errors.As(err, &api) && api.Retry > 0 {
				delay = max(delay, time.Duration(min(api.Retry, 86400))*time.Second)
			}
			b.setStatus(fmt.Sprintf("%s. Повтор через %d с", err, int(delay.Seconds())))
			if failures == 1 {
				b.e.DiagnosticLog(err.Error())
			}
		} else {
			failures = 0
			b.setStatus("Подключён")
		}
		if !wait(ctx, delay) {
			return
		}
	}
}

// send delivers one message as Telegram HTML; if Telegram rejects the markup,
// the same text is retried without parse_mode so a report is never lost.
func (b *Bot) send(ctx context.Context, chat, text string) error {
	r := []rune(text)
	if len(r) > 3900 {
		text = string(r[:3900])
	}
	err := b.api(ctx, "sendMessage", map[string]any{"chat_id": chat, "text": text, "parse_mode": "HTML", "disable_web_page_preview": true}, nil)
	if err == nil {
		return nil
	}
	var api *apiError
	if errors.As(err, &api) && api.Code == 400 {
		return b.api(ctx, "sendMessage", map[string]any{"chat_id": chat, "text": text}, nil)
	}
	return err
}

// sendParts splits a long reply into Telegram-sized parts on line boundaries.
func (b *Bot) sendParts(ctx context.Context, chat, text string) error {
	for _, part := range splitMessage(text) {
		if err := b.send(ctx, chat, part); err != nil {
			return err
		}
	}
	return nil
}

// statusReport renders /status: live engine state plus the cached Steam
// library, whose TwoWeeks minutes are the profile's "past two weeks" hours.
func (b *Bot) statusReport(name string) string {
	s := b.e.Snapshot()
	_, f := b.e.Data()
	now := time.Now()
	accounts, _ := s["accounts"].([]any)
	out := []string{}
	for _, v := range accounts {
		a, ok := v.(map[string]any)
		if !ok {
			continue
		}
		accountName, _ := a["name"].(string)
		if name != "" && name != "all" && !strings.EqualFold(accountName, name) {
			continue
		}
		id, _ := a["id"].(string)
		out = append(out, formatStatus(a, f.Accounts[id], now))
	}
	if len(out) == 0 {
		if name != "" && name != "all" {
			return "Аккаунт не найден"
		}
		return "Аккаунтов нет. Добавь их в программе."
	}
	return strings.Join(out, "\n")
}

func (b *Bot) refreshAccounts(name string) string {
	c, f := b.e.Data()
	now := time.Now()
	out := []string{}
	requested := false
	for _, a := range c.Accounts {
		if name != "" && name != "all" && !strings.EqualFold(a.Name, name) {
			continue
		}
		requested = true
		d := f.Accounts[a.ID]
		dAge := int64(0)
		if d != nil {
			dAge = d.LibraryAt
		}
		age := ""
		if dAge > 0 {
			age = ", прошлые данные " + shortAgo(dAge, now)
		}
		if err := b.e.RefreshLibrary(a.ID); err != nil {
			out = append(out, "⛔️ "+esc(a.Name)+": "+esc(err.Error()))
			continue
		}
		out = append(out, "✅ "+esc(a.Name)+": запрос отправлен"+age+". Свежие часы появятся в /status через 1–2 минуты.")
	}
	if !requested {
		if name == "" || name == "all" {
			return "Аккаунтов нет. Добавь их в программе."
		}
		return "Аккаунт не найден"
	}
	return "Обновление библиотеки Steam\n" + strings.Join(out, "\n")
}

func (b *Bot) hoursReport() string {
	c, _ := b.e.Data()
	if len(c.Accounts) == 0 {
		return "Аккаунтов нет. Добавь их в программе."
	}

	// Trigger library refresh for all online accounts
	for _, a := range c.Accounts {
		_ = b.e.ForceRefreshLibrary(a.ID)
	}

	// Wait up to 10 seconds for library updates to finish if busy
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		anyBusy := false
		for _, a := range c.Accounts {
			if b.e.LibraryBusy(a.ID) {
				anyBusy = true
				break
			}
		}
		if !anyBusy {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	_, f := b.e.Data()
	reports := []string{}
	for _, a := range c.Accounts {
		d := f.Accounts[a.ID]
		rep := formatAccountHoursOnly(a.Name, d, true, func(m map[string]float64) {
			_ = b.e.UpdateAccountLastPlaytime(a.ID, m)
		})
		reports = append(reports, rep)
	}
	return strings.Join(reports, "\n")
}

func (b *Bot) Execute(text string) string {
	parts := strings.Fields(text)
	if len(parts) == 0 {
		return ""
	}
	command := strings.ToLower(strings.Split(parts[0], "@")[0])
	arg := ""
	if len(parts) > 1 {
		arg = parts[1]
	}
	c, f := b.e.Data()
	accounts := []model.Account{}
	for _, a := range c.Accounts {
		if len(parts) < 2 || parts[1] == "all" || strings.EqualFold(a.Name, parts[1]) {
			accounts = append(accounts, a)
		}
	}
	switch command {
	case "/hours":
		return b.hoursReport()
	case "/start":
		return formatWelcome()
	case "/help":
		return helpText()
	case "/status", "/report":
		return b.statusReport(arg)
	case "/refresh":
		return b.refreshAccounts(arg)
	case "/diagnostics":
		return formatDiagnostics(b.e.Snapshot())
	case "/goals":
		id := ""
		if arg != "" && arg != "all" {
			for _, a := range accounts {
				if strings.EqualFold(a.Name, arg) {
					id = a.ID
					break
				}
			}
			if id == "" {
				return "Аккаунт не найден"
			}
		}
		return formatGoals(c, f, id, b.e, time.Now())
	case "/mute":
		if arg == "" {
			return "Укажи минуты: /mute 60"
		}
		minutes, err := strconv.Atoi(arg)
		if err != nil || minutes < 1 || minutes > 43200 {
			return "Минуты: число от 1 до 43200"
		}
		t := b.e.TelegramConfig()
		t.MuteUntil = time.Now().Add(time.Duration(minutes) * time.Minute).UnixMilli()
		if err = b.e.SetTelegram(t); err != nil {
			return err.Error()
		}
		return "🔕 Тишина до " + time.Now().Add(time.Duration(minutes)*time.Minute).Format("15:04:05") + " (" + strconv.Itoa(minutes) + " мин). /unmute — включить уведомления раньше."
	case "/unmute":
		t := b.e.TelegramConfig()
		t.MuteUntil = 0
		if err := b.e.SetTelegram(t); err != nil {
			return err.Error()
		}
		return "🔔 Уведомления снова включены."
	case "/pause", "/resume", "/next":
		if len(accounts) == 0 {
			return "Аккаунт не найден"
		}
		out := []string{}
		for _, a := range accounts {
			var err error
			switch command {
			case "/pause":
				err = b.e.Stop(a.ID)
			case "/resume":
				err = b.e.Start(a.ID, "")
			case "/next":
				err = b.e.Next(a.ID)
			}
			if err != nil {
				out = append(out, "⛔️ "+esc(a.Name)+": "+esc(err.Error()))
			} else {
				what := map[string]string{"/pause": "остановлен", "/resume": "запущен", "/next": "переключён на следующую партию"}[command]
				out = append(out, "✅ "+esc(a.Name)+": "+what)
			}
		}
		return strings.Join(out, "\n")
	case "/goal", "/goal-set":
		if len(accounts) != 1 || len(parts) < 4 {
			return "/goal логин AppID часы [local|steam]"
		}
		ids, err := model.ParseIDs(parts[2])
		hours, he := strconv.ParseFloat(parts[3], 64)
		if err != nil || he != nil || len(ids) != 1 {
			return "Неверная цель"
		}
		basis := "local"
		if len(parts) > 4 {
			basis = parts[4]
		}
		if err = b.e.Edit(accounts[0].ID, "goal-save", model.Preset{}, model.Game{}, model.Goal{AppID: ids[0], Hours: hours, Basis: basis}); err != nil {
			return esc(err.Error())
		}
		return "✅ Цель сохранена"
	}
	return helpText()
}
