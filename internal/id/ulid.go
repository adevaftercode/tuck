package id

import (
	"crypto/rand"
	"errors"
	"time"
)

const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// New returns a prefixed ULID using the current UTC millisecond and crypto
// entropy. The prefix makes task IDs easy to distinguish from other IDs.
func New(now time.Time) (string, error) {
	var raw [16]byte
	ms := uint64(now.UTC().UnixMilli())
	if ms >= 1<<48 {
		return "", errors.New("timestamp is outside the ULID range")
	}
	raw[0] = byte(ms >> 40)
	raw[1] = byte(ms >> 32)
	raw[2] = byte(ms >> 24)
	raw[3] = byte(ms >> 16)
	raw[4] = byte(ms >> 8)
	raw[5] = byte(ms)
	if _, err := rand.Read(raw[6:]); err != nil {
		return "", err
	}

	var encoded [26]byte
	var accumulator uint32
	bits := uint(2) // ULIDs are encoded as 130 bits with two leading zero bits.
	input := 0
	for i := range encoded {
		for bits < 5 {
			if input < len(raw) {
				accumulator = accumulator<<8 | uint32(raw[input])
				input++
				bits += 8
			} else {
				accumulator <<= 1
				bits++
			}
		}
		bits -= 5
		encoded[i] = alphabet[(accumulator>>bits)&31]
		if bits == 0 {
			accumulator = 0
		} else {
			accumulator &= (1 << bits) - 1
		}
	}
	return "tuck_" + string(encoded[:]), nil
}
