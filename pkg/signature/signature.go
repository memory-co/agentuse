// Package signature 是平台 ⇄ hub 两个方向共用的 HMAC 签名。
//
// 签名串 = timestamp + "." + body,HMAC-SHA256,hex 编码。
// 放在两个头里:X-AgentUse-Timestamp(unix 秒)与 X-AgentUse-Signature(v1=<hex>)。
package signature

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	HeaderTimestamp = "X-AgentUse-Timestamp"
	HeaderSignature = "X-AgentUse-Signature"
	// Tolerance 是允许的时钟偏差,超出即视为重放。
	Tolerance = 5 * time.Minute
)

var (
	ErrMissing = errors.New("signature: missing headers")
	ErrExpired = errors.New("signature: timestamp outside tolerance")
	ErrInvalid = errors.New("signature: mismatch")
)

// Sign 计算 body 在 ts 时刻的签名。
func Sign(secret string, ts int64, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strconv.FormatInt(ts, 10)))
	mac.Write([]byte("."))
	mac.Write(body)
	return "v1=" + hex.EncodeToString(mac.Sum(nil))
}

// SetHeaders 给请求签名。
func SetHeaders(h http.Header, secret string, body []byte, now time.Time) {
	ts := now.Unix()
	h.Set(HeaderTimestamp, strconv.FormatInt(ts, 10))
	h.Set(HeaderSignature, Sign(secret, ts, body))
}

// Verify 验证请求头里的签名。
func Verify(h http.Header, secret string, body []byte, now time.Time) error {
	tsRaw, sig := h.Get(HeaderTimestamp), h.Get(HeaderSignature)
	if tsRaw == "" || sig == "" {
		return ErrMissing
	}
	ts, err := strconv.ParseInt(tsRaw, 10, 64)
	if err != nil {
		return ErrMissing
	}
	if d := now.Sub(time.Unix(ts, 0)); d > Tolerance || d < -Tolerance {
		return ErrExpired
	}
	want := Sign(secret, ts, body)
	if !strings.HasPrefix(sig, "v1=") || !hmac.Equal([]byte(sig), []byte(want)) {
		return ErrInvalid
	}
	return nil
}
