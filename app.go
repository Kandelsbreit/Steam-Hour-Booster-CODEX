package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Kandelsbreit/Steam-Hour-Booster-CODEX/internal/backup"
	"github.com/Kandelsbreit/Steam-Hour-Booster-CODEX/internal/engine"
	"github.com/Kandelsbreit/Steam-Hour-Booster-CODEX/internal/model"
	"github.com/Kandelsbreit/Steam-Hour-Booster-CODEX/internal/netx"
	"github.com/Kandelsbreit/Steam-Hour-Booster-CODEX/internal/platform"
	"github.com/Kandelsbreit/Steam-Hour-Booster-CODEX/internal/steam"
	"github.com/Kandelsbreit/Steam-Hour-Booster-CODEX/internal/storage"
	"github.com/Kandelsbreit/Steam-Hour-Booster-CODEX/internal/telegram"
	"github.com/Kandelsbreit/Steam-Hour-Booster-CODEX/internal/update"
	"github.com/energye/systray"
	wr "github.com/wailsapp/wails/v2/pkg/runtime"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

type App struct {
	ctx      context.Context
	dir      string
	test     bool
	e        *engine.Engine
	bot      *telegram.Bot
	updates  *update.Checker
	failure  string
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	commands sync.Mutex
	quit     atomic.Bool
	tray     atomic.Bool
	searchAt time.Time
}
type Response struct {
	OK     bool           `json:"ok"`
	Result any            `json:"result"`
	State  map[string]any `json:"state,omitempty"`
	Error  string         `json:"error,omitempty"`
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	s, c, f, err := storage.Open(a.dir, storage.DPAPI{})
	if err == nil && !a.test {
		err = s.BackupLegacy()
	}
	if err != nil {
		a.failure = "Не удалось открыть локальные данные. Исходные файлы сохранены. " + err.Error()
		return
	}
	factory := steam.New
	if a.test {
		factory = func(string) (steam.Client, error) {
			return nil, errors.New("Steam отключён в тестовом профиле")
		}
	}
	a.e = engine.New(s, c, f, factory, nil)
	a.e.SetNetworkProbe(platform.Online)
	a.bot = telegram.New(a.e)
	a.updates = update.New()
	if !a.test {
		if c.AutoLaunch {
			if err = platform.AutoLaunch(true); err != nil {
				a.e.DiagnosticLog("Не удалось обновить путь автозагрузки Windows")
			}
		}
		a.e.Startup()
		a.bot.Restart()
	}
	loop, cancel := context.WithCancel(ctx)
	a.cancel = cancel
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		timer := time.NewTicker(time.Second)
		defer timer.Stop()
		for {
			select {
			case <-loop.Done():
				return
			case n := <-a.e.Alerts():
				if !a.test {
					a.bot.Notify(n)
				}
				wr.EventsEmit(ctx, "app-error", n.Message)
			case <-timer.C:
				a.e.Tick()
				wr.EventsEmit(ctx, "snapshot", a.snapshot())
			}
		}
	}()
}
func (a *App) ready(ctx context.Context) {
	if a.failure != "" {
		_, _ = wr.MessageDialog(ctx, wr.MessageDialogOptions{Type: wr.ErrorDialog, Title: "Ошибка локальных данных", Message: a.failure})
		a.quit.Store(true)
		wr.Quit(ctx)
		return
	}
	_, f := a.e.Data()
	if !a.test {
		go systray.Run(func() {
			systray.SetTooltip("Agnia Steam Hours")
			systray.AddMenuItem("Открыть", "Показать окно").Click(a.show)
			systray.AddMenuItem("Остановить все", "Остановить аккаунты").Click(a.e.StopAll)
			systray.AddSeparator()
			systray.AddMenuItem("Выход", "Завершить программу").Click(func() { a.quit.Store(true); wr.Quit(a.ctx) })
			systray.SetOnDClick(func(systray.IMenu) { a.show() })
			systray.SetOnRClick(func(m systray.IMenu) { _ = m.ShowMenu() })
			a.tray.Store(true)
		}, func() { a.tray.Store(false) })
		if f.Options.StartMinimized {
			wr.WindowHide(a.ctx)
		}
		if f.Options.TrafficMode != "low" {
			go a.checkUpdate(false)
		}
	}
}
func (a *App) show() {
	if a.ctx != nil {
		wr.WindowShow(a.ctx)
		wr.WindowUnminimise(a.ctx)
	}
}
func (a *App) beforeClose(ctx context.Context) bool {
	if !a.quit.Load() && a.tray.Load() {
		wr.WindowHide(ctx)
		return true
	}
	return false
}
func (a *App) suspend() {
	if a.e != nil {
		a.e.Suspend()
	}
}
func (a *App) shutdown(context.Context) {
	a.quit.Store(true)
	if a.cancel != nil {
		a.cancel()
	}
	a.wg.Wait()
	if a.bot != nil {
		a.bot.Stop()
	}
	a.commands.Lock()
	defer a.commands.Unlock()
	if a.e != nil {
		if err := a.e.Close(); err != nil {
			platform.ErrorBox("Не удалось сохранить данные при выходе. Проверь доступ к папке: " + a.dir)
		}
	}
	if a.tray.Load() {
		systray.Quit()
	}
}
func (a *App) snapshot() map[string]any {
	s := a.e.Snapshot()
	s["telegram"] = a.bot.Snapshot()
	h := s["health"].(map[string]any)
	h["network"] = platform.Online()
	h["memoryMB"] = platform.MemoryMB()
	h["bytes"] = netx.Meter()
	if a.updates != nil {
		s["update"] = a.updates.Snapshot()
	}
	return s
}
func (a *App) checkUpdate(manual bool) (any, error) {
	if a.updates == nil {
		return nil, errors.New("Проверка обновлений пока недоступна")
	}
	ctx, cancel := context.WithTimeout(a.ctx, 15*time.Second)
	defer cancel()
	state, err := a.updates.Check(ctx)
	if a.ctx != nil {
		wr.EventsEmit(a.ctx, "snapshot", a.snapshot())
	}
	if err != nil {
		return nil, err
	}
	if state.Available && !manual && a.ctx != nil {
		wr.EventsEmit(a.ctx, "app-error", state.Status+". Открой «Настройки», чтобы перейти к релизу.")
	}
	return state.Status, nil
}
func decode(p map[string]any, out any) error {
	b, err := json.Marshal(p)
	if err != nil {
		return errors.New("Неверные параметры")
	}
	if json.Unmarshal(b, out) != nil {
		return errors.New("Неверные параметры")
	}
	return nil
}
func str(p map[string]any, k string) string {
	if p[k] == nil {
		return ""
	}
	return fmt.Sprint(p[k])
}
func boolean(p map[string]any, k string) bool { v, _ := p[k].(bool); return v }
func number(p map[string]any, k string) int   { n, _ := strconv.Atoi(str(p, k)); return n }
func (a *App) Command(command string, p map[string]any) Response {
	a.commands.Lock()
	defer a.commands.Unlock()
	if a.e == nil {
		return Response{Error: a.failure}
	}
	if a.quit.Load() {
		return Response{Error: "Программа закрывается"}
	}
	result, err := a.command(command, p)
	if err != nil {
		return Response{Error: err.Error(), State: a.snapshot()}
	}
	return Response{OK: true, Result: result, State: a.snapshot()}
}
func (a *App) command(command string, p map[string]any) (any, error) {
	id := str(p, "id")
	switch command {
	case "state":
		return nil, nil
	case "add":
		return a.e.Add(str(p, "name"))
	case "save":
		ids, err := model.ParseIDs(p["appids"])
		if err != nil {
			return nil, err
		}
		return nil, a.e.Update(id, ids, number(p, "batchSize"), number(p, "rotationMinutes"), boolean(p, "autoStart"))
	case "start":
		if a.test {
			return nil, errors.New("Steam отключён в тестовом профиле")
		}
		return nil, a.e.Start(id, str(p, "password"))
	case "stop":
		return nil, a.e.Stop(id)
	case "start-all":
		if a.test {
			return nil, errors.New("Steam отключён в тестовом профиле")
		}
		return nil, a.e.StartAll()
	case "stop-all":
		a.e.StopAll()
		return nil, nil
	case "guard":
		return nil, a.e.Guard(id, str(p, "code"))
	case "next":
		return nil, a.e.Next(id)
	case "library":
		return nil, a.e.RefreshLibrary(id)
	case "free-license":
		ids, err := model.ParseIDs(p["appids"])
		if err != nil {
			return nil, err
		}
		return a.e.FreeLicense(id, ids)
	case "search-games":
		if time.Since(a.searchAt) < 2*time.Second {
			return nil, errors.New("Подожди 2 секунды перед следующим поиском")
		}
		a.searchAt = time.Now()
		ctx, cancel := context.WithTimeout(a.ctx, 20*time.Second)
		defer cancel()
		return netx.Search(ctx, str(p, "query"))
	case "custom-add", "custom-remove", "game-remove", "popular", "preset-save", "preset-delete", "preset-apply", "goal-save", "goal-delete":
		ids, err := model.ParseIDs(p["appids"])
		if err != nil {
			return nil, err
		}
		var appid uint32
		if p["appid"] != nil {
			parsed, err := model.ParseIDs(str(p, "appid"))
			if err != nil || len(parsed) != 1 {
				return nil, errors.New("Неверный AppID")
			}
			appid = parsed[0]
		}
		hours, _ := strconv.ParseFloat(str(p, "hours"), 64)
		return nil, a.e.Edit(id, command, model.Preset{ID: str(p, "preset"), Name: str(p, "name"), AppIDs: ids}, model.Game{AppID: appid, Name: str(p, "name")}, model.Goal{AppID: appid, Hours: hours, Basis: str(p, "basis")})
	case "options":
		var o model.Options
		if err := decode(p, &o); err != nil {
			return nil, err
		}
		return nil, a.e.Options(o)
	case "profile-save":
		return nil, a.e.SaveProfile(str(p, "name"))
	case "profile-apply":
		return nil, a.e.ApplyProfile(str(p, "profile"))
	case "profile-delete":
		return nil, a.e.DeleteProfile(str(p, "profile"))
	case "check-update":
		if a.test {
			return nil, errors.New("Проверка GitHub отключена в тестовом профиле")
		}
		return a.checkUpdate(true)
	case "telegram":
		var c model.Telegram
		if err := decode(p, &c); err != nil {
			return nil, err
		}
		if a.test && c.Enabled {
			return nil, errors.New("Telegram отключён в тестовом профиле")
		}
		return nil, a.bot.Save(c, str(p, "token"))
	case "forget":
		return nil, a.e.Forget(id)
	case "remove":
		return nil, a.e.Remove(id)
	case "autolaunch":
		enabled := boolean(p, "enabled")
		if !a.test {
			if err := platform.AutoLaunch(enabled); err != nil {
				return nil, errors.New("Не удалось изменить автозагрузку Windows")
			}
		}
		return nil, a.e.AutoLaunch(enabled)
	case "export-backup":
		return a.exportBackup(p)
	case "restore-backup":
		return a.restoreBackup(p)
	case "export-diagnostics":
		path, err := wr.SaveFileDialog(a.ctx, wr.SaveDialogOptions{DefaultFilename: "Agnia-diagnostics.json", Filters: []wr.FileFilter{{DisplayName: "JSON", Pattern: "*.json"}}})
		if err != nil || path == "" {
			return nil, err
		}
		s := a.snapshot()
		data := map[string]any{"version": s["version"], "health": s["health"], "logs": s["logs"]}
		b, err := json.MarshalIndent(data, "", "  ")
		if err == nil {
			err = storage.Atomic(path, b)
		}
		return "Диагностика сохранена", err
	case "quit":
		a.quit.Store(true)
		go wr.Quit(a.ctx)
		return nil, nil
	}
	return nil, errors.New("Неизвестная команда")
}
func (a *App) exportBackup(p map[string]any) (any, error) {
	if err := a.e.Flush(); err != nil {
		return nil, err
	}
	c, f := a.e.Data()
	d := backup.Data{Version: 1, Config: c, Features: f, TokensIncluded: boolean(p, "includeTokens"), Tokens: map[string]string{}, Secrets: map[string]model.Secret{}}
	d.Features.Telegram.Enabled = false
	d.Features.Telegram.Offset = 0
	if d.TokensIncluded {
		for _, account := range c.Accounts {
			if a.e.Store.Has(account.ID) {
				s, err := a.e.Store.Token(account.ID)
				if err != nil {
					return nil, errors.New("Не удалось прочитать сохранённую сессию")
				}
				d.Tokens[account.ID] = s.RefreshToken
				d.Secrets[account.ID] = s
			}
		}
	}
	data, err := backup.Pack(d, str(p, "password"))
	if err != nil {
		return nil, err
	}
	path, err := wr.SaveFileDialog(a.ctx, wr.SaveDialogOptions{Title: "Сохранить резервную копию", DefaultFilename: "Agnia-backup.agnia", Filters: []wr.FileFilter{{DisplayName: "Резервная копия Agnia", Pattern: "*.agnia"}}})
	if err != nil || path == "" {
		return nil, err
	}
	return "Резервная копия сохранена", storage.Atomic(path, data)
}
func (a *App) restoreBackup(p map[string]any) (any, error) {
	path, err := wr.OpenFileDialog(a.ctx, wr.OpenDialogOptions{Title: "Восстановить резервную копию", Filters: []wr.FileFilter{{DisplayName: "Резервная копия Agnia", Pattern: "*.agnia"}}})
	if err != nil || path == "" {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Size() > backup.Limit {
		return nil, errors.New("Слишком большой файл")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	d, err := backup.Unpack(data, str(p, "password"))
	if err != nil {
		return nil, err
	}
	answer, err := wr.MessageDialog(a.ctx, wr.MessageDialogOptions{Type: wr.QuestionDialog, Title: "Восстановление", Message: "Остановить аккаунты и заменить настройки, пресеты и статистику данными копии? Исходные данные будут сохранены в папке before-restore.", Buttons: []string{"Восстановить", "Отмена"}, DefaultButton: "Отмена", CancelButton: "Отмена"})
	if err != nil || answer != "Восстановить" {
		return nil, err
	}
	old, _ := a.e.Data()
	secrets := map[string]model.Secret{}
	for _, account := range d.Config.Accounts {
		if d.TokensIncluded {
			s := d.Secrets[account.ID]
			if s.RefreshToken == "" {
				s.RefreshToken = d.Tokens[account.ID]
			}
			if s.RefreshToken != "" {
				secrets[account.ID] = s
			}
		} else {
			for _, o := range old.Accounts {
				if o.ID == account.ID && o.Name == account.Name && a.e.Store.Has(o.ID) {
					s, err := a.e.Store.Token(o.ID)
					if err != nil {
						return nil, err
					}
					secrets[o.ID] = s
				}
			}
		}
	}
	a.bot.Stop()
	d.Features.Telegram.Enabled = false
	d.Features.Telegram.Offset = 0
	if err = a.e.Restore(d.Config, d.Features, secrets); err != nil {
		a.bot.Restart()
		return nil, err
	}
	if !a.test {
		if err = platform.AutoLaunch(d.Config.AutoLaunch); err != nil {
			a.e.DiagnosticLog("Проверь автозагрузку Windows после восстановления")
		}
	}
	return "Резервная копия восстановлена. Аккаунты остановлены; для запуска нажми «Запустить все».", nil
}
