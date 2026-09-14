package backup

import (
	"bytes"
	"github.com/Kandelsbreit/Steam-Hour-Booster-CODEX/internal/model"
	"testing"
)

func fixture() Data {
	return Data{Version: 1, Config: model.Config{Version: 1, Accounts: []model.Account{}}, Features: model.NewFeatures()}
}
func TestArchiveModes(t *testing.T) {
	for _, password := range []string{"", "Тестовый пароль"} {
		d := fixture()
		b, err := Pack(d, password)
		if err != nil {
			t.Fatal(err)
		}
		out, err := Unpack(b, password)
		if err != nil || out.Config.Version != 1 {
			t.Fatal(err)
		}
		if password != "" {
			if _, err = Unpack(b, "wrong password"); err == nil {
				t.Fatal("bad password accepted")
			}
			b[len(b)-1] ^= 1
			if _, err = Unpack(b, password); err == nil {
				t.Fatal("tampering accepted")
			}
		}
	}
}
func TestSecretsRequireEncryption(t *testing.T) {
	d := fixture()
	d.TokensIncluded = true
	d.Tokens = map[string]string{"11111111-1111-4111-8111-111111111111": "TEST_ONLY_SECRET"}
	if _, err := Pack(d, ""); err == nil {
		t.Fatal("plaintext permitted")
	}
	b, err := Pack(d, "test-password")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte("TEST_ONLY_SECRET")) {
		t.Fatal("secret exposed")
	}
	out, err := Unpack(b, "test-password")
	if err != nil || len(out.Tokens) != 1 {
		t.Fatal(err)
	}
}
