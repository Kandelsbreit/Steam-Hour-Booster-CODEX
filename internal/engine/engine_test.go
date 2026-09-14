package engine

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Kandelsbreit/Steam-Hour-Booster-CODEX/internal/model"
	"github.com/Kandelsbreit/Steam-Hour-Booster-CODEX/internal/steam"
	"github.com/Kandelsbreit/Steam-Hour-Booster-CODEX/internal/storage"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fake struct {
	mu                      sync.Mutex
	events                  chan steam.Event
	online, blocked, closed bool
	played                  [][]uint32
	clears                  int
	password                string
	secret                  model.Secret
	loginErr                error
	guard                   bool
	code                    chan string
	holdPlay                chan struct{}
}

func (f *fake) Login(ctx context.Context, name, password string, s model.Secret) (model.Secret, error) {
	f.mu.Lock()
	f.password = password
	f.secret = s
	err := f.loginErr
	guard := f.guard
	f.mu.Unlock()
	if guard {
		f.events <- steam.Event{Kind: "guard", GuardKind: "mobile", Wrong: true}
		select {
		case <-f.code:
		case <-ctx.Done():
			return s, ctx.Err()
		}
	}
	if err != nil {
		return s, err
	}
	f.mu.Lock()
	f.online = true
	f.mu.Unlock()
	return model.Secret{RefreshToken: "TEST_ONLY_TOKEN"}, nil
}
func (f *fake) SubmitGuard(code string) error {
	select {
	case f.code <- code:
		return nil
	default:
		return errors.New("busy")
	}
}
func (f *fake) Play(ctx context.Context, ids []uint32) (bool, error) {
	if f.holdPlay != nil {
		select {
		case <-f.holdPlay:
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.blocked {
		return true, nil
	}
	f.played = append(f.played, append([]uint32{}, ids...))
	return false, nil
}
func (f *fake) Clear(context.Context) error { f.mu.Lock(); f.clears++; f.mu.Unlock(); return nil }
func (f *fake) Library(context.Context) ([]model.Game, error) {
	return []model.Game{{AppID: 1, Name: "Test game"}}, nil
}
func (f *fake) FreeLicense(context.Context, []uint32) (steam.Grant, error) {
	return steam.Grant{Apps: []uint32{1}, Packages: []uint32{}}, nil
}
func (f *fake) State() steam.State {
	f.mu.Lock()
	defer f.mu.Unlock()
	return steam.State{Online: f.online, Blocked: f.blocked}
}
func (f *fake) Events() <-chan steam.Event { return f.events }
func (f *fake) Close() error {
	f.mu.Lock()
	f.online = false
	f.closed = true
	f.clears++
	f.mu.Unlock()
	return nil
}
func (f *fake) remote(v bool) {
	f.mu.Lock()
	f.blocked = v
	f.mu.Unlock()
	f.events <- steam.Event{Kind: "playing", Blocked: v}
}
func (f *fake) count() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.played) }

type fixture struct {
	e       *Engine
	id      string
	now     atomic.Int64
	clients []*fake
	mu      sync.Mutex
	prepare func(*fake)
}

func setup(t *testing.T) *fixture {
	t.Helper()
	s, c, d, err := storage.Open(t.TempDir(), storage.DPAPI{})
	if err != nil {
		t.Fatal(err)
	}
	d.Options.LibraryHours = 0
	f := &fixture{}
	f.now.Store(time.Date(2026, 9, 14, 12, 0, 0, 0, time.Local).UnixMilli())
	f.e = New(s, c, d, func(string) (steam.Client, error) {
		v := &fake{events: make(chan steam.Event, 32), code: make(chan string, 1)}
		if f.prepare != nil {
			f.prepare(v)
		}
		f.mu.Lock()
		f.clients = append(f.clients, v)
		f.mu.Unlock()
		return v, nil
	}, func() time.Time { return time.UnixMilli(f.now.Load()) })
	f.id, err = f.e.Add("test_account")
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]uint32, 65)
	for i := range ids {
		ids[i] = uint32(i + 1)
	}
	if err = f.e.Update(f.id, ids, 32, 1, true); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := f.e.Close(); err != nil {
			t.Error(err)
		}
	})
	return f
}
func eventually(t *testing.T, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition timed out")
}
func (f *fixture) tick(n int) {
	for i := 0; i < n; i++ {
		f.now.Add(1000)
		f.e.Tick()
		time.Sleep(time.Millisecond)
	}
}
func (f *fixture) read(fn func(*live) bool) bool {
	f.e.mu.Lock()
	defer f.e.mu.Unlock()
	return fn(f.e.live[f.id])
}
func (f *fixture) login(t *testing.T) *fake {
	t.Helper()
	if err := f.e.Start(f.id, "TEST_ONLY_PASSWORD"); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return f.read(func(r *live) bool { return r.online }) })
	f.tick(5)
	f.mu.Lock()
	c := f.clients[0]
	f.mu.Unlock()
	return c
}
func TestRotationAndStatistics(t *testing.T) {
	f := setup(t)
	c := f.login(t)
	eventually(t, func() bool { return c.count() == 1 })
	f.tick(60)
	eventually(t, func() bool { return c.count() == 2 })
	c.mu.Lock()
	second := append([]uint32{}, c.played[1]...)
	c.mu.Unlock()
	if len(second) != 32 || second[0] != 33 {
		t.Fatal(second)
	}
	if err := f.e.Next(f.id); err != nil {
		t.Fatal(err)
	}
	f.tick(1)
	eventually(t, func() bool { return c.count() == 3 })
	c.mu.Lock()
	third := c.played[2]
	c.mu.Unlock()
	if !reflect.DeepEqual(third, []uint32{65}) {
		t.Fatal(third)
	}
	_, d := f.e.Data()
	if d.Accounts[f.id].ActiveMS < 60000 {
		t.Fatal("active time missing")
	}
}
func TestRemotePauseResume(t *testing.T) {
	f := setup(t)
	c := f.login(t)
	eventually(t, func() bool { return c.count() == 1 })
	f.tick(1)
	c.remote(true)
	eventually(t, func() bool { return f.read(func(r *live) bool { return r.blocked && len(r.sent) == 0 }) })
	_, before := f.e.Data()
	f.tick(10)
	_, after := f.e.Data()
	if before.Accounts[f.id].ActiveMS != after.Accounts[f.id].ActiveMS || c.count() != 1 {
		t.Fatal("counted or played during remote session")
	}
	c.remote(false)
	eventually(t, func() bool { return f.read(func(r *live) bool { return !r.blocked }) })
	f.tick(14)
	if c.count() != 1 {
		t.Fatal("resumed too early")
	}
	f.tick(1)
	eventually(t, func() bool { return c.count() == 2 })
	if !f.read(func(r *live) bool { return r.desired }) {
		t.Fatal("intent lost")
	}
}
func TestInitiallyBlockedAndTokenReconnect(t *testing.T) {
	f := setup(t)
	f.prepare = func(c *fake) { c.blocked = true }
	c := f.login(t)
	if c.count() != 0 {
		t.Fatal("displaced remote session")
	}
	c.events <- steam.Event{Kind: "disconnected", Code: 34}
	eventually(t, func() bool { return f.read(func(r *live) bool { return r.retryAt > 0 && !r.closing }) })
	f.tick(119)
	f.mu.Lock()
	n := len(f.clients)
	f.mu.Unlock()
	if n != 1 {
		t.Fatal("aggressive reconnect")
	}
	f.tick(1)
	eventually(t, func() bool { return f.read(func(r *live) bool { return r.online }) })
	f.mu.Lock()
	second := f.clients[1]
	f.mu.Unlock()
	second.mu.Lock()
	password, token := second.password, second.secret.RefreshToken
	second.mu.Unlock()
	if password != "" || token != "TEST_ONLY_TOKEN" {
		t.Fatal("refresh login failed")
	}
	second.remote(false)
	eventually(t, func() bool { return f.read(func(r *live) bool { return !r.blocked }) })
	f.tick(15)
	eventually(t, func() bool { return second.count() == 1 })
}
func TestBackoff(t *testing.T) {
	for _, code := range []int{6, 34, 50} {
		for i, want := range []time.Duration{120, 240, 480, 900, 900} {
			if Backoff(code, i+1) != want*time.Second {
				t.Fatal(code, i)
			}
		}
	}
	if Backoff(0, 1) != 30*time.Second || Backoff(0, 5) != 300*time.Second || Backoff(84, 1) != 300*time.Second {
		t.Fatal("network retry")
	}
}
func TestStopCancelsRetryAndSecretsHidden(t *testing.T) {
	f := setup(t)
	c := f.login(t)
	c.events <- steam.Event{Kind: "disconnected", Code: 3}
	eventually(t, func() bool { return f.read(func(r *live) bool { return r.retryAt > 0 }) })
	_ = f.e.Stop(f.id)
	f.tick(40)
	f.mu.Lock()
	n := len(f.clients)
	f.mu.Unlock()
	if n != 1 {
		t.Fatal("reconnected after stop")
	}
	b, _ := json.Marshal(f.e.Snapshot())
	if strings.Contains(string(b), "TEST_ONLY") {
		t.Fatal("snapshot secret")
	}
}
func TestGuardWrongWaitAndShutdown(t *testing.T) {
	f := setup(t)
	f.prepare = func(c *fake) { c.guard = true }
	if err := f.e.Start(f.id, "TEST_ONLY_PASSWORD"); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return f.read(func(r *live) bool { return r.guard != nil }) })
	if f.e.Guard(f.id, "ABCDE") == nil {
		t.Fatal("wrong-code wait ignored")
	}
	f.tick(30)
	if err := f.e.Guard(f.id, "ABCDE"); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return f.read(func(r *live) bool { return r.online }) })
	if err := f.e.Close(); err != nil {
		t.Fatal(err)
	}
}
func TestSleepAndThreeIndependentAccounts(t *testing.T) {
	f := setup(t)
	_ = f.login(t)
	second, err := f.e.Add("second_account")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.e.Add("third_account")
	if _, err = f.e.Add("fourth"); err == nil {
		t.Fatal("account limit")
	}
	if err = f.e.Start(second, "test"); err != nil {
		t.Fatal(err)
	}
	f.now.Add(3600000)
	f.e.Tick()
	if !f.read(func(r *live) bool { return r.desired && !r.online && r.retryAt > 0 }) {
		t.Fatal("sleep recovery")
	}
	_, d := f.e.Data()
	if d.Accounts[f.id].ActiveMS > 1000 {
		t.Fatal("sleep counted")
	}
}
func TestScheduleBreakStopAndGoals(t *testing.T) {
	f := setup(t)
	o := model.Defaults()
	o.LibraryHours = 0
	o.BreakEvery = 1
	o.BreakMinutes = 1
	o.StopAfter = 3
	if err := f.e.Options(o); err != nil {
		t.Fatal(err)
	}
	if err := f.e.Edit(f.id, "goal-save", model.Preset{}, model.Game{}, model.Goal{AppID: 1, Hours: 0.01, Basis: "local"}); err != nil {
		t.Fatal(err)
	}
	c := f.login(t)
	eventually(t, func() bool { return c.count() == 1 })
	f.tick(61)
	if !f.read(func(r *live) bool { return r.breakUntil > 0 && len(r.sent) == 0 }) {
		t.Fatal("break not entered")
	}
	_, d := f.e.Data()
	if !d.Accounts[f.id].Goals[0].Notified {
		t.Fatal("goal not reached")
	}
	f.tick(60)
	eventually(t, func() bool { return c.count() == 2 })
	f.tick(60)
	if !f.read(func(r *live) bool { return !r.desired }) {
		t.Fatal("stop timer")
	}
}
func TestClearInvalidatesPendingPlay(t *testing.T) {
	f := setup(t)
	hold := make(chan struct{})
	f.prepare = func(c *fake) { c.holdPlay = hold }
	c := f.login(t)
	eventually(t, func() bool { return f.read(func(r *live) bool { return r.busy }) })
	if err := f.e.Update(f.id, []uint32{99}, 32, 1, false); err != nil {
		t.Fatal(err)
	}
	close(hold)
	eventually(t, func() bool { return f.read(func(r *live) bool { return !r.busy && !r.clearing }) })
	if !f.read(func(r *live) bool { return len(r.sent) == 0 }) {
		t.Fatal("stale play restored old batch")
	}
	f.tick(1)
	eventually(t, func() bool { return c.count() == 2 })
}
