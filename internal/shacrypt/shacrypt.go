// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

// Package shacrypt implements the SHA-256 and SHA-512 based crypt(3) schemes
// ($5$ and $6$) from Ulrich Drepper's "Unix crypt using SHA-256 and SHA-512",
// which EdgeOS uses for login passwords. It exists so the provider can check
// a configured password against the hash the router stores.
package shacrypt

import (
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"errors"
	"hash"
	"strconv"
	"strings"
)

const (
	defaultRounds = 5000
	minRounds     = 1000
	maxRounds     = 999999999
	maxSaltLen    = 16
	alphabet      = "./0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
)

// ErrUnsupported is returned for hashes that are not $5$ or $6$ crypt strings.
var ErrUnsupported = errors.New("not a SHA-256 or SHA-512 crypt hash")

type scheme struct {
	id    string
	new   func() hash.Hash
	size  int
	order [][3]int // byte triples for the final base64 encoding
	tail  [3]int   // the last group: indices, -1 for a zero byte
	tailN int
}

var sha512Scheme = scheme{
	id: "6", new: sha512.New, size: sha512.Size,
	order: [][3]int{
		{0, 21, 42}, {22, 43, 1}, {44, 2, 23}, {3, 24, 45}, {25, 46, 4}, {47, 5, 26}, {6, 27, 48},
		{28, 49, 7}, {50, 8, 29}, {9, 30, 51}, {31, 52, 10}, {53, 11, 32}, {12, 33, 54}, {34, 55, 13},
		{56, 14, 35}, {15, 36, 57}, {37, 58, 16}, {59, 17, 38}, {18, 39, 60}, {40, 61, 19}, {62, 20, 41},
	},
	tail: [3]int{-1, -1, 63}, tailN: 2,
}

var sha256Scheme = scheme{
	id: "5", new: sha256.New, size: sha256.Size,
	order: [][3]int{
		{0, 10, 20}, {21, 1, 11}, {12, 22, 2}, {3, 13, 23}, {24, 4, 14},
		{15, 25, 5}, {6, 16, 26}, {27, 7, 17}, {18, 28, 8}, {9, 19, 29},
	},
	tail: [3]int{-1, 31, 30}, tailN: 3,
}

// Crypt512 hashes password with salt the way crypt(3) does for "$6$<salt>".
// The salt is cut to 16 characters; rounds 0 means the default of 5000 and
// leaves the rounds out of the result.
func Crypt512(password, salt string, rounds int) string {
	return sha512Scheme.crypt(password, salt, rounds)
}

// Crypt256 is Crypt512 for "$5$" hashes.
func Crypt256(password, salt string, rounds int) string {
	return sha256Scheme.crypt(password, salt, rounds)
}

// Verify reports whether password matches the crypt hash. It returns
// ErrUnsupported when hash is not a $5$ or $6$ hash.
func Verify(password, hashed string) (bool, error) {
	var s *scheme
	switch {
	case strings.HasPrefix(hashed, "$6$"):
		s = &sha512Scheme
	case strings.HasPrefix(hashed, "$5$"):
		s = &sha256Scheme
	default:
		return false, ErrUnsupported
	}
	rest := hashed[3:]
	rounds := 0
	if strings.HasPrefix(rest, "rounds=") {
		n, after, ok := strings.Cut(rest[len("rounds="):], "$")
		r, err := strconv.Atoi(n)
		if !ok || err != nil || r < 1 {
			return false, ErrUnsupported
		}
		rounds, rest = r, after
	}
	salt, _, ok := strings.Cut(rest, "$")
	if !ok {
		return false, ErrUnsupported
	}
	got := s.crypt(password, salt, rounds)
	return subtle.ConstantTimeCompare([]byte(got), []byte(hashed)) == 1, nil
}

func (s *scheme) crypt(password, salt string, rounds int) string {
	explicit := rounds != 0
	switch {
	case !explicit:
		rounds = defaultRounds
	case rounds < minRounds:
		rounds = minRounds
	case rounds > maxRounds:
		rounds = maxRounds
	}
	if len(salt) > maxSaltLen {
		salt = salt[:maxSaltLen]
	}
	p, sb := []byte(password), []byte(salt)

	h := s.new()
	h.Write(p)
	h.Write(sb)
	h.Write(p)
	b := h.Sum(nil)

	h = s.new()
	h.Write(p)
	h.Write(sb)
	h.Write(repeat(b, len(p)))
	for n := len(p); n > 0; n >>= 1 {
		if n&1 != 0 {
			h.Write(b)
		} else {
			h.Write(p)
		}
	}
	a := h.Sum(nil)

	h = s.new()
	for range p {
		h.Write(p)
	}
	pp := repeat(h.Sum(nil), len(p))

	h = s.new()
	for i := 0; i < 16+int(a[0]); i++ {
		h.Write(sb)
	}
	ss := repeat(h.Sum(nil), len(sb))

	c := a
	for i := range rounds {
		h = s.new()
		if i&1 != 0 {
			h.Write(pp)
		} else {
			h.Write(c)
		}
		if i%3 != 0 {
			h.Write(ss)
		}
		if i%7 != 0 {
			h.Write(pp)
		}
		if i&1 != 0 {
			h.Write(c)
		} else {
			h.Write(pp)
		}
		c = h.Sum(c[:0])
	}

	var out strings.Builder
	out.WriteString("$" + s.id + "$")
	if explicit {
		out.WriteString("rounds=" + strconv.Itoa(rounds) + "$")
	}
	out.WriteString(salt)
	out.WriteByte('$')
	for _, t := range s.order {
		b64(&out, c[t[0]], c[t[1]], c[t[2]], 4)
	}
	var tail [3]byte
	for i, idx := range s.tail {
		if idx >= 0 {
			tail[i] = c[idx]
		}
	}
	b64(&out, tail[0], tail[1], tail[2], s.tailN)
	return out.String()
}

// repeat returns n bytes of b repeated.
func repeat(b []byte, n int) []byte {
	out := make([]byte, 0, n)
	for len(out) < n {
		out = append(out, b[:min(len(b), n-len(out))]...)
	}
	return out
}

func b64(out *strings.Builder, b2, b1, b0 byte, n int) {
	w := uint(b2)<<16 | uint(b1)<<8 | uint(b0)
	for range n {
		out.WriteByte(alphabet[w&0x3f])
		w >>= 6
	}
}
