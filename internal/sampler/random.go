// Package sampler: ланцюжок випадковості (qrandom.io → random.org → math/rand/v2).
package sampler

import (
	"encoding/json"
	"fmt"
	"io"
	mathrand "math/rand/v2"
	"net/http"
	"strings"
	"time"
)

// RandSource — джерело випадковості (відповідає ранньому Python rand_src).
type RandSource string

const (
	LocalSource  RandSource = "local"
	QRandSource  RandSource = "qrandom"
	RandomSource RandSource = "random.org"
)

// RandInt повертає випадкове число в [lo, hi] і джерело, яке його видало.
// Ланцюжок: qrandom.io → random.org → локальний PRNG.
// Якщо lo == hi, повертаємо локально без HTTP-запитів (як у Python).
func RandInt(lo, hi int) (int, RandSource) {
	if lo == hi {
		return lo, LocalSource
	}
	if hi < lo {
		lo, hi = hi, lo
	}

	if v, ok := tryQRandInt(lo, hi); ok {
		return clamp(v, lo, hi), QRandSource
	}
	if v, ok := tryRandomOrgInt(lo, hi); ok {
		return clamp(v, lo, hi), RandomSource
	}
	return localInt(lo, hi), LocalSource
}

var httpClient5s = &http.Client{Timeout: 5 * time.Second}

type qrandResponse struct {
	Numbers []int `json:"numbers"`
	Number  []int `json:"number"`
}

func tryQRandInt(lo, hi int) (int, bool) {
	url := fmt.Sprintf("https://qrandom.io/api/random/ints?min=%d&max=%d&n=1", lo, hi)
	resp, err := httpClient5s.Get(url)
	if err != nil {
		return 0, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32*1024))
	if err != nil {
		return 0, false
	}
	var payload qrandResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return 0, false
	}
	if len(payload.Numbers) > 0 {
		return payload.Numbers[0], true
	}
	if len(payload.Number) > 0 {
		return payload.Number[0], true
	}
	return 0, false
}

func tryRandomOrgInt(lo, hi int) (int, bool) {
	url := fmt.Sprintf(
		"https://www.random.org/integers/?num=1&min=%d&max=%d&col=1&base=10&format=plain&rnd=new",
		lo, hi,
	)
	resp, err := httpClient5s.Get(url)
	if err != nil {
		return 0, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return 0, false
	}
	s := strings.TrimSpace(string(body))
	if !isDigits(s) {
		return 0, false
	}
	return parseInt(s)
}

func localInt(lo, hi int) int {
	return mathrand.IntN(hi-lo+1) + lo
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	switch s[0] {
	case '+', '-':
		s = s[1:]
		if s == "" {
			return false
		}
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func parseInt(s string) (int, bool) {
	if !isDigits(s) {
		return 0, false
	}
	neg := false
	if s[0] == '-' {
		neg = true
		s = s[1:]
	} else if s[0] == '+' {
		s = s[1:]
	}
	n := 0
	for _, r := range s {
		n = n*10 + int(r-'0')
	}
	if neg {
		n = -n
	}
	return n, true
}
