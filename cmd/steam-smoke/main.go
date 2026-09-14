package main

import (
	"bufio"
	"context"
	"fmt"
	"github.com/Kandelsbreit/Steam-Hour-Booster-CODEX/internal/model"
	"github.com/Kandelsbreit/Steam-Hour-Booster-CODEX/internal/steam"
	"golang.org/x/term"
	"os"
	"strings"
	"time"
)

func main() {
	if e := run(); e != nil {
		fmt.Println("Проверка не завершена:", e)
		os.Exit(1)
	}
}
func run() error {
	reader := bufio.NewReader(os.Stdin)
	fmt.Print("Логин Steam: ")
	name, _ := reader.ReadString('\n')
	fmt.Print("Пароль (не сохраняется): ")
	pass, e := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if e != nil {
		return e
	}
	fmt.Print("Тестовые AppID через запятую: ")
	line, _ := reader.ReadString('\n')
	ids, e := model.ParseIDs(line)
	if e != nil || len(ids) == 0 {
		return fmt.Errorf("нужен AppID")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	c, e := steam.New("live-smoke")
	if e != nil {
		return e
	}
	defer c.Close()
	done := make(chan struct{})
	go func(c steam.Client) {
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-c.Events():
				if !ok {
					return
				}
				if ev.Kind == "guard" {
					fmt.Println("Steam Guard:", ev.GuardKind)
					if ev.GuardKind != "confirmation" {
						code, _ := reader.ReadString('\n')
						_ = c.SubmitGuard(strings.TrimSpace(code))
					}
				}
			}
		}
	}(c)
	secret, e := c.Login(ctx, strings.TrimSpace(name), string(pass), model.Secret{})
	clear(pass)
	if e != nil {
		return e
	}
	fmt.Println("Вход выполнен; refresh token получен:", secret.RefreshToken != "")
	_ = c.Close()
	<-done
	c, e = steam.New("live-smoke-reconnect")
	if e != nil {
		return e
	}
	defer c.Close()
	if _, e = c.Login(ctx, strings.TrimSpace(name), "", secret); e != nil {
		return e
	}
	secret = model.Secret{}
	fmt.Println("Повторный вход по токену выполнен")
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
	}
	if c.State().Blocked {
		return fmt.Errorf("игра на другом ПК: отправка отменена")
	}
	pc, stop := context.WithTimeout(ctx, 20*time.Second)
	blocked, e := c.Play(pc, ids)
	stop()
	if e != nil {
		return e
	}
	if blocked {
		return fmt.Errorf("игровая сессия занята")
	}
	fmt.Println("Партия подтверждена Steam")
	if e = c.Clear(ctx); e != nil {
		return e
	}
	fmt.Println("ClearGames выполнен; отключение")
	return nil
}
