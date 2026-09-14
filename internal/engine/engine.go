package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Kandelsbreit/Steam-Hour-Booster-CODEX/internal/model"
	"github.com/Kandelsbreit/Steam-Hour-Booster-CODEX/internal/steam"
	"github.com/Kandelsbreit/Steam-Hour-Booster-CODEX/internal/storage"
	"github.com/google/uuid"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Guard struct {
	Kind      string `json:"kind"`
	Wrong     bool   `json:"wrong"`
	WaitUntil int64  `json:"waitUntil"`
}
type live struct {
	revision                                                                                uint64
	clearing                                                                                bool
	client                                                                                  steam.Client
	cancel                                                                                  context.CancelFunc
	ctx                                                                                     context.Context
	generation                                                                              uint64
	desired, online, blocked, busy, closing, libraryBusy                                    bool
	status                                                                                  string
	guard                                                                                   *Guard
	retryAt, readyAt, pendingAt, startedAt, breakUntil, stableAt, blockedAt, libraryAttempt int64
	failures, index                                                                         int
	activeMS, gameMS, batchElapsed, workMS                                                  int64
	sent                                                                                    []uint32
	lastTick                                                                                time.Time
}
type Engine struct {
	mu                            sync.Mutex
	Store                         *storage.Store
	Config                        model.Config
	Features                      model.Features
	factory                       steam.Factory
	clock                         func() time.Time
	live                          map[string]*live
	ctx                           context.Context
	cancel                        context.CancelFunc
	wg                            sync.WaitGroup
	closed                        bool
	startedAt, lastSave, lastTick time.Time
	alerts                        chan model.Notice
	fatal                         string
}

func New(store *storage.Store, c model.Config, f model.Features, factory steam.Factory, clock func() time.Time) *Engine {
	if clock == nil {
		clock = time.Now
	}
	ctx, cancel := context.WithCancel(context.Background())
	e := &Engine{Store: store, Config: c, Features: f, factory: factory, clock: clock, ctx: ctx, cancel: cancel, live: map[string]*live{}, alerts: make(chan model.Notice, 100), startedAt: clock(), lastSave: clock(), lastTick: clock()}
	for _, a := range c.Accounts {
		e.live[a.ID] = e.fresh()
	}
	return e
}
func (e *Engine) fresh() *live {
	return &live{status: "Остановлено", sent: []uint32{}, lastTick: e.clock()}
}
func (e *Engine) Alerts() <-chan model.Notice { return e.alerts }
func (e *Engine) spawn(fn func())             { e.wg.Add(1); go func() { defer e.wg.Done(); fn() }() }
func (e *Engine) log(id, msg string) {
	name := "Программа"
	for _, a := range e.Config.Accounts {
		if a.ID == id {
			name = a.Name
		}
	}
	e.Features.Logs = append(e.Features.Logs, model.Log{Time: e.clock().UnixMilli(), Account: name, Message: msg})
	if len(e.Features.Logs) > 500 {
		e.Features.Logs = e.Features.Logs[len(e.Features.Logs)-500:]
	}
}
func (e *Engine) alert(msg string) {
	n := model.Notice{Time: e.clock().UnixMilli(), Message: msg}
	select {
	case e.alerts <- n:
	default:
	}
}
func (e *Engine) save() error {
	err := e.Store.Save(e.Config, e.Features)
	if err == nil {
		e.lastSave = e.clock()
	}
	return err
}
func (e *Engine) Flush() error { e.mu.Lock(); defer e.mu.Unlock(); return e.save() }
func (e *Engine) account(id string) (*model.Account, *live, error) {
	for i := range e.Config.Accounts {
		if e.Config.Accounts[i].ID == id {
			return &e.Config.Accounts[i], e.live[id], nil
		}
	}
	return nil, nil, errors.New("Аккаунт не найден")
}
func (e *Engine) Add(name string) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return "", errors.New("Программа закрывается")
	}
	name = strings.TrimSpace(name)
	if !model.ValidName(name) {
		return "", errors.New("Введи логин Steam, а не имя профиля")
	}
	if len(e.Config.Accounts) >= 3 {
		return "", errors.New("Можно добавить до трёх аккаунтов")
	}
	for _, a := range e.Config.Accounts {
		if strings.EqualFold(a.Name, name) {
			return "", errors.New("Этот аккаунт уже добавлен")
		}
	}
	id := uuid.NewString()
	e.Config.Accounts = append(e.Config.Accounts, model.Account{ID: id, Name: name, AppIDs: []uint32{}, BatchSize: 32, RotationMinutes: 60})
	e.Features.Accounts[id] = model.NewData()
	e.live[id] = e.fresh()
	if err := e.save(); err != nil {
		e.Config.Accounts = e.Config.Accounts[:len(e.Config.Accounts)-1]
		delete(e.Features.Accounts, id)
		delete(e.live, id)
		return "", err
	}
	return id, nil
}
func (e *Engine) Update(id string, ids []uint32, batch, rotation int, auto bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	a, r, err := e.account(id)
	if err != nil {
		return err
	}
	next := *a
	next.AppIDs = ids
	next.BatchSize = batch
	next.RotationMinutes = rotation
	next.AutoStart = auto
	if err = next.Validate(); err != nil {
		return err
	}
	old := *a
	*a = next
	if err = e.save(); err != nil {
		*a = old
		return err
	}
	if !reflect.DeepEqual(old.AppIDs, ids) || old.BatchSize != batch || old.RotationMinutes != rotation {
		r.index = 0
		r.batchElapsed = 0
		e.clearLocked(id, r)
	}
	return nil
}
func (e *Engine) Start(id, password string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.start(id, password)
}
func (e *Engine) start(id, password string) error {
	if e.closed {
		return errors.New("Программа закрывается")
	}
	a, r, err := e.account(id)
	if err != nil {
		return err
	}
	if r.desired {
		return errors.New("Аккаунт уже запущен")
	}
	if password == "" && !e.Store.Has(id) {
		return errors.New("Для первого входа введи пароль Steam")
	}
	if r.closing {
		return errors.New("Завершается предыдущее соединение. Повтори через несколько секунд")
	}
	r.pendingAt = 0
	r.desired = true
	r.startedAt = e.clock().UnixMilli()
	r.workMS = 0
	r.breakUntil = 0
	r.failures = 0
	e.connect(*a, r, password)
	return nil
}
func (e *Engine) connect(a model.Account, r *live, password string) {
	var secret model.Secret
	var err error
	if password == "" {
		secret, err = e.Store.Token(a.ID)
		if err != nil {
			r.desired = false
			r.status = "Сессия недоступна: нужен повторный вход"
			return
		}
	}
	c, err := e.factory(a.ID)
	if err != nil {
		e.fail(a.ID, r, 0)
		return
	}
	r.generation++
	gen := r.generation
	ctx, cancel := context.WithCancel(e.ctx)
	r.client = c
	r.ctx = ctx
	r.cancel = cancel
	r.online = false
	r.blocked = false
	r.guard = nil
	r.retryAt = 0
	r.status = "Подключение…"
	r.lastTick = e.clock()
	e.spawn(func() {
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-c.Events():
				if !ok {
					return
				}
				e.mu.Lock()
				current := e.live[a.ID]
				if current == r && r.generation == gen && !e.closed {
					switch event.Kind {
					case "guard":
						r.guard = &Guard{Kind: event.GuardKind, Wrong: event.Wrong}
						r.status = "Нужен код Steam Guard"
						if event.GuardKind == "confirmation" {
							r.status = "Подтверди вход в приложении Steam"
						}
						if event.Wrong {
							r.guard.WaitUntil = e.clock().Add(30 * time.Second).UnixMilli()
							r.status = "Неверный код: дождись нового"
						}
					case "playing":
						e.playing(a.ID, r, event.Blocked)
					case "disconnected", "error":
						if r.online {
							e.fail(a.ID, r, event.Code)
						}
					}
				}
				e.mu.Unlock()
			}
		}
	})
	e.spawn(func() {
		loginCtx, stop := context.WithTimeout(ctx, 5*time.Minute)
		defer stop()
		result, err := c.Login(loginCtx, a.Name, password, secret)
		password = ""
		e.mu.Lock()
		defer e.mu.Unlock()
		if e.live[a.ID] != r || r.generation != gen || e.closed {
			return
		}
		if err != nil {
			e.fail(a.ID, r, steam.Code(err))
			return
		}
		if err = e.Store.SaveToken(a.ID, result); err != nil {
			e.log(a.ID, "Не удалось сохранить сессию в Windows")
			e.alert("Не удалось сохранить сессию " + a.Name)
		}
		r.online = true
		r.guard = nil
		r.readyAt = e.clock().Add(5 * time.Second).UnixMilli()
		r.stableAt = e.clock().Add(time.Minute).UnixMilli()
		r.status = "Проверка игровой сессии…"
		r.lastTick = e.clock()
		if c.State().Blocked {
			e.playing(a.ID, r, true)
		}
		e.log(a.ID, "Вход выполнен")
	})
}
func (e *Engine) detach(r *live) {
	c := r.client
	r.generation++
	r.client = nil
	r.online = false
	r.guard = nil
	r.sent = []uint32{}
	r.busy = false
	r.clearing = false
	r.libraryBusy = false
	if r.cancel != nil {
		r.cancel()
	}
	if c != nil {
		r.closing = true
		e.spawn(func() { _ = c.Close(); e.mu.Lock(); r.closing = false; e.mu.Unlock() })
	}
}
func Backoff(code, failures int) time.Duration {
	if failures < 1 {
		failures = 1
	}
	if failures > 5 {
		failures = 5
	}
	if code == 6 || code == 34 || code == 50 {
		n := 120 * (1 << min(failures-1, 3))
		return time.Duration(min(n, 900)) * time.Second
	}
	if code == 84 {
		return 300 * time.Second
	}
	return time.Duration(min(300, 30*(1<<(failures-1)))) * time.Second
}
func (e *Engine) fail(id string, r *live, code int) {
	e.detach(r)
	if !r.desired {
		return
	}
	conflict := code == 6 || code == 34 || code == 50
	temporary := code == 0 || code == 2 || code == 3 || code == 10 || code == 16 || code == 20 || code == 35 || code == 36 || code == 37 || code == 38 || code == 48 || code == 84
	if (conflict || temporary) && e.Store.Has(id) {
		r.failures++
		delay := Backoff(code, r.failures)
		r.retryAt = e.clock().Add(delay).UnixMilli()
		r.status = fmt.Sprintf("Нет связи: повтор через %d с", int(delay.Seconds()))
		if conflict {
			r.blocked = true
			r.status = fmt.Sprintf("Сессия занята: проверка через %d с", int(delay.Seconds()))
		}
		if code == 84 {
			r.status = "Steam ограничил частоту: повтор через 300 с"
		}
	} else {
		r.desired = false
		r.retryAt = 0
		r.status = "Вход отклонён: нужен пароль и Steam Guard"
		if code == 5 {
			r.status = "Неверный пароль или недействительный токен"
		}
		if code == 65 {
			r.status = "Токен истёк: нужен повторный вход"
		}
	}
	e.log(id, r.status)
	e.alert(r.status)
}
func (e *Engine) playing(id string, r *live, blocked bool) {
	if blocked {
		r.blocked = true
		r.blockedAt = e.clock().UnixMilli()
		r.status = "Пауза: игра на другом компьютере"
		e.clearLocked(id, r)
	} else if r.blocked {
		r.blocked = false
		r.blockedAt = 0
		r.readyAt = e.clock().Add(15 * time.Second).UnixMilli()
		r.status = "Игра закрыта, возобновление через 15 с"
	}
}
func (e *Engine) clearLocked(id string, r *live) {
	r.revision++
	had := len(r.sent) > 0 || r.busy
	r.sent = []uint32{}
	if !had || !r.online || r.client == nil || r.clearing {
		return
	}
	r.clearing = true
	c, gen, parent := r.client, r.generation, r.ctx
	e.spawn(func() {
		ctx, cancel := context.WithTimeout(parent, 30*time.Second)
		defer cancel()
		err := c.Clear(ctx)
		e.mu.Lock()
		defer e.mu.Unlock()
		if e.live[id] == r && r.generation == gen && !e.closed {
			r.clearing = false
			if err != nil {
				e.fail(id, r, steam.Code(err))
			}
		}
	})
}
func (e *Engine) stop(id string) error {
	_, r, err := e.account(id)
	if err != nil {
		return err
	}
	r.desired = false
	r.retryAt = 0
	r.pendingAt = 0
	r.blocked = false
	e.detach(r)
	r.status = "Остановлено"
	return nil
}
func (e *Engine) Stop(id string) error { e.mu.Lock(); defer e.mu.Unlock(); return e.stop(id) }
func (e *Engine) StopAll() {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, a := range e.Config.Accounts {
		_ = e.stop(a.ID)
	}
}
func (e *Engine) StartAll() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	var missing []string
	for _, a := range e.Config.Accounts {
		if !e.live[a.ID].desired {
			if err := e.start(a.ID, ""); err != nil {
				missing = append(missing, a.Name)
			}
		}
	}
	if len(missing) > 0 {
		return errors.New("Нужен вход: " + strings.Join(missing, ", "))
	}
	return nil
}
func (e *Engine) Startup() {
	e.mu.Lock()
	defer e.mu.Unlock()
	n := 0
	for _, a := range e.Config.Accounts {
		if a.AutoStart && e.Store.Has(a.ID) {
			r := e.live[a.ID]
			r.pendingAt = e.clock().Add(time.Duration(e.Features.Options.StartupDelay+n*10) * time.Second).UnixMilli()
			r.status = "Ожидание автозапуска / сети"
			n++
		}
	}
}
func (e *Engine) Guard(id, code string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	_, r, err := e.account(id)
	if err != nil {
		return err
	}
	if r.guard == nil || r.client == nil {
		return errors.New("Steam сейчас не запрашивает код")
	}
	if e.clock().UnixMilli() < r.guard.WaitUntil {
		return errors.New("Дождись нового кода Steam Guard")
	}
	code = strings.ToUpper(strings.TrimSpace(code))
	if len(code) != 5 || strings.Trim(code, "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789") != "" {
		return errors.New("Код Steam Guard: пять символов")
	}
	if err = r.client.SubmitGuard(code); err == nil {
		r.guard = nil
		r.status = "Проверка кода…"
	}
	return err
}
func (e *Engine) Next(id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	_, r, err := e.account(id)
	if err != nil {
		return err
	}
	r.index++
	r.batchElapsed = 0
	return nil
}
func (e *Engine) Tick() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed || e.fatal != "" {
		return
	}
	now := e.clock()
	ms := now.UnixMilli()
	o := e.Features.Options
	gap := now.Sub(e.lastTick)
	e.lastTick = now
	for i := range e.Config.Accounts {
		a := e.Config.Accounts[i]
		r := e.live[a.ID]
		d := e.Features.Accounts[a.ID]
		elapsed := now.Sub(r.lastTick)
		r.lastTick = now
		if r.pendingAt > 0 && ms >= r.pendingAt && !r.closing {
			_ = e.start(a.ID, "")
		}
		if gap >= 5*time.Second && r.desired && r.client != nil {
			e.fail(a.ID, r, 0)
			continue
		}
		if r.online && r.desired && !r.blocked && len(r.sent) > 0 && elapsed > 0 && elapsed < 5*time.Second {
			n := elapsed.Milliseconds()
			model.Accrue(d, now.Add(-elapsed), now, r.sent)
			r.activeMS += n
			r.gameMS += n * int64(len(r.sent))
			r.batchElapsed += n
			r.workMS += n
			if r.batchElapsed >= int64(a.RotationMinutes)*60000 {
				r.index++
				r.batchElapsed = 0
			}
		}
		if r.desired && o.StopAfter > 0 && ms-r.startedAt >= int64(o.StopAfter)*60000 {
			_ = e.stop(a.ID)
			e.log(a.ID, "Остановлено по таймеру")
			continue
		}
		if r.breakUntil > 0 && ms >= r.breakUntil {
			r.breakUntil = 0
			r.workMS = 0
		}
		if r.desired && o.BreakEvery > 0 && r.workMS >= int64(o.BreakEvery)*60000 && r.breakUntil == 0 {
			r.breakUntil = ms + int64(o.BreakMinutes)*60000
			e.log(a.ID, "Начался запланированный перерыв")
		}
		if r.desired && r.client == nil && !r.closing && r.retryAt > 0 && ms >= r.retryAt {
			e.connect(a, r, "")
		}
		if r.online && r.client != nil {
			state := r.client.State()
			if !state.Online {
				e.fail(a.ID, r, 0)
				continue
			}
			if state.Blocked && !r.blocked {
				e.playing(a.ID, r, true)
			}
			if r.stableAt > 0 && ms >= r.stableAt {
				r.failures = 0
				r.stableAt = 0
			}
		}
		if r.desired && r.online {
			if r.blocked {
				if ms-r.blockedAt > 300000 {
					e.fail(a.ID, r, 34)
				}
				continue
			}
			if r.breakUntil > ms || !model.InSchedule(now, o) {
				e.clearLocked(a.ID, r)
				r.status = "Пауза по расписанию"
				if r.breakUntil > ms {
					r.status = "Перерыв до " + time.UnixMilli(r.breakUntil).Format("15:04:05")
				}
				continue
			}
			if ms >= r.readyAt && !r.busy {
				e.apply(a, r)
			}
		}
		if r.online && o.LibraryHours > 0 && !r.libraryBusy && ms-d.LibraryAt >= int64(o.LibraryHours)*3600000 && ms-r.libraryAttempt >= 900000 {
			e.libraryLocked(a.ID, r)
		}
		e.goals(a.ID)
	}
	if now.Sub(e.lastSave) >= 30*time.Second {
		if err := e.save(); err != nil {
			e.fatal = "Не удалось сохранить данные. Проверь папку данных и перезапусти программу"
			for _, a := range e.Config.Accounts {
				_ = e.stop(a.ID)
			}
			e.alert(e.fatal)
		}
	}
}
func (e *Engine) apply(a model.Account, r *live) {
	if len(a.AppIDs) == 0 {
		e.clearLocked(a.ID, r)
		r.status = "В сети: выбери игры и сохрани"
		return
	}
	batches := gameBatches(a.AppIDs, a.BatchSize)
	count := len(batches)
	r.index %= count
	batch := batches[r.index]
	if reflect.DeepEqual(batch, r.sent) {
		r.status = "Работает"
		return
	}
	if r.clearing {
		return
	}
	c, ctx, gen, revision := r.client, r.ctx, r.generation, r.revision
	r.busy = true
	r.status = "Подтверждение партии Steam…"
	e.spawn(func() {
		pc, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		blocked, err := c.Play(pc, batch)
		e.mu.Lock()
		defer e.mu.Unlock()
		if e.live[a.ID] != r || gen != r.generation || e.closed {
			return
		}
		r.busy = false
		if revision != r.revision {
			return
		}
		if blocked || r.blocked {
			e.playing(a.ID, r, true)
			return
		}
		if err != nil {
			e.fail(a.ID, r, steam.Code(err))
			return
		}
		r.sent = batch
		r.status = "Работает"
		e.log(a.ID, fmt.Sprintf("Подтверждена партия %d/%d: %d игр", r.index+1, count, len(batch)))
	})
}

// gameBatches keeps Steam's 32-game maximum full whenever the account has at
// least that many games. The final, otherwise short batch is filled with games
// from the preceding batch. AppIDs are unique in an account, so this never
// sends a duplicate within one PlayGames request.
func gameBatches(ids []uint32, size int) [][]uint32 {
	if size < 1 || len(ids) == 0 {
		return [][]uint32{}
	}
	count := (len(ids) + size - 1) / size
	batches := make([][]uint32, 0, count)
	for start := 0; start < len(ids); start += size {
		batches = append(batches, append([]uint32{}, ids[start:min(start+size, len(ids))]...))
	}
	last := batches[len(batches)-1]
	if len(batches) > 1 && len(last) < size {
		previous := batches[len(batches)-2]
		last = append(last, previous[:size-len(last)]...)
		batches[len(batches)-1] = last
	}
	return batches
}
func (e *Engine) libraryLocked(id string, r *live) {
	r.libraryBusy = true
	r.libraryAttempt = e.clock().UnixMilli()
	c, ctx, gen := r.client, r.ctx, r.generation
	e.spawn(func() {
		lc, cancel := context.WithTimeout(ctx, 40*time.Second)
		defer cancel()
		games, err := c.Library(lc)
		e.mu.Lock()
		defer e.mu.Unlock()
		if e.live[id] != r || r.generation != gen || e.closed {
			return
		}
		r.libraryBusy = false
		if err != nil {
			e.log(id, "Не удалось обновить библиотеку Steam; сохранён кеш")
			return
		}
		d := e.Features.Accounts[id]
		d.Library = games
		d.LibraryAt = e.clock().UnixMilli()
		e.goals(id)
		if e.save() != nil {
			e.log(id, "Не удалось сохранить кеш библиотеки")
		}
	})
}
func (e *Engine) RefreshLibrary(id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	_, r, err := e.account(id)
	if err != nil {
		return err
	}
	if !r.online {
		return errors.New("Сначала войди в аккаунт")
	}
	if r.libraryBusy {
		return nil
	}
	if e.clock().UnixMilli()-r.libraryAttempt < 60000 {
		return errors.New("Обновление доступно раз в минуту")
	}
	e.libraryLocked(id, r)
	return nil
}
func (e *Engine) goals(id string) {
	d := e.Features.Accounts[id]
	for i := range d.Goals {
		g := &d.Goals[i]
		n := e.progress(d, *g)
		if !g.Notified && n != nil && *n >= g.Hours*60 {
			g.Notified = true
			msg := fmt.Sprintf("Цель %.2f ч для App %d достигнута (%s)", g.Hours, g.AppID, g.Basis)
			e.Features.Notifications = append(e.Features.Notifications, model.Notice{Time: e.clock().UnixMilli(), Message: msg})
			if len(e.Features.Notifications) > 100 {
				e.Features.Notifications = e.Features.Notifications[len(e.Features.Notifications)-100:]
			}
			e.alert(msg)
		}
	}
}
func (e *Engine) progress(d *model.AccountData, g model.Goal) *float64 {
	if g.Basis == "steam" {
		for _, game := range d.Library {
			if game.AppID == g.AppID {
				return game.Forever
			}
		}
		return nil
	}
	n := float64(d.Games[strconv.FormatUint(uint64(g.AppID), 10)]) / 60000
	return &n
}
func (e *Engine) Suspend() {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, a := range e.Config.Accounts {
		r := e.live[a.ID]
		if r.desired {
			e.fail(a.ID, r, 0)
		}
	}
	_ = e.save()
}
func (e *Engine) Close() error {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil
	}
	e.closed = true
	for _, a := range e.Config.Accounts {
		_ = e.stop(a.ID)
	}
	e.cancel()
	err := e.save()
	e.mu.Unlock()
	e.wg.Wait()
	return err
}
func (e *Engine) Snapshot() map[string]any {
	e.mu.Lock()
	defer e.mu.Unlock()
	accounts := []map[string]any{}
	for _, a := range e.Config.Accounts {
		r := e.live[a.ID]
		d := e.Features.Accounts[a.ID]
		b, _ := json.Marshal(a)
		item := map[string]any{}
		_ = json.Unmarshal(b, &item)
		batches := gameBatches(a.AppIDs, a.BatchSize)
		count := len(batches)
		queue := []map[string]any{}
		for i, batch := range batches {
			queue = append(queue, map[string]any{"index": i, "appids": batch})
		}
		goals := []model.Goal{}
		for _, g := range d.Goals {
			g.Minutes = e.progress(d, g)
			goals = append(goals, g)
		}
		stopAt := int64(0)
		if r.desired && e.Features.Options.StopAfter > 0 {
			stopAt = r.startedAt + int64(e.Features.Options.StopAfter)*60000
		}
		for key, v := range map[string]any{"hasToken": e.Store.Has(a.ID), "status": r.status, "online": r.online, "desired": r.desired, "blocked": r.blocked, "guard": r.guard, "activeMs": r.activeMS, "gameMs": r.gameMS, "current": r.sent, "batchIndex": r.index, "batchCount": count, "remainingMs": max(0, int64(a.RotationMinutes)*60000-r.batchElapsed), "library": d.Library, "data": d, "goals": goals, "nextBatches": queue, "pendingAt": r.pendingAt, "retryAt": r.retryAt, "failures": r.failures, "stopAt": stopAt} {
			item[key] = v
		}
		accounts = append(accounts, item)
	}
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return model.Clone(map[string]any{"version": "2.0.0", "autoLaunch": e.Config.AutoLaunch, "accounts": accounts, "logs": e.Features.Logs, "notifications": e.Features.Notifications, "options": e.Features.Options, "telegram": e.Features.Telegram, "presets": Builtins, "popular": Popular, "dataPath": e.Store.Dir, "fatal": e.fatal, "health": map[string]any{"uptimeMs": e.clock().Sub(e.startedAt).Milliseconds(), "heartbeat": e.lastTick.UnixMilli(), "network": true, "memoryMB": m.Sys / 1048576, "bytes": map[string]int{"httpReceived": 0, "httpSent": 0, "requests": 0}}})
}
func (e *Engine) Data() (model.Config, model.Features) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return model.Clone(e.Config), model.Clone(e.Features)
}
func (e *Engine) TelegramConfig() model.Telegram {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.Features.Telegram
}
func (e *Engine) SetTelegram(t model.Telegram) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	old := e.Features.Telegram
	e.Features.Telegram = t
	if err := e.save(); err != nil {
		e.Features.Telegram = old
		return err
	}
	return nil
}
func (e *Engine) UpdateTelegramProgress(offset int64, daily string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if offset > e.Features.Telegram.Offset {
		e.Features.Telegram.Offset = offset
	}
	if daily != "" {
		e.Features.Telegram.LastDaily = daily
	}
	return e.save()
}
func (e *Engine) DiagnosticLog(msg string) { e.mu.Lock(); defer e.mu.Unlock(); e.log("", msg) }
func (e *Engine) Remove(id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, _, err := e.account(id); err != nil {
		return err
	}
	_ = e.stop(id)
	oldC, oldF := model.Clone(e.Config), model.Clone(e.Features)
	list := []model.Account{}
	for _, a := range e.Config.Accounts {
		if a.ID != id {
			list = append(list, a)
		}
	}
	e.Config.Accounts = list
	delete(e.Features.Accounts, id)
	if err := e.save(); err != nil {
		e.Config = oldC
		e.Features = oldF
		return err
	}
	delete(e.live, id)
	return e.Store.Forget(id)
}
func (e *Engine) Forget(id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.stop(id); err != nil {
		return err
	}
	return e.Store.Forget(id)
}
func (e *Engine) Options(o model.Options) error {
	if err := o.Validate(); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	old := e.Features.Options
	e.Features.Options = o
	if err := e.save(); err != nil {
		e.Features.Options = old
		return err
	}
	return nil
}
func (e *Engine) AutoLaunch(v bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	old := e.Config.AutoLaunch
	e.Config.AutoLaunch = v
	if err := e.save(); err != nil {
		e.Config.AutoLaunch = old
		return err
	}
	return nil
}
func (e *Engine) FreeLicense(id string, ids []uint32) (steam.Grant, error) {
	e.mu.Lock()
	_, r, err := e.account(id)
	if err != nil {
		e.mu.Unlock()
		return steam.Grant{}, err
	}
	if !r.online || len(ids) == 0 || len(ids) > 32 {
		e.mu.Unlock()
		return steam.Grant{}, errors.New("Войди и выбери от 1 до 32 игр")
	}
	d := e.Features.Accounts[id]
	if d.LicenseAt > 0 && e.clock().UnixMilli()-d.LicenseAt < 3600000 {
		e.mu.Unlock()
		return steam.Grant{}, errors.New("Запрос лицензий доступен раз в час")
	}
	d.LicenseAt = e.clock().UnixMilli()
	if err = e.save(); err != nil {
		e.mu.Unlock()
		return steam.Grant{}, err
	}
	c, ctx := r.client, r.ctx
	e.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return c.FreeLicense(ctx, ids)
}
func (e *Engine) Restore(c model.Config, f model.Features, secrets map[string]model.Secret) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := model.Validate(&c, &f); err != nil {
		return err
	}
	for _, a := range e.Config.Accounts {
		_ = e.stop(a.ID)
	}
	if err := e.Store.Restore(c, f, secrets); err != nil {
		return err
	}
	e.Config = c
	e.Features = f
	e.live = map[string]*live{}
	for _, a := range c.Accounts {
		e.live[a.ID] = e.fresh()
	}
	return nil
}
