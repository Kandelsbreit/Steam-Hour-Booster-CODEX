package telegram

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Kandelsbreit/Steam-Hour-Booster-CODEX/internal/model"
)

const maxPart = 3800

// esc makes dynamic text safe inside Telegram HTML messages.
func esc(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}

func hours(minutes float64) string {
	return strconv.FormatFloat(minutes/60, 'f', 1, 64)
}

func shortAgo(ms int64, now time.Time) string {
	if ms <= 0 {
		return ""
	}
	d := now.Sub(time.UnixMilli(ms))
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return "только что"
	case d < time.Hour:
		return fmt.Sprintf("%d мин назад", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d ч назад", int(d.Hours()))
	default:
		return fmt.Sprintf("%d дн назад", int(d.Hours()/24))
	}
}

func relTime(ms int64, now time.Time) string {
	if ms <= 0 {
		return ""
	}
	d := time.Duration(ms - now.UnixMilli())
	if d <= 0 {
		return "сейчас"
	}
	if d < time.Minute {
		return fmt.Sprintf("через %d с", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("через %d мин", int(d.Minutes()))
	}
	return fmt.Sprintf("через %d ч %02d мин", int(d.Hours()), int(d.Minutes())%60)
}

// splitMessage breaks long text into Telegram-sized parts on line boundaries.
func splitMessage(text string) []string {
	runes := []rune(text)
	if len(runes) <= maxPart {
		if len(runes) == 0 {
			return []string{"Пустой ответ"}
		}
		return []string{text}
	}
	parts := []string{}
	current := ""
	for _, line := range strings.SplitAfter(text, "\n") {
		if len([]rune(current))+len([]rune(line)) > maxPart && current != "" {
			parts = append(parts, strings.TrimRight(current, "\n"))
			current = ""
		}
		for len([]rune(line)) > maxPart {
			r := []rune(line)
			parts = append(parts, string(r[:maxPart]))
			line = string(r[maxPart:])
		}
		current += line
	}
	if strings.TrimSpace(current) != "" {
		parts = append(parts, strings.TrimRight(current, "\n"))
	}
	if len(parts) == 0 {
		return []string{"Пустой ответ"}
	}
	return parts
}

func section(title string) string {
	return "<b>" + esc(title) + "</b>\n"
}

func statusLine(s string) string {
	low := strings.ToLower(s)
	switch {
	case strings.Contains(low, "работает"):
		return "✅ " + esc(s)
	case strings.Contains(low, "пауза") || strings.Contains(low, "ожидан") || strings.Contains(low, "повтор") || strings.Contains(low, "проверк"):
		return "⏸ " + esc(s)
	case strings.Contains(low, "остановлено"):
		return "⛔️ " + esc(s)
	default:
		return "• " + esc(s)
	}
}

func libraryStamp(at int64, now time.Time) string {
	if at <= 0 {
		return "данные Steam ещё не загружались"
	}
	return "данные Steam от " + time.UnixMilli(at).Format("02.01 15:04") + " (" + shortAgo(at, now) + ")"
}

type gameHours struct {
	id    uint32
	name  string
	hours float64
	boost bool
}

// libraryTop returns games with two-week playtime, most played first.
func libraryTop(data *model.AccountData, playing map[float64]bool) (float64, []gameHours) {
	total := 0.0
	out := []gameHours{}
	for _, g := range data.Library {
		if g.TwoWeeks == nil || *g.TwoWeeks <= 0 {
			continue
		}
		total += *g.TwoWeeks
		out = append(out, gameHours{id: g.AppID, name: g.Name, hours: *g.TwoWeeks, boost: playing[float64(g.AppID)]})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].hours != out[j].hours {
			return out[i].hours > out[j].hours
		}
		return out[i].name < out[j].name
	})
	return total, out
}

// formatStatus renders one account for /status. snap comes from the engine
// snapshot; data holds the cached Steam library. TwoWeeks is the same
// "playtime in the last two weeks" Steam shows on the profile.
func formatStatus(snap map[string]any, data *model.AccountData, now time.Time) string {
	var b strings.Builder
	name, _ := snap["name"].(string)
	status, _ := snap["status"].(string)
	b.WriteString(section(name))
	b.WriteString(statusLine(status))
	b.WriteString("\n")

	current, _ := snap["current"].([]any)
	playing := map[float64]bool{}
	for _, v := range current {
		if id, ok := toFloat(v); ok {
			playing[id] = true
		}
	}
	total, top := libraryTop(data, playing)
	if len(top) > 0 {
		b.WriteString("\n<b>Steam за 2 недели: " + esc(hours(total)) + " ч</b>\n")
		b.WriteString(esc(libraryStamp(data.LibraryAt, now)) + "\n")
		b.WriteString("<i>Steam обновляет часы с задержкой; для свежих чисел — /refresh</i>\n")
		for i, g := range top {
			if i == 8 {
				break
			}
			line := fmt.Sprintf("%d. %s — %s ч", i+1, esc(g.name), esc(hours(g.hours)))
			if g.boost {
				line += " ▶️"
			}
			b.WriteString(line + "\n")
		}
		if len(top) > 8 {
			b.WriteString(fmt.Sprintf("…и ещё %d игр с часами\n", len(top)-8))
		}
	} else {
		b.WriteString("\n<b>Steam за 2 недели: нет данных</b>\n")
		b.WriteString("Библиотека Steam ещё не загружалась. Запусти буст и подожди пару минут, нажми «Обновить из Steam» в программе или отправь /refresh.\n")
	}
	if len(playing) > 0 {
		list := []string{}
		for _, v := range current {
			if id, ok := toFloat(v); ok {
				n := ""
				for _, g := range top {
					if g.id == uint32(id) {
						n = g.name
						break
					}
				}
				if n == "" {
					for _, g := range data.Library {
						if g.AppID == uint32(id) {
							n = g.Name
							break
						}
					}
				}
				if n == "" {
					n = "App " + strconv.Itoa(int(id))
				}
				list = append(list, esc(n))
			}
		}
		b.WriteString("В бусте сейчас: " + strings.Join(list, ", ") + "\n")
	}
	active, _ := toFloat(snap["activeMs"])
	game, _ := toFloat(snap["gameMs"])
	b.WriteString(fmt.Sprintf("\n<i>Локально за сессию: подключение %s, игровой счёт %s ч</i>\n",
		esc(duration(time.Duration(active))), esc(hours(game/60000))))
	return b.String()
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case uint32:
		return float64(x), true
	}
	return 0, false
}

func duration(d time.Duration) string {
	s := int(d.Seconds())
	if s < 0 {
		s = 0
	}
	return fmt.Sprintf("%d:%02d:%02d", s/3600, (s/60)%60, s%60)
}

func formatDiagnostics(s map[string]any) string {
	var b strings.Builder
	b.WriteString(section("Диагностика"))
	version, _ := s["version"].(string)
	b.WriteString("Версия: <code>" + esc(version) + "</code>\n")
	if h, ok := s["health"].(map[string]any); ok {
		uptime, _ := toFloat(h["uptimeMs"])
		mem, _ := toFloat(h["memoryMB"])
		net, _ := h["network"].(bool)
		b.WriteString(fmt.Sprintf("Аптайм: <code>%s</code> · память: <code>%d МБ</code>\n",
			esc(duration(time.Duration(uptime))), int(mem)))
		b.WriteString("Сеть: " + map[bool]string{true: "доступна", false: "недоступна"}[net] + "\n")
		if bytes, ok := h["bytes"].(map[string]any); ok {
			in, _ := toFloat(bytes["httpReceived"])
			out, _ := toFloat(bytes["httpSent"])
			req, _ := toFloat(bytes["requests"])
			b.WriteString(fmt.Sprintf("HTTP за запуск: ↑%.1f КБ ↓%.1f КБ · запросов %d\n", out/1024, in/1024, int(req)))
		}
	}
	b.WriteString("Данные: <code>" + esc(fmt.Sprint(s["dataPath"])) + "</code>\n")
	if fatal, _ := s["fatal"].(string); fatal != "" {
		b.WriteString("⚠️ " + esc(fatal) + "\n")
	}
	if accounts, ok := s["accounts"].([]any); ok {
		now := time.Now()
		b.WriteString("\n<b>Аккаунты</b>\n")
		for _, v := range accounts {
			a, ok := v.(map[string]any)
			if !ok {
				continue
			}
			name, _ := a["name"].(string)
			status, _ := a["status"].(string)
			failures, _ := toFloat(a["failures"])
			retryAt, _ := toFloat(a["retryAt"])
			line := esc(name) + ": " + statusLine(status)
			if int(failures) > 0 {
				line += fmt.Sprintf(" · ошибок: %d", int(failures))
			}
			if retryAt > 0 {
				line += " · " + relTime(int64(retryAt), now)
			}
			b.WriteString(line + "\n")
		}
	}
	return b.String()
}

type progresser interface {
	GoalProgress(id string, g model.Goal) *float64
}

func formatGoals(c model.Config, f model.Features, id string, e progresser, now time.Time) string {
	var b strings.Builder
	b.WriteString(section("Цели"))
	found := false
	for _, a := range c.Accounts {
		if id != "" && a.ID != id {
			continue
		}
		goals := f.Accounts[a.ID].Goals
		if len(goals) == 0 {
			continue
		}
		found = true
		b.WriteString("\n" + esc(a.Name) + "\n")
		for _, g := range goals {
			minutes := g.Minutes
			if minutes == nil && e != nil {
				minutes = e.GoalProgress(a.ID, g)
			}
			game := fmt.Sprintf("App %d", g.AppID)
			for _, lib := range f.Accounts[a.ID].Library {
				if lib.AppID == g.AppID && lib.Name != "" {
					game = lib.Name
					break
				}
			}
			line := fmt.Sprintf("• %s: цель %s ч (%s)", esc(game), esc(trimNum(g.Hours)), map[string]string{"steam": "Steam", "local": "локально"}[g.Basis])
			if minutes == nil {
				line += " — нет данных"
				if g.Basis == "steam" {
					line += " (обнови библиотеку)"
				}
			} else {
				percent := 0.0
				if g.Hours > 0 {
					percent = (*minutes / 60) / g.Hours * 100
				}
				if percent > 100 {
					percent = 100
				}
				line += fmt.Sprintf(" — %s ч (%.0f%%)", esc(hours(*minutes)), percent)
				if g.Notified {
					line += " ✅"
				}
			}
			b.WriteString(line + "\n")
		}
	}
	if !found {
		return "Целей нет. Добавь: /goal логин AppID часы [local|steam]"
	}
	return b.String()
}

func trimNum(v float64) string {
	s := strconv.FormatFloat(v, 'f', 2, 64)
	s = strings.TrimRight(s, "0")
	return strings.TrimRight(s, ".")
}

func formatHoursDiff(diff float64) string {
	if diff > 0.001 {
		return " (+" + hours(diff) + " ч)"
	}
	return ""
}

// formatAccountHoursOnly renders: "account - total hours (2 weeks) - diff"
func formatAccountHoursOnly(name string, d *model.AccountData, updateRecord bool, saveFn func(map[string]float64)) string {
	if d == nil || len(d.Library) == 0 {
		return esc(name) + " — нет данных"
	}

	totalMinutes := 0.0
	for _, g := range d.Library {
		if g.TwoWeeks != nil && *g.TwoWeeks > 0 {
			totalMinutes += *g.TwoWeeks
		}
	}

	prevTotal := 0.0
	hasPrev := false
	if d.LastTwoWeeksPlaytime != nil {
		if v, ok := d.LastTwoWeeksPlaytime["__total__"]; ok {
			prevTotal = v
			hasPrev = true
		} else {
			for _, v := range d.LastTwoWeeksPlaytime {
				prevTotal += v
			}
			if len(d.LastTwoWeeksPlaytime) > 0 {
				hasPrev = true
			}
		}
	}

	diffStr := ""
	if hasPrev {
		diff := totalMinutes - prevTotal
		if diff > 0.001 {
			diffStr = " (+" + hours(diff) + " ч)"
		} else if diff < -0.001 {
			diffStr = " (-" + hours(-diff) + " ч)"
		} else {
			diffStr = " (+0.0 ч)"
		}
	} else {
		diffStr = " (первый замер)"
	}

	if updateRecord && saveFn != nil {
		saveFn(map[string]float64{"__total__": totalMinutes})
	}

	return esc(name) + " — " + hours(totalMinutes) + " ч" + diffStr
}

func formatWelcome() string {
	var b strings.Builder
	b.WriteString(section("Steam Hours Booster"))
	b.WriteString("Бот управляет бустом часов и присылает сводки. Команды принимаются только от владельца чата в личных сообщениях.\n\n")
	b.WriteString(helpText())
	return b.String()
}

func helpText() string {
	return "<b>Команды</b>\n" +
		"/hours — текущие часы за 2 недели со всех аккаунтов и прибавка\n" +
		"/status [логин|all] — состояние и часы Steam за 2 недели\n" +
		"/refresh [логин|all] — обновить библиотеку Steam сейчас\n" +
		"/pause [логин|all] — остановить буст\n" +
		"/resume [логин|all] — возобновить по сохранённой сессии\n" +
		"/next [логин|all] — следующая партия\n" +
		"/goals [логин] — цели и прогресс\n" +
		"/goal логин AppID часы [local|steam] — задать цель\n" +
		"/mute N | /unmute — тихий режим уведомлений\n" +
		"/diagnostics — состояние программы\n" +
		"/help — эта справка\n\n" +
		"<i>Пароли и коды Steam Guard вводи только в программе.</i>"
}
