package storage

import (
	"encoding/json"
	"github.com/Kandelsbreit/Steam-Hour-Booster-CODEX/internal/model"
	"os"
	"path/filepath"
	"time"
)

// Restore receives a complete, validated secret selection. Absence is an explicit
// tombstone so an older Electron token cannot unexpectedly reappear.
func (s *Store) Restore(c model.Config, f model.Features, secrets map[string]model.Secret) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := model.Validate(&c, &f); err != nil {
		return err
	}
	cb, err := json.Marshal(c)
	if err != nil {
		return err
	}
	fb, err := json.Marshal(f)
	if err != nil {
		return err
	}
	files := map[string][]byte{"settings.json": cb, "features.json": fb}
	for _, a := range c.Accounts {
		name, err := secretName(a.ID)
		if err != nil {
			return err
		}
		b := []byte("FORGOTTEN")
		if secret, ok := secrets[a.ID]; ok && secret.RefreshToken != "" {
			plain, err := json.Marshal(secret)
			if err != nil {
				return err
			}
			b, err = s.Secure.Protect(plain)
			clear(plain)
			if err != nil {
				return err
			}
		}
		files[name] = b
	}
	dest := filepath.Join(s.Dir, "before-restore-"+time.Now().Format("20060102-150405.000"))
	for name := range files {
		old, err := os.ReadFile(filepath.Join(s.Dir, name))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if err = Atomic(filepath.Join(dest, name), old); err != nil {
			return err
		}
	}
	return s.transaction(files)
}
