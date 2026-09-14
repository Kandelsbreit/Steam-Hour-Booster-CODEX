package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Account struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	AppIDs          []uint32 `json:"appids"`
	BatchSize       int      `json:"batchSize"`
	RotationMinutes int      `json:"rotationMinutes"`
	AutoStart       bool     `json:"autoStart"`
}
type Config struct {
	Version    int       `json:"version"`
	AutoLaunch bool      `json:"autoLaunch"`
	Accounts   []Account `json:"accounts"`
}
type Game struct {
	AppID    uint32   `json:"appid"`
	Name     string   `json:"name"`
	Forever  *float64 `json:"playtime_forever,omitempty"`
	TwoWeeks *float64 `json:"playtime_2weeks"`
}
type Preset struct {
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	AppIDs []uint32 `json:"appids"`
}
type Goal struct {
	AppID    uint32   `json:"appid"`
	Hours    float64  `json:"hours"`
	Basis    string   `json:"basis"`
	Notified bool     `json:"notified"`
	Minutes  *float64 `json:"minutes,omitempty"`
}
type Totals struct {
	ActiveMS int64 `json:"activeMs"`
	GameMS   int64 `json:"gameMs"`
}
type AccountData struct {
	Library   []Game             `json:"library"`
	LibraryAt int64              `json:"libraryAt"`
	Custom    []Game             `json:"custom"`
	Presets   []Preset           `json:"presets"`
	Goals     []Goal             `json:"goals"`
	Days      map[string]*Totals `json:"days"`
	Games     map[string]int64   `json:"games"`
	ActiveMS  int64              `json:"activeMs"`
	GameMS    int64              `json:"gameMs"`
	LicenseAt int64              `json:"licenseAt"`
}
type Options struct {
	StartupDelay    int    `json:"startupDelay"`
	StartMinimized  bool   `json:"startMinimized"`
	LibraryHours    int    `json:"libraryHours"`
	ScheduleEnabled bool   `json:"scheduleEnabled"`
	ScheduleStart   string `json:"scheduleStart"`
	ScheduleEnd     string `json:"scheduleEnd"`
	ScheduleDays    []int  `json:"scheduleDays"`
	BreakEvery      int    `json:"breakEvery"`
	BreakMinutes    int    `json:"breakMinutes"`
	StopAfter       int    `json:"stopAfter"`
	TrafficMode     string `json:"trafficMode"`
}
type ProfileAccount struct {
	Name            string   `json:"name"`
	AppIDs          []uint32 `json:"appids"`
	BatchSize       int      `json:"batchSize"`
	RotationMinutes int      `json:"rotationMinutes"`
	AutoStart       bool     `json:"autoStart"`
}
type Profile struct {
	ID       string           `json:"id"`
	Name     string           `json:"name"`
	Accounts []ProfileAccount `json:"accounts"`
	Options  Options          `json:"options"`
}
type Telegram struct {
	Enabled   bool   `json:"enabled"`
	ChatID    string `json:"chatId"`
	DailyTime string `json:"dailyTime"`
	Daily     bool   `json:"daily"`
	Errors    bool   `json:"errors"`
	Offset    int64  `json:"offset"`
	LastDaily string `json:"lastDaily"`
}
type Log struct {
	Time    int64  `json:"time"`
	Account string `json:"account"`
	Message string `json:"message"`
}
type Notice struct {
	Time    int64  `json:"time"`
	Message string `json:"message"`
}
type Features struct {
	Version       int                     `json:"version"`
	Options       Options                 `json:"options"`
	Accounts      map[string]*AccountData `json:"accounts"`
	Logs          []Log                   `json:"logs"`
	Notifications []Notice                `json:"notifications"`
	Telegram      Telegram                `json:"telegram"`
	Profiles      []Profile               `json:"profiles"`
}
type Secret struct {
	RefreshToken string `json:"refreshToken"`
	AccessToken  string `json:"accessToken,omitempty"`
	GuardData    string `json:"guardData,omitempty"`
}

func Defaults() Options {
	return Options{StartupDelay: 60, LibraryHours: 24, ScheduleStart: "00:00", ScheduleEnd: "00:00", ScheduleDays: []int{0, 1, 2, 3, 4, 5, 6}, BreakMinutes: 10, TrafficMode: "normal"}
}
func NewFeatures() Features {
	return Features{Version: 1, Options: Defaults(), Accounts: map[string]*AccountData{}, Logs: []Log{}, Notifications: []Notice{}, Telegram: Telegram{DailyTime: "21:00", Daily: true, Errors: true}, Profiles: []Profile{}}
}
func NewData() *AccountData {
	return &AccountData{Library: []Game{}, Custom: []Game{}, Presets: []Preset{}, Goals: []Goal{}, Days: map[string]*Totals{}, Games: map[string]int64{}}
}
func Clone[T any](v T) T { b, _ := json.Marshal(v); var out T; _ = json.Unmarshal(b, &out); return out }

var idRE = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)
var nameRE = regexp.MustCompile(`^[a-zA-Z0-9_]{2,64}$`)
var timeRE = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

func ValidID(s string) bool   { return idRE.MatchString(s) }
func ValidName(s string) bool { return nameRE.MatchString(s) }
func ValidTime(s string) bool { return timeRE.MatchString(s) }
func ParseIDs(v any) ([]uint32, error) {
	var items []string
	switch x := v.(type) {
	case string:
		items = strings.FieldsFunc(x, func(r rune) bool { return r == ' ' || r == '\n' || r == '\r' || r == '\t' || r == ',' || r == ';' })
	case []uint32:
		for _, n := range x {
			items = append(items, strconv.FormatUint(uint64(n), 10))
		}
	case []any:
		for _, n := range x {
			items = append(items, fmt.Sprint(n))
		}
	case nil:
	default:
		return nil, errors.New("Неверный список AppID")
	}
	if len(items) > 10000 {
		return nil, errors.New("Максимум 10 000 игр")
	}
	out := []uint32{}
	seen := map[uint32]bool{}
	for _, s := range items {
		if s == "" || strings.Trim(s, "0123456789") != "" {
			return nil, errors.New("AppID должны быть целыми положительными числами")
		}
		n, err := strconv.ParseUint(s, 10, 32)
		if err != nil || n == 0 {
			return nil, errors.New("Неверный AppID")
		}
		if !seen[uint32(n)] {
			out = append(out, uint32(n))
			seen[uint32(n)] = true
		}
	}
	return out, nil
}
func (a Account) Validate() error {
	if !ValidID(a.ID) || !ValidName(a.Name) {
		return errors.New("Неверный аккаунт")
	}
	if a.BatchSize < 1 || a.BatchSize > 32 || a.RotationMinutes < 1 || a.RotationMinutes > 1440 {
		return errors.New("Размер партии: 1–32; ротация: 1–1440 минут")
	}
	_, err := ParseIDs(a.AppIDs)
	return err
}
func (o Options) Validate() error {
	if o.StartupDelay < 0 || o.StartupDelay > 3600 || o.LibraryHours < 0 || o.LibraryHours > 720 || o.BreakEvery < 0 || o.BreakEvery > 10080 || o.BreakMinutes < 1 || o.BreakMinutes > 1440 || o.StopAfter < 0 || o.StopAfter > 43200 || !ValidTime(o.ScheduleStart) || !ValidTime(o.ScheduleEnd) {
		return errors.New("Проверь интервалы и время автоматизации")
	}
	if o.TrafficMode != "normal" && o.TrafficMode != "low" {
		return errors.New("Неверный режим трафика")
	}
	for _, d := range o.ScheduleDays {
		if d < 0 || d > 6 {
			return errors.New("Неверный день недели")
		}
	}
	return nil
}
func Validate(c *Config, f *Features) error {
	if c.Version != 1 || f.Version != 1 || len(c.Accounts) > 3 {
		return errors.New("Неверный формат локальных данных")
	}
	if f.Options.TrafficMode == "" {
		f.Options.TrafficMode = "normal"
	}
	if err := f.Options.Validate(); err != nil {
		return err
	}
	ids, names := map[string]bool{}, map[string]bool{}
	if c.Accounts == nil {
		c.Accounts = []Account{}
	}
	if f.Accounts == nil {
		f.Accounts = map[string]*AccountData{}
	}
	if f.Logs == nil {
		f.Logs = []Log{}
	}
	if f.Notifications == nil {
		f.Notifications = []Notice{}
	}
	if f.Profiles == nil {
		f.Profiles = []Profile{}
	}
	if len(f.Profiles) > 30 {
		return errors.New("Слишком много профилей")
	}
	profileNames := map[string]bool{}
	for i := range f.Profiles {
		p := &f.Profiles[i]
		p.Name = strings.TrimSpace(p.Name)
		if !ValidID(p.ID) || p.Name == "" || len([]rune(p.Name)) > 80 || profileNames[strings.ToLower(p.Name)] || len(p.Accounts) > 3 {
			return errors.New("Повреждён профиль")
		}
		if p.Options.TrafficMode == "" {
			p.Options.TrafficMode = "normal"
		}
		if p.Options.Validate() != nil {
			return errors.New("Повреждены настройки профиля")
		}
		profileNames[strings.ToLower(p.Name)] = true
		seen := map[string]bool{}
		for _, a := range p.Accounts {
			if !ValidName(a.Name) || seen[strings.ToLower(a.Name)] || a.BatchSize < 1 || a.BatchSize > 32 || a.RotationMinutes < 1 || a.RotationMinutes > 1440 {
				return errors.New("Повреждён профиль аккаунта")
			}
			if _, err := ParseIDs(a.AppIDs); err != nil {
				return err
			}
			seen[strings.ToLower(a.Name)] = true
		}
	}
	for i := range c.Accounts {
		a := &c.Accounts[i]
		if err := a.Validate(); err != nil {
			return err
		}
		key := strings.ToLower(a.Name)
		if ids[a.ID] || names[key] {
			return errors.New("Повторяющийся аккаунт")
		}
		ids[a.ID] = true
		names[key] = true
		a.AppIDs, _ = ParseIDs(a.AppIDs)
		d := f.Accounts[a.ID]
		if d == nil {
			d = NewData()
			f.Accounts[a.ID] = d
		}
		if d.Days == nil {
			d.Days = map[string]*Totals{}
		}
		if d.Games == nil {
			d.Games = map[string]int64{}
		}
		if d.Library == nil {
			d.Library = []Game{}
		}
		if d.Custom == nil {
			d.Custom = []Game{}
		}
		if d.Goals == nil {
			d.Goals = []Goal{}
		}
		if d.Presets == nil {
			d.Presets = []Preset{}
		}
		if d.ActiveMS < 0 || d.GameMS < 0 || len(d.Library) > 10000 || len(d.Custom) > 10000 || len(d.Presets) > 100 || len(d.Goals) > 1000 {
			return errors.New("Повреждены данные аккаунта")
		}
		for _, g := range append(append([]Game{}, d.Library...), d.Custom...) {
			if g.AppID == 0 || g.Forever != nil && (*g.Forever < 0 || math.IsNaN(*g.Forever)) || g.TwoWeeks != nil && *g.TwoWeeks < 0 {
				return errors.New("Повреждена библиотека")
			}
		}
		for _, p := range d.Presets {
			if _, err := ParseIDs(p.AppIDs); err != nil {
				return err
			}
		}
		for _, g := range d.Goals {
			if g.AppID == 0 || g.Hours <= 0 || g.Hours > 1000000 || math.IsNaN(g.Hours) || math.IsInf(g.Hours, 0) || (g.Basis != "local" && g.Basis != "steam") {
				return errors.New("Неверная цель")
			}
		}
		for day, v := range d.Days {
			if _, err := time.Parse("2006-01-02", day); err != nil || v == nil || v.ActiveMS < 0 || v.GameMS < 0 {
				return errors.New("Повреждена история статистики")
			}
		}
		for app, ms := range d.Games {
			if _, err := ParseIDs(app); err != nil || ms < 0 {
				return errors.New("Повреждена статистика игры")
			}
		}
	}
	if !ValidTime(f.Telegram.DailyTime) || f.Telegram.Offset < 0 {
		return errors.New("Неверные настройки Telegram")
	}
	return nil
}
func InSchedule(now time.Time, o Options) bool {
	if !o.ScheduleEnabled {
		return true
	}
	toMin := func(s string) int { h, _ := strconv.Atoi(s[:2]); m, _ := strconv.Atoi(s[3:]); return h*60 + m }
	start, end := toMin(o.ScheduleStart), toMin(o.ScheduleEnd)
	m := now.Hour()*60 + now.Minute()
	day := int(now.Weekday())
	if start > end && m < end {
		day = (day + 6) % 7
	}
	allowed := false
	for _, d := range o.ScheduleDays {
		allowed = allowed || d == day
	}
	return allowed && (start == end || (start < end && m >= start && m < end) || (start > end && (m >= start || m < end)))
}
func Accrue(d *AccountData, from, to time.Time, ids []uint32) {
	for from.Before(to) {
		next := time.Date(from.Year(), from.Month(), from.Day()+1, 0, 0, 0, 0, from.Location())
		if next.After(to) {
			next = to
		}
		ms := next.Sub(from).Milliseconds()
		key := from.Format("2006-01-02")
		if d.Days[key] == nil {
			d.Days[key] = &Totals{}
		}
		d.Days[key].ActiveMS += ms
		d.Days[key].GameMS += ms * int64(len(ids))
		d.ActiveMS += ms
		d.GameMS += ms * int64(len(ids))
		for _, id := range ids {
			d.Games[strconv.FormatUint(uint64(id), 10)] += ms
		}
		from = next
	}
}
