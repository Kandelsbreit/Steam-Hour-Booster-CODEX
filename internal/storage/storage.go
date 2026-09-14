package storage

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/Kandelsbreit/Steam-Hour-Booster-CODEX/internal/model"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Protector interface {
	Protect([]byte) ([]byte, error)
	Unprotect([]byte) ([]byte, error)
}
type Store struct {
	Dir    string
	Secure Protector
	mu     sync.Mutex
}

func Open(dir string, p Protector) (*Store, model.Config, model.Features, error) {
	s := &Store{Dir: dir, Secure: p}
	c := model.Config{Version: 1, Accounts: []model.Account{}}
	f := model.NewFeatures()
	for name, dst := range map[string]any{"settings.json": &c, "features.json": &f} {
		b, e := os.ReadFile(filepath.Join(dir, name))
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return nil, c, f, e
		}
		if len(b) > 32<<20 || json.Unmarshal(b, dst) != nil {
			return nil, c, f, errors.New("Повреждены локальные данные: " + name + ". Исходные файлы сохранены.")
		}
	}
	if e := model.Validate(&c, &f); e != nil {
		return nil, c, f, e
	}
	if e := os.MkdirAll(dir, 0700); e != nil {
		return nil, c, f, e
	}
	return s, c, f, nil
}
func Atomic(file string, b []byte) error {
	if e := os.MkdirAll(filepath.Dir(file), 0700); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(file), ".agnia-*")
	if e != nil {
		return e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(b)
	}
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	return replaceFile(tmp, file)
}
func (s *Store) Save(c model.Config, f model.Features) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := model.Validate(&c, &f); e != nil {
		return e
	}
	cb, e := json.MarshalIndent(c, "", "  ")
	if e != nil {
		return e
	}
	fb, e := json.Marshal(f)
	if e != nil {
		return e
	}
	return s.transaction(map[string][]byte{"settings.json": cb, "features.json": fb})
}

// All originals are retained until every replacement succeeds. Failed rollback
// keeps the recovery directory rather than deleting the only surviving copy.
func (s *Store) transaction(files map[string][]byte) error {
	stage, e := os.MkdirTemp(s.Dir, "recovery-")
	if e != nil {
		return e
	}
	original := map[string][]byte{}
	exists := map[string]bool{}
	written := []string{}
	for name, b := range files {
		old, err := os.ReadFile(filepath.Join(s.Dir, name))
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		exists[name] = err == nil
		original[name] = old
		if err == nil {
			if e = Atomic(filepath.Join(stage, name), old); e != nil {
				return e
			}
		}
		_ = b
	}
	for name, b := range files {
		if e = Atomic(filepath.Join(s.Dir, name), b); e != nil {
			rollbackOK := true
			for _, n := range written {
				if exists[n] {
					if Atomic(filepath.Join(s.Dir, n), original[n]) != nil {
						rollbackOK = false
					}
				} else if os.Remove(filepath.Join(s.Dir, n)) != nil {
					rollbackOK = false
				}
			}
			if rollbackOK {
				_ = os.RemoveAll(stage)
			}
			return errors.New("Не удалось сохранить данные. Резервная копия: " + stage)
		}
		written = append(written, name)
	}
	return os.RemoveAll(stage)
}
func (s *Store) BackupLegacy() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	marker := filepath.Join(s.Dir, "wails-migrated")
	if _, e := os.Stat(marker); e == nil {
		return nil
	}
	dest := filepath.Join(s.Dir, "migration-backup-"+time.Now().Format("20060102-150405"))
	for _, name := range []string{"settings.json", "features.json", "Local State", "telegram.dat", "sessions"} {
		root := filepath.Join(s.Dir, name)
		if _, e := os.Stat(root); os.IsNotExist(e) {
			continue
		}
		e := filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
			if e != nil {
				return e
			}
			if d.IsDir() {
				return nil
			}
			if d.Type()&os.ModeSymlink != 0 {
				return errors.New("Ссылка в папке данных не поддерживается")
			}
			b, e := os.ReadFile(p)
			if e != nil {
				return e
			}
			rel, _ := filepath.Rel(s.Dir, p)
			return Atomic(filepath.Join(dest, rel), b)
		})
		if e != nil {
			return e
		}
	}
	return Atomic(marker, []byte("Backup: "+dest))
}
func secretName(id string) (string, error) {
	if id != "telegram" && !model.ValidID(id) {
		return "", errors.New("Неверный ID секрета")
	}
	return filepath.Join("secrets-v2", id+".dat"), nil
}
func (s *Store) Has(id string) bool { _, e := s.ReadSecret(id); return e == nil }
func (s *Store) ReadSecret(id string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	name, e := secretName(id)
	if e != nil {
		return nil, e
	}
	b, e := os.ReadFile(filepath.Join(s.Dir, name))
	if e == nil {
		if bytes.Equal(b, []byte("FORGOTTEN")) {
			return nil, os.ErrNotExist
		}
		return s.Secure.Unprotect(b)
	}
	if !os.IsNotExist(e) {
		return nil, e
	}
	legacy := filepath.Join("sessions", id+".dat")
	if id == "telegram" {
		legacy = "telegram.dat"
	}
	b, e = os.ReadFile(filepath.Join(s.Dir, legacy))
	if e != nil {
		return nil, e
	}
	return s.decryptElectron(b)
}
func (s *Store) WriteSecret(id string, b []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	name, e := secretName(id)
	if e != nil {
		return e
	}
	enc, e := s.Secure.Protect(b)
	if e != nil {
		return errors.New("Хранилище Windows недоступно")
	}
	return Atomic(filepath.Join(s.Dir, name), enc)
}
func (s *Store) Forget(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	name, e := secretName(id)
	if e != nil {
		return e
	}
	return Atomic(filepath.Join(s.Dir, name), []byte("FORGOTTEN"))
}
func (s *Store) Token(id string) (model.Secret, error) {
	b, e := s.ReadSecret(id)
	if e != nil {
		return model.Secret{}, e
	}
	var out model.Secret
	if len(b) > 0 && b[0] == '{' {
		e = json.Unmarshal(b, &out)
	} else {
		out.RefreshToken = string(b)
	}
	return out, e
}
func (s *Store) SaveToken(id string, secret model.Secret) error {
	b, e := json.Marshal(secret)
	if e != nil {
		return e
	}
	return s.WriteSecret(id, b)
}
func (s *Store) decryptElectron(b []byte) ([]byte, error) {
	if len(b) < 3 || (!bytes.Equal(b[:3], []byte("v10")) && !bytes.Equal(b[:3], []byte("v11"))) {
		return s.Secure.Unprotect(b)
	}
	data, e := os.ReadFile(filepath.Join(s.Dir, "Local State"))
	if e != nil {
		return nil, errors.New("Для старой сессии нужен исходный Local State")
	}
	var state struct {
		OSCrypt struct {
			Key string `json:"encrypted_key"`
		} `json:"os_crypt"`
	}
	if json.Unmarshal(data, &state) != nil {
		return nil, errors.New("Повреждён Local State")
	}
	wrapped, e := base64.StdEncoding.DecodeString(state.OSCrypt.Key)
	if e != nil || !strings.HasPrefix(string(wrapped), "DPAPI") {
		return nil, errors.New("Неподдерживаемый ключ Electron")
	}
	key, e := s.Secure.Unprotect(wrapped[5:])
	if e != nil {
		return nil, e
	}
	defer clear(key)
	block, e := aes.NewCipher(key)
	if e != nil {
		return nil, e
	}
	gcm, e := cipher.NewGCM(block)
	if e != nil {
		return nil, e
	}
	if len(b) < 3+gcm.NonceSize()+gcm.Overhead() {
		return nil, errors.New("Повреждён токен")
	}
	return gcm.Open(nil, b[3:15], b[15:], nil)
}
