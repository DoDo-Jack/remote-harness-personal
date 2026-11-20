package main

import (
	"crypto/hmac"
	"encoding/binary"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"sync"
	"time"
)

/* ───────────── token ───────────── */

type tokenPayload struct {
	UID string `json:"uid"`
	Exp int64  `json:"exp"`
}

func signToken(secret []byte, uid string, ttl time.Duration) string {
	p := tokenPayload{UID: uid, Exp: time.Now().Add(ttl).Unix()}
	raw, _ := json.Marshal(p)
	body := base64.RawURLEncoding.EncodeToString(raw)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(body))
	return body + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func verifyToken(secret []byte, token string) (string, bool) {
	i := -1
	for k := 0; k < len(token); k++ {
		if token[k] == '.' {
			i = k
			break
		}
	}
	if i <= 0 || i == len(token)-1 {
		return "", false
	}
	body, sig := token[:i], token[i+1:]
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(body))
	want := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(sig), []byte(want)) {
		return "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return "", false
	}
	var p tokenPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", false
	}
	if time.Now().Unix() > p.Exp {
		return "", false
	}
	return p.UID, true
}

/* ───────────── 验证码 ───────────── */

type codeEntry struct {
	code     string
	expires  time.Time
	attempts int
}

type CodeStore struct {
	mu    sync.Mutex
	codes map[string]*codeEntry
}

func newCodeStore() *CodeStore { return &CodeStore{codes: map[string]*codeEntry{}} }

func (c *CodeStore) Issue(phone string) string {
	n, _ := rand.Int(rand.Reader, big.NewInt(1000000))
	code := leftPad(n.Int64(), 6)
	c.mu.Lock()
	c.codes[phone] = &codeEntry{code: code, expires: time.Now().Add(5 * time.Minute)}
	c.mu.Unlock()
	return code
}

func (c *CodeStore) Check(phone, code string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.codes[phone]
	if e == nil {
		return false
	}
	if time.Now().After(e.expires) {
		delete(c.codes, phone)
		return false
	}
	e.attempts++
	if e.attempts > 5 {
		delete(c.codes, phone)
		return false
	}
	if e.code != code {
		return false
	}
	delete(c.codes, phone)
	return true
}

func leftPad(n int64, width int) string {
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	for len(s) < width {
		s = "0" + s
	}
	return s
}

/* ───────────── 限流 ───────────── */

type Limiter struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

func newLimiter() *Limiter { return &Limiter{hits: map[string][]time.Time{}} }

// Allow 返回 false 表示超限。minInterval 两次最小间隔；maxPerHour 每小时上限。
func (l *Limiter) Allow(key string, minInterval time.Duration, maxPerHour int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	cut := now.Add(-time.Hour)
	list := l.hits[key]
	kept := list[:0]
	for _, t := range list {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= maxPerHour {
		l.hits[key] = kept
		return false
	}
	if len(kept) > 0 && now.Sub(kept[len(kept)-1]) < minInterval {
		l.hits[key] = kept
		return false
	}
	l.hits[key] = append(kept, now)
	return true
}

// 简单 PBKDF2-HMAC-SHA256（不引第三方库）
func pbkdf2(pass, salt []byte, iter, keyLen int) []byte {
	prf := hmac.New(sha256.New, pass)
	var out []byte
	for block := 1; len(out) < keyLen; block++ {
		prf.Reset()
		prf.Write(salt)
		var b [4]byte
		binary.BigEndian.PutUint32(b[:], uint32(block))
		prf.Write(b[:])
		u := prf.Sum(nil)
		t := make([]byte, len(u))
		copy(t, u)
		for i := 1; i < iter; i++ {
			prf.Reset()
			prf.Write(u)
			u = prf.Sum(u[:0])
			for j := range t {
				t[j] ^= u[j]
			}
		}
		out = append(out, t...)
	}
	return out[:keyLen]
}

func hashPassword(pass string) (salt, hash string) {
	sb := make([]byte, 16)
	rand.Read(sb)
	salt = base64.RawURLEncoding.EncodeToString(sb)
	h := pbkdf2([]byte(pass), []byte(salt), 120000, 32)
	return salt, base64.RawURLEncoding.EncodeToString(h)
}

func checkPassword(pass, salt, hash string) bool {
	h := pbkdf2([]byte(pass), []byte(salt), 120000, 32)
	return hmac.Equal([]byte(base64.RawURLEncoding.EncodeToString(h)), []byte(hash))
}
