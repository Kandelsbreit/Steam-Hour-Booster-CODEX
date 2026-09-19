package steam

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Kandelsbreit/Steam-Hour-Booster-CODEX/internal/model"
	gosteam "github.com/w849688611/GoSteam"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Event struct {
	Kind      string
	Blocked   bool
	Code      int
	Wrong     bool
	GuardKind string
}
type State struct{ Online, Blocked bool }
type Grant struct {
	Apps     []uint32 `json:"apps"`
	Packages []uint32 `json:"packages"`
}
type Client interface {
	Login(context.Context, string, string, model.Secret) (model.Secret, error)
	SubmitGuard(string) error
	Play(context.Context, []uint32) (bool, error)
	Clear(context.Context) error
	Library(context.Context) ([]model.Game, error)
	FreeLicense(context.Context, []uint32) (Grant, error)
	State() State
	Events() <-chan Event
	Close() error
}
type Factory func(string) (Client, error)
type Error struct {
	Code    int
	Message string
}

var sensitiveErrorRE = regexp.MustCompile(`(?i)(access[_-]?token|refresh[_-]?token|password|guard[_-]?data|secret)([\s:=]+)[^\s&,;]+`)

func safeMessage(message string) string {
	message = sensitiveErrorRE.ReplaceAllString(message, "$1$2[скрыто]")
	if i := strings.Index(message, "?"); i >= 0 {
		message = message[:i] + "?[параметры скрыты]"
	}
	return strings.TrimSpace(message)
}

func (e *Error) Error() string { return e.Message }
func Code(err error) int {
	var s *Error
	if errors.As(err, &s) {
		return s.Code
	}
	return 0
}
func Safe(err error) error {
	if err == nil {
		return nil
	}
	var s *gosteam.SteamError
	if errors.As(err, &s) {
		return &Error{int(s.Result), fmt.Sprintf("Steam отклонил запрос (код %d)", s.Result)}
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	message := safeMessage(err.Error())
	if message == "" {
		message = "неизвестная ошибка подключения"
	}
	return &Error{0, "Steam: " + message}
}

type Adapter struct {
	client  *gosteam.Client
	ctx     context.Context
	cancel  context.CancelFunc
	events  chan Event
	guard   chan string
	wg      sync.WaitGroup
	mu      sync.Mutex
	op      sync.Mutex
	secret  model.Secret
	steamID string
	blocked atomic.Bool
	closed  sync.Once
}

func New(id string) (Client, error) {
	opts := gosteam.Options{ID: id, ConnectionTimeout: 15 * time.Second, JobTimeout: 25 * time.Second, EventBuffer: 256, LogOutput: io.Discard, Transport: gosteam.TransportAuto, WebSocketPort: 443}
	if proxy := strings.TrimSpace(os.Getenv("STEAM_PROXY")); proxy != "" {
		switch {
		case strings.HasPrefix(proxy, "http://"), strings.HasPrefix(proxy, "https://"):
			opts.Proxy = gosteam.ProxyOptions{Type: gosteam.ProxyHTTP, URL: proxy}
		case strings.HasPrefix(proxy, "socks5://"):
			opts.Proxy = gosteam.ProxyOptions{Type: gosteam.ProxySOCKS5, URL: proxy}
		}
	}
	c, e := gosteam.New(opts)
	if e != nil {
		return nil, Safe(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	a := &Adapter{client: c, ctx: ctx, cancel: cancel, events: make(chan Event, 256), guard: make(chan string, 1)}
	a.wg.Add(1)
	go a.pump()
	return a, nil
}
func (a *Adapter) emit(e Event) {
	select {
	case a.events <- e:
	case <-a.ctx.Done():
	}
}
func (a *Adapter) pump() {
	defer a.wg.Done()
	for {
		select {
		case <-a.ctx.Done():
			return
		case event, ok := <-a.client.Events():
			if !ok {
				return
			}
			switch event.Type {
			case gosteam.EventPlayingSession:
				s, ok := event.Data.(gosteam.PlayingSessionState)
				if ok {
					a.blocked.Store(s.Blocked)
					a.emit(Event{Kind: "playing", Blocked: s.Blocked})
				}
			case gosteam.EventLoggedOff:
				s, _ := event.Data.(gosteam.LoggedOffEvent)
				a.emit(Event{Kind: "disconnected", Code: int(s.Result)})
			case gosteam.EventDisconnected:
				a.emit(Event{Kind: "disconnected"})
			case gosteam.EventError:
				a.emit(Event{Kind: "error", Code: Code(Safe(event.Err))})
			}
		}
	}
}

type authenticator struct {
	a   *Adapter
	ctx context.Context
}

func (g *authenticator) code(kind string, wrong bool) (string, error) {
	g.a.emit(Event{Kind: "guard", GuardKind: kind, Wrong: wrong})
	select {
	case v := <-g.a.guard:
		return v, nil
	case <-g.ctx.Done():
		return "", g.ctx.Err()
	case <-g.a.ctx.Done():
		return "", context.Canceled
	}
}
func (g *authenticator) GetDeviceCode(w bool) (string, error)          { return g.code("mobile", w) }
func (g *authenticator) GetEmailCode(_ string, w bool) (string, error) { return g.code("email", w) }
func (g *authenticator) AcceptDeviceConfirmation() (bool, error) {
	g.a.emit(Event{Kind: "guard", GuardKind: "confirmation"})
	return true, nil
}
func (a *Adapter) SubmitGuard(s string) error {
	select {
	case a.guard <- s:
		return nil
	case <-a.ctx.Done():
		return context.Canceled
	default:
		return errors.New("Код уже отправлен")
	}
}
func (a *Adapter) Login(ctx context.Context, name, password string, secret model.Secret) (model.Secret, error) {
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(a.ctx, cancel)
	defer stop()
	defer cancel()
	result, e := a.client.ConnectAndLogin(ctx, gosteam.Credentials{Username: name, Password: password, RefreshToken: secret.RefreshToken, GuardData: secret.GuardData, Persistent: true, DeviceName: "Agnia Steam Hours", Authenticator: &authenticator{a, ctx}})
	password = ""
	if e != nil {
		return model.Secret{}, Safe(e)
	}
	if result.RefreshToken != "" {
		secret.RefreshToken = result.RefreshToken
	}
	if result.AccessToken != "" {
		secret.AccessToken = result.AccessToken
	}
	if result.NewGuardData != "" {
		secret.GuardData = result.NewGuardData
	}
	a.mu.Lock()
	a.secret = secret
	a.steamID = result.Session.SteamID.String()
	a.mu.Unlock()
	return secret, nil
}
func (a *Adapter) State() State {
	return State{Online: a.client.Session().State == gosteam.StateLoggedOn, Blocked: a.blocked.Load()}
}
func (a *Adapter) Events() <-chan Event { return a.events }
func (a *Adapter) Play(ctx context.Context, ids []uint32) (bool, error) {
	a.op.Lock()
	defer a.op.Unlock()
	if a.blocked.Load() {
		return true, nil
	}
	state, e := a.client.PlayGamesConfirmed(ctx, ids...)
	if state.Blocked {
		a.blocked.Store(true)
		return true, nil
	}
	return false, Safe(e)
}
func (a *Adapter) Clear(ctx context.Context) error {
	a.op.Lock()
	defer a.op.Unlock()
	return Safe(a.client.ClearGames(ctx))
}
func (a *Adapter) FreeLicense(ctx context.Context, ids []uint32) (Grant, error) {
	g, e := a.client.RequestFreeLicense(ctx, ids...)
	return Grant{Apps: append([]uint32{}, g.GrantedAppIDs...), Packages: append([]uint32{}, g.GrantedPackageIDs...)}, Safe(e)
}
func (a *Adapter) Close() error {
	a.closed.Do(func() {
		a.cancel()
		a.op.Lock()
		defer a.op.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if a.State().Online {
			_ = a.client.ClearGames(ctx)
			_ = a.client.LogOff(ctx)
		}
		_ = a.client.Close()
		a.wg.Wait()
		close(a.events)
	})
	return nil
}
func request(ctx context.Context, method, endpoint string, form url.Values, out any) error {
	var body io.Reader
	if method == "POST" {
		body = strings.NewReader(form.Encode())
	} else {
		endpoint += "?" + form.Encode()
	}
	req, e := http.NewRequestWithContext(ctx, method, endpoint, body)
	if e != nil {
		return errors.New("Неверный запрос Steam")
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	r, e := http.DefaultClient.Do(req)
	if e != nil {
		return errors.New("Не удалось связаться со Steam")
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return fmt.Errorf("Steam HTTP %d", r.StatusCode)
	}
	if e = json.NewDecoder(io.LimitReader(r.Body, 16<<20)).Decode(out); e != nil {
		return errors.New("Неверный ответ Steam")
	}
	return nil
}
func (a *Adapter) Library(ctx context.Context) ([]model.Game, error) {
	a.mu.Lock()
	secret, id := a.secret, a.steamID
	a.mu.Unlock()
	// Web API supplements the public GoSteam transport; no CM messages or custom protobufs.
	if secret.AccessToken == "" {
		var res struct {
			Response struct {
				Token string `json:"access_token"`
			} `json:"response"`
		}
		input, _ := json.Marshal(map[string]any{"refresh_token": secret.RefreshToken, "steamid": id, "renewal_type": 0})
		if e := request(ctx, "POST", "https://api.steampowered.com/IAuthenticationService/GenerateAccessTokenForApp/v1/", url.Values{"input_json": {string(input)}}, &res); e != nil {
			return nil, e
		}
		if res.Response.Token == "" {
			return nil, errors.New("Steam не предоставил доступ к библиотеке")
		}
		secret.AccessToken = res.Response.Token
		a.mu.Lock()
		a.secret.AccessToken = secret.AccessToken
		a.mu.Unlock()
	}
	var res struct {
		Response struct {
			Games []struct {
				AppID    uint32   `json:"appid"`
				Name     string   `json:"name"`
				Forever  float64  `json:"playtime_forever"`
				TwoWeeks *float64 `json:"playtime_2weeks"`
			} `json:"games"`
			Count *int `json:"game_count"`
		} `json:"response"`
	}
	e := request(ctx, "GET", "https://api.steampowered.com/IPlayerService/GetOwnedGames/v1/", url.Values{"access_token": {secret.AccessToken}, "steamid": {id}, "include_appinfo": {"true"}, "include_played_free_games": {"true"}, "skip_unvetted_apps": {"false"}}, &res)
	if e != nil {
		a.mu.Lock()
		a.secret.AccessToken = ""
		a.mu.Unlock()
		return nil, e
	}
	if res.Response.Count == nil {
		return nil, errors.New("Steam не предоставил библиотеку; сохранён прежний кеш")
	}
	games := []model.Game{}
	for _, g := range res.Response.Games {
		n := g.Forever
		if g.Name == "" {
			g.Name = fmt.Sprintf("App %d", g.AppID)
		}
		games = append(games, model.Game{AppID: g.AppID, Name: g.Name, Forever: &n, TwoWeeks: g.TwoWeeks})
	}
	sort.Slice(games, func(i, j int) bool { return games[i].Name < games[j].Name })
	return games, nil
}
