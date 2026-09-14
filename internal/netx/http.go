package netx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Kandelsbreit/Steam-Hour-Booster-CODEX/internal/model"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

var sent, received, requests atomic.Int64
var Client = &http.Client{Timeout: 60 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect rejected") }}

func Meter() map[string]int64 {
	return map[string]int64{"httpSent": sent.Load(), "httpReceived": received.Load(), "requests": requests.Load()}
}
func JSON(ctx context.Context, address string, body any, out any) (int, error) {
	var b []byte
	var err error
	method := "GET"
	if body != nil {
		b, err = json.Marshal(body)
		if err != nil {
			return 0, errors.New("Неверный запрос")
		}
		method = "POST"
	}
	req, err := http.NewRequestWithContext(ctx, method, address, bytes.NewReader(b))
	if err != nil {
		return 0, errors.New("Неверный адрес сервиса")
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	requests.Add(1)
	sent.Add(int64(len(b)))
	res, err := Client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		return 0, errors.New("Сетевая ошибка: проверь интернет, VPN и доступ к сервису")
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 2<<20+1))
	received.Add(int64(len(data)))
	if err != nil || len(data) > 2<<20 {
		return res.StatusCode, errors.New("Не удалось прочитать ответ сервиса")
	}
	if err = json.Unmarshal(data, out); err != nil {
		return res.StatusCode, fmt.Errorf("Сервис вернул некорректный ответ (HTTP %d)", res.StatusCode)
	}
	return res.StatusCode, nil
}
func Search(ctx context.Context, query string) ([]model.Game, error) {
	query = strings.TrimSpace(query)
	if len([]rune(query)) < 2 || len([]rune(query)) > 100 {
		return nil, errors.New("Для поиска введи от 2 до 100 символов")
	}
	var result struct {
		Items []struct {
			ID   uint32 `json:"id"`
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"items"`
	}
	status, err := JSON(ctx, "https://store.steampowered.com/api/storesearch/?l=russian&cc=US&term="+url.QueryEscape(query), nil, &result)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, fmt.Errorf("Поиск недоступен (HTTP %d)", status)
	}
	out := []model.Game{}
	for _, g := range result.Items {
		if g.ID > 0 && g.Type == "app" {
			out = append(out, model.Game{AppID: g.ID, Name: g.Name})
			if len(out) == 40 {
				break
			}
		}
	}
	return out, nil
}
