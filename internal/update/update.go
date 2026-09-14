package update

import (
	"context"
	"errors"
	"fmt"
	"github.com/Kandelsbreit/Steam-Hour-Booster-CODEX/internal/netx"
	"strconv"
	"strings"
	"sync"
	"time"
)

const Current = "2.1.0"
const latestURL = "https://api.github.com/repos/Kandelsbreit/Steam-Hour-Booster-CODEX/releases/latest"

type State struct {
	Checking  bool   `json:"checking"`
	Available bool   `json:"available"`
	Version   string `json:"version"`
	URL       string `json:"url"`
	Status    string `json:"status"`
	CheckedAt int64  `json:"checkedAt"`
}

type Checker struct {
	mu      sync.Mutex
	state   State
	request func(context.Context, string, any, any) (int, error)
}

func New() *Checker {
	return &Checker{state: State{Status: "Обновления ещё не проверялись"}, request: netx.JSON}
}
func (c *Checker) Snapshot() State { c.mu.Lock(); defer c.mu.Unlock(); return c.state }
func (c *Checker) Check(ctx context.Context) (State, error) {
	c.mu.Lock()
	if c.state.Checking {
		s := c.state
		c.mu.Unlock()
		return s, errors.New("Проверка обновлений уже выполняется")
	}
	c.state.Checking = true
	c.state.Status = "Проверка GitHub…"
	c.mu.Unlock()
	var release struct {
		Tag        string `json:"tag_name"`
		URL        string `json:"html_url"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
	}
	status, err := c.request(ctx, latestURL, nil, &release)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state.Checking = false
	c.state.CheckedAt = time.Now().UnixMilli()
	if err != nil {
		c.state.Status = "Не удалось проверить обновления: " + safe(err.Error())
		return c.state, err
	}
	if status != 200 || release.Draft || release.Prerelease {
		err = fmt.Errorf("обновление недоступно (HTTP %d)", status)
		c.state.Status = err.Error()
		return c.state, err
	}
	version := strings.TrimPrefix(strings.TrimSpace(release.Tag), "v")
	c.state.Version = version
	c.state.URL = release.URL
	c.state.Available = newer(version, Current)
	if c.state.Available {
		c.state.Status = "Доступна версия " + version
	} else {
		c.state.Status = "Установлена актуальная версия " + Current
	}
	return c.state, nil
}
func safe(s string) string {
	if len(s) > 160 {
		return s[:160]
	}
	return s
}
func newer(a, b string) bool {
	a = strings.TrimPrefix(a, "v")
	b = strings.TrimPrefix(b, "v")
	pa, pb := parse(a), parse(b)
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return false
}
func parse(s string) [3]int {
	var out [3]int
	for i, p := range strings.Split(s, ".") {
		if i == 3 {
			break
		}
		out[i], _ = strconv.Atoi(p)
	}
	return out
}
