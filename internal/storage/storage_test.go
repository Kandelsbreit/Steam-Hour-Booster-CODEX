package storage

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"github.com/Kandelsbreit/Steam-Hour-Booster-CODEX/internal/model"
	"os"
	"path/filepath"
	"testing"
)

const testID = "11111111-1111-4111-8111-111111111111"

func TestDPAPIAndLegacyMigration(t *testing.T) {
	s, c, f, err := Open(filepath.Join(t.TempDir(), "Русский путь с пробелами"), DPAPI{})
	if err != nil {
		t.Fatal(err)
	}
	c.Accounts = append(c.Accounts, model.Account{ID: testID, Name: "test_user", AppIDs: []uint32{440}, BatchSize: 32, RotationMinutes: 60})
	if err = s.Save(c, f); err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{7}, 32)
	wrapped, err := s.Secure.Protect(key)
	if err != nil {
		t.Fatal(err)
	}
	state, _ := json.Marshal(map[string]any{"os_crypt": map[string]string{"encrypted_key": base64.StdEncoding.EncodeToString(append([]byte("DPAPI"), wrapped...))}})
	if err = Atomic(filepath.Join(s.Dir, "Local State"), state); err != nil {
		t.Fatal(err)
	}
	block, _ := aes.NewCipher(key)
	gcm, _ := cipher.NewGCM(block)
	iv := bytes.Repeat([]byte{3}, 12)
	encrypted := append(append([]byte("v10"), iv...), gcm.Seal(nil, iv, []byte("TEST_ONLY_REFRESH"), nil)...)
	if err = Atomic(filepath.Join(s.Dir, "sessions", testID+".dat"), encrypted); err != nil {
		t.Fatal(err)
	}
	if err = s.BackupLegacy(); err != nil {
		t.Fatal(err)
	}
	token, err := s.Token(testID)
	if err != nil || token.RefreshToken != "TEST_ONLY_REFRESH" {
		t.Fatal("legacy token failed", err)
	}
	if err = s.SaveToken(testID, token); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(s.Dir, "secrets-v2", testID+".dat"))
	if bytes.Contains(raw, []byte(token.RefreshToken)) {
		t.Fatal("plaintext secret")
	}
	token2, err := s.Token(testID)
	if err != nil || token2 != token {
		t.Fatal("DPAPI roundtrip", err)
	}
	if err = s.Forget(testID); err != nil {
		t.Fatal(err)
	}
	if s.Has(testID) {
		t.Fatal("legacy token resurfaced")
	}
	if _, err = os.Stat(filepath.Join(s.Dir, "sessions", testID+".dat")); err != nil {
		t.Fatal("legacy removed")
	}
}
func TestCorruptionPreserved(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	want := []byte("{BROKEN")
	_ = os.WriteFile(path, want, 0600)
	if _, _, _, err := Open(dir, DPAPI{}); err == nil {
		t.Fatal("accepted corruption")
	}
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, want) {
		t.Fatal("overwritten")
	}
}
func TestRestoreValidatedBeforeMutation(t *testing.T) {
	s, c, f, err := Open(t.TempDir(), DPAPI{})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Save(c, f); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(s.Dir, "settings.json"))
	c.Accounts = []model.Account{{ID: "../../outside", Name: "bad"}}
	if err = s.Restore(c, f, nil); err == nil {
		t.Fatal("accepted invalid archive")
	}
	after, _ := os.ReadFile(filepath.Join(s.Dir, "settings.json"))
	if !bytes.Equal(before, after) {
		t.Fatal("data changed")
	}
}
