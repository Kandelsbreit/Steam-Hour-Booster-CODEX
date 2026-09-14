package engine

import (
	_ "embed"
	"encoding/json"
	"errors"
	"github.com/Kandelsbreit/Steam-Hour-Booster-CODEX/internal/model"
	"github.com/google/uuid"
	"strings"
)

//go:embed catalog.json
var catalog []byte
var Popular []model.Game
var Builtins []model.Preset

func init() {
	var c struct {
		Popular []model.Game   `json:"popular"`
		Presets []model.Preset `json:"presets"`
	}
	if err := json.Unmarshal(catalog, &c); err != nil {
		panic("invalid embedded catalog")
	}
	Popular = c.Popular
	Builtins = c.Presets
}
func (e *Engine) Edit(id, command string, p model.Preset, g model.Game, goal model.Goal) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	a, r, err := e.account(id)
	if err != nil {
		return err
	}
	oldC, oldF := model.Clone(e.Config), model.Clone(e.Features)
	d := e.Features.Accounts[id]
	changed := false
	switch command {
	case "custom-add":
		if g.AppID == 0 {
			return errors.New("Неверный AppID")
		}
		g.Name = strings.TrimSpace(g.Name)
		if g.Name == "" {
			return errors.New("Введи название игры")
		}
		found := false
		for i, v := range d.Custom {
			if v.AppID == g.AppID {
				d.Custom[i] = g
				found = true
			}
		}
		if !found {
			d.Custom = append(d.Custom, g)
		}
	case "custom-remove", "game-remove":
		list := []model.Game{}
		for _, v := range d.Custom {
			if v.AppID != g.AppID {
				list = append(list, v)
			}
		}
		d.Custom = list
		if command == "game-remove" {
			ids := []uint32{}
			for _, v := range a.AppIDs {
				if v != g.AppID {
					ids = append(ids, v)
				}
			}
			a.AppIDs = ids
			changed = true
		}
	case "popular":
		for _, v := range Popular {
			found := false
			for _, x := range d.Custom {
				found = found || v.AppID == x.AppID
			}
			if !found {
				d.Custom = append(d.Custom, v)
			}
		}
	case "preset-save":
		p.Name = strings.TrimSpace(p.Name)
		if p.Name == "" || len([]rune(p.Name)) > 80 {
			return errors.New("Название пресета: 1–80 символов")
		}
		p.ID = uuid.NewString()
		d.Presets = append(d.Presets, p)
	case "preset-delete":
		list := []model.Preset{}
		for _, v := range d.Presets {
			if v.ID != p.ID {
				list = append(list, v)
			}
		}
		d.Presets = list
	case "preset-apply":
		found := false
		for _, v := range append(append([]model.Preset{}, Builtins...), d.Presets...) {
			if v.ID == p.ID {
				a.AppIDs = append([]uint32{}, v.AppIDs...)
				found = true
			}
		}
		if !found {
			return errors.New("Пресет не найден")
		}
		changed = true
	case "goal-save":
		if goal.Basis == "" {
			goal.Basis = "local"
		}
		found := false
		for i, v := range d.Goals {
			if v.AppID == goal.AppID {
				d.Goals[i] = goal
				found = true
			}
		}
		if !found {
			d.Goals = append(d.Goals, goal)
		}
	case "goal-delete":
		list := []model.Goal{}
		for _, v := range d.Goals {
			if v.AppID != g.AppID {
				list = append(list, v)
			}
		}
		d.Goals = list
	default:
		return errors.New("Неизвестная операция")
	}
	if err = e.save(); err != nil {
		e.Config = oldC
		e.Features = oldF
		return err
	}
	if changed {
		r.index = 0
		r.batchElapsed = 0
		e.clearLocked(id, r)
	}
	e.goals(id)
	return nil
}
