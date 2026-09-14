package backup

import (
	"bytes"
	"compress/gzip"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"github.com/Kandelsbreit/Steam-Hour-Booster-CODEX/internal/model"
	"golang.org/x/crypto/scrypt"
	"io"
	"time"
)

const Limit = 32 << 20

var magic = []byte("AGNIA1\n")

type Data struct {
	Version        int                     `json:"version"`
	CreatedAt      string                  `json:"createdAt"`
	Config         model.Config            `json:"config"`
	Features       model.Features          `json:"features"`
	TokensIncluded bool                    `json:"tokensIncluded"`
	Tokens         map[string]string       `json:"tokens"`
	Secrets        map[string]model.Secret `json:"secrets,omitempty"`
}

func Validate(d *Data) error {
	if d.Version != 1 {
		return errors.New("Неподдерживаемая резервная копия")
	}
	if err := model.Validate(&d.Config, &d.Features); err != nil {
		return err
	}
	for id, t := range d.Tokens {
		if !model.ValidID(id) || len(t) > 16384 {
			return errors.New("Неверная сессия")
		}
	}
	for id, t := range d.Secrets {
		if !model.ValidID(id) || len(t.RefreshToken) > 16384 || len(t.GuardData) > 65536 || len(t.AccessToken) > 16384 {
			return errors.New("Неверная сессия")
		}
	}
	if !d.TokensIncluded && (len(d.Tokens) > 0 || len(d.Secrets) > 0) {
		return errors.New("Сессии требуют шифрования")
	}
	return nil
}
func Pack(d Data, password string) ([]byte, error) {
	if err := Validate(&d); err != nil {
		return nil, err
	}
	if d.TokensIncluded && len([]rune(password)) < 8 {
		return nil, errors.New("Для переноса сессий нужен пароль от 8 символов")
	}
	if password != "" && len([]rune(password)) < 8 {
		return nil, errors.New("Пароль: минимум 8 символов")
	}
	d.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	b, err := json.Marshal(d)
	if err != nil || len(b) > Limit {
		return nil, errors.New("Слишком большой архив")
	}
	var compressed bytes.Buffer
	w := gzip.NewWriter(&compressed)
	if _, err = w.Write(b); err != nil {
		return nil, err
	}
	if err = w.Close(); err != nil {
		return nil, err
	}
	out := append([]byte{}, magic...)
	if password == "" {
		return append(append(out, 0), compressed.Bytes()...), nil
	}
	salt := make([]byte, 16)
	iv := make([]byte, 12)
	if _, err = rand.Read(salt); err != nil {
		return nil, err
	}
	if _, err = rand.Read(iv); err != nil {
		return nil, err
	}
	gcm, err := derive(password, salt)
	if err != nil {
		return nil, err
	}
	encrypted := gcm.Seal(nil, iv, compressed.Bytes(), magic)
	out = append(out, 1)
	out = append(out, salt...)
	out = append(out, iv...)
	out = append(out, encrypted[len(encrypted)-16:]...)
	return append(out, encrypted[:len(encrypted)-16]...), nil
}
func derive(password string, salt []byte) (cipher.AEAD, error) {
	key, err := scrypt.Key([]byte(password), salt, 16384, 8, 1, 32)
	if err != nil {
		return nil, err
	}
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
func Unpack(b []byte, password string) (d Data, err error) {
	defer func() {
		if err != nil {
			err = errors.New("Неверный пароль или повреждённая резервная копия. Данные не изменены.")
		}
	}()
	if len(b) > Limit || len(b) < 8 || !bytes.Equal(b[:7], magic) {
		return d, errors.New("format")
	}
	mode := b[7]
	payload := b[8:]
	switch mode {
	case 0:
	case 1:
		if len(payload) < 44 || password == "" {
			return d, errors.New("password")
		}
		gcm, e := derive(password, payload[:16])
		if e != nil {
			return d, e
		}
		ciphertext := append(append([]byte{}, payload[44:]...), payload[28:44]...)
		payload, err = gcm.Open(nil, payload[16:28], ciphertext, magic)
		if err != nil {
			return d, err
		}
	default:
		return d, errors.New("version")
	}
	r, err := gzip.NewReader(bytes.NewReader(payload))
	if err != nil {
		return d, err
	}
	defer r.Close()
	plain, err := io.ReadAll(io.LimitReader(r, Limit+1))
	if err != nil || len(plain) > Limit {
		return d, errors.New("size")
	}
	if err = json.Unmarshal(plain, &d); err != nil {
		return d, err
	}
	if d.TokensIncluded && mode != 1 {
		return d, errors.New("unencrypted tokens")
	}
	err = Validate(&d)
	return
}
