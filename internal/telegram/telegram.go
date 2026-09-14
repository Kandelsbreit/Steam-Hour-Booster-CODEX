package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Kandelsbreit/Steam-Hour-Booster-CODEX/internal/engine"
	"github.com/Kandelsbreit/Steam-Hour-Booster-CODEX/internal/model"
	"github.com/Kandelsbreit/Steam-Hour-Booster-CODEX/internal/netx"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
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
	queue     []string
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
	b.stop()
	if token != "" {
		if err := b.e.Store.WriteSecret("telegram", []byte(token)); err != nil {
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
func (b *Bot) Notify(n model.Notice) {
	c := b.e.TelegramConfig()
	if !c.Enabled {
		return
	}
	goal := strings.HasPrefix(n.Message, "Цель ")
	if !goal && !c.Errors {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if !goal && time.Since(b.lastAlert) < 5*time.Minute {
		return
	}
	if !goal {
		b.lastAlert = time.Now()
	}
	if len(b.queue) < 20 {
		b.queue = append(b.queue, n.Message)
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
				reply := b.Execute(u.Message.Text)
				if err := b.send(ctx, c.ChatID, reply); err != nil {
					return err
				}
			}
			b.mu.Lock()
			n := min(10, len(b.queue))
			messages := append([]string{}, b.queue[:n]...)
			b.mu.Unlock()
			if n > 0 {
				if err := b.send(ctx, c.ChatID, strings.Join(messages, "\n")); err != nil {
					return err
				}
				b.mu.Lock()
				b.queue = b.queue[n:]
				b.mu.Unlock()
			}
			now := time.Now()
			day := now.Format("2006-01-02")
			if c.Daily && c.LastDaily != day && now.Format("15:04") >= c.DailyTime {
				if err := b.send(ctx, c.ChatID, "Ежедневная сводка\n"+b.Report()); err != nil {
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
func (b *Bot) send(ctx context.Context, chat, text string) error {
	r := []rune(text)
	if len(r) > 3900 {
		text = string(r[:3900])
	}
	return b.api(ctx, "sendMessage", map[string]any{"chat_id": chat, "text": text}, nil)
}
func (b *Bot) Report() string {
	s := b.e.Snapshot()
	accounts := s["accounts"].([]any)
	out := []string{}
	day := time.Now().Format("2006-01-02")
	for _, v := range accounts {
		a := v.(map[string]any)
		d := a["data"].(map[string]any)
		today := float64(0)
		if v, ok := d["days"].(map[string]any)[day]; ok {
			today = v.(map[string]any)["gameMs"].(float64)
		}
		out = append(out, fmt.Sprintf("%s: %s\nСегодня: %.2f игровых ч; всего локально: %.2f ч\nАктивных игр: %d", a["name"], a["status"], today/3600000, d["gameMs"].(float64)/3600000, len(a["current"].([]any))))
	}
	if len(out) == 0 {
		return "Аккаунтов нет"
	}
	return strings.Join(out, "\n\n")
}
func (b *Bot) Execute(text string) string {
	parts := strings.Fields(text)
	if len(parts) == 0 {
		return ""
	}
	command := strings.ToLower(strings.Split(parts[0], "@")[0])
	c, f := b.e.Data()
	accounts := []model.Account{}
	for _, a := range c.Accounts {
		if len(parts) < 2 || parts[1] == "all" || strings.EqualFold(a.Name, parts[1]) {
			accounts = append(accounts, a)
		}
	}
	out := []string{}
	switch command {
	case "/status", "/report":
		return b.Report()
	case "/pause", "/resume", "/next":
		if len(accounts) == 0 {
			return "Аккаунт не найден"
		}
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
				out = append(out, a.Name+": "+err.Error())
			} else {
				out = append(out, a.Name+": выполнено")
			}
		}
		return strings.Join(out, "\n")
	case "/diagnostics":
		return b.Report()
	case "/goals":
		for _, a := range c.Accounts {
			for _, g := range f.Accounts[a.ID].Goals {
				out = append(out, fmt.Sprintf("%s / %d: цель %.2f ч (%s)", a.Name, g.AppID, g.Hours, g.Basis))
			}
		}
		if len(out) == 0 {
			return "Целей нет"
		}
		return strings.Join(out, "\n")
	case "/goal":
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
			return err.Error()
		}
		return "Цель сохранена"
	}
	return "/status /report /diagnostics /goals\n/pause [логин|all]\n/resume [логин|all]\n/next [логин|all]\n/goal логин AppID часы [local|steam]"
}
