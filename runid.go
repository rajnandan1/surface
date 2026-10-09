package main

import (
	"crypto/rand"
	"encoding/binary"
	"time"
)

const crockford = "0123456789abcdefghjkmnpqrstvwxyz"

// newRunID returns a ULID (https://github.com/ulid/spec) in lowercase.
func newRunID(now time.Time) string {
	var b [16]byte
	binary.BigEndian.PutUint64(b[:8], uint64(now.UnixMilli())<<16)
	rand.Read(b[6:])
	hi, lo := binary.BigEndian.Uint64(b[:8]), binary.BigEndian.Uint64(b[8:])
	var id [26]byte
	for i := len(id) - 1; i >= 0; i-- {
		id[i] = crockford[lo&31]
		lo = lo>>5 | hi<<59
		hi >>= 5
	}
	return string(id[:])
}
