// Package token 签发和校验运行令牌:每次运行一枚,范围限于一个线程,动作限于策略允许的那几个。
//
// 形态:aut_<base64url(claims JSON)>.<base64url(HMAC-SHA256)>。由 hub 自己的密钥签,平台永远看不到它。
package token

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/memory-co/agentuse/internal/models"
)

const Prefix = "aut_"

// MaxTTL 是运行令牌的最长有效期。
const MaxTTL = 24 * time.Hour

var (
	ErrMalformed = errors.New("token: malformed")
	ErrInvalid   = errors.New("token: invalid signature")
	ErrExpired   = errors.New("token: expired")
	ErrScope     = errors.New("token: action not allowed")
)

// Claims 是令牌携带的范围。
type Claims struct {
	RunID     string          `json:"run"`
	Platform  string          `json:"platform"`
	Thread    string          `json:"thread"`
	Actions   []models.Action `json:"actions"`
	ExpiresAt int64           `json:"exp"`
}

// Allows 报告令牌是否允许某个动作。
func (c Claims) Allows(a models.Action) bool { return models.Has(c.Actions, a) }

// Issuer 用 hub 的密钥签发 / 校验令牌。
type Issuer struct {
	key []byte
	now func() time.Time
}

func NewIssuer(key []byte) *Issuer { return &Issuer{key: key, now: time.Now} }

// Issue 为一次运行签发令牌。ttl 超过 MaxTTL 时按 MaxTTL 算。
func (i *Issuer) Issue(run models.Run, ttl time.Duration) (string, error) {
	if ttl <= 0 || ttl > MaxTTL {
		ttl = MaxTTL
	}
	payload, err := json.Marshal(Claims{
		RunID:     run.ID,
		Platform:  run.Platform,
		Thread:    run.Thread,
		Actions:   run.Actions,
		ExpiresAt: i.now().Add(ttl).Unix(),
	})
	if err != nil {
		return "", err
	}
	p := base64.RawURLEncoding.EncodeToString(payload)
	return Prefix + p + "." + i.sign(p), nil
}

// Verify 校验令牌并交回它的范围。
func (i *Issuer) Verify(tok string) (Claims, error) {
	var c Claims
	rest, ok := strings.CutPrefix(tok, Prefix)
	if !ok {
		return c, ErrMalformed
	}
	p, sig, ok := strings.Cut(rest, ".")
	if !ok {
		return c, ErrMalformed
	}
	if !hmac.Equal([]byte(sig), []byte(i.sign(p))) {
		return c, ErrInvalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(p)
	if err != nil || json.Unmarshal(raw, &c) != nil {
		return c, ErrMalformed
	}
	if i.now().Unix() >= c.ExpiresAt {
		return c, ErrExpired
	}
	return c, nil
}

func (i *Issuer) sign(p string) string {
	mac := hmac.New(sha256.New, i.key)
	mac.Write([]byte(p))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
