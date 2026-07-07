package identity

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

const (
	idPrefix      = "sha256:"
	hashHexLength = sha256.Size * 2
	shortIDLength = 12
)

const Prefix = idPrefix

type ResourceKind string

const (
	ResourceProject   ResourceKind = "project"
	ResourceAgent     ResourceKind = "agent"
	ResourceScheduler ResourceKind = "scheduler"
	ResourceTrigger   ResourceKind = "trigger"
	ResourceLoader    ResourceKind = "loader"
	ResourceRun       ResourceKind = "run"
	ResourceSandbox   ResourceKind = "sandbox"
	ResourceCache     ResourceKind = "cache"
	ResourceWorkspace ResourceKind = "workspace"
)

func NewID(kind ResourceKind, parts ...string) string {
	h := sha256.New()
	writePart(h, string(kind))
	for _, part := range parts {
		writePart(h, part)
	}
	return idPrefix + hex.EncodeToString(h.Sum(nil))
}

func NewRandomID(kind ResourceKind) string {
	var seed [sha256.Size]byte
	if _, err := rand.Read(seed[:]); err != nil {
		return NewID(kind, time.Now().UTC().Format(time.RFC3339Nano), err.Error())
	}
	return NewID(kind, string(seed[:]))
}

func ShortID(id string) string {
	id = strings.TrimSpace(id)
	if !IsID(id) {
		return ""
	}
	return id[len(idPrefix) : len(idPrefix)+shortIDLength]
}

func Hash(id string) (string, error) {
	id = strings.TrimSpace(strings.ToLower(id))
	if strings.HasPrefix(id, idPrefix) {
		id = strings.TrimPrefix(id, idPrefix)
	}
	if len(id) != hashHexLength || !isLowerHex(id) {
		return "", fmt.Errorf("invalid sha256 identity")
	}
	return id, nil
}

func IsID(id string) bool {
	id = strings.TrimSpace(id)
	if len(id) != len(idPrefix)+hashHexLength || !strings.HasPrefix(id, idPrefix) {
		return false
	}
	return isLowerHex(id[len(idPrefix):])
}

func IsIDPrefix(id string) bool {
	id = strings.TrimSpace(strings.ToLower(id))
	if strings.HasPrefix(id, idPrefix) {
		id = strings.TrimPrefix(id, idPrefix)
	}
	return len(id) >= shortIDLength && len(id) <= hashHexLength && isLowerHex(id)
}

func IsShortID(id string) bool {
	id = strings.TrimSpace(id)
	return len(id) == shortIDLength && isLowerHex(id)
}

func writePart(h interface{ Write([]byte) (int, error) }, value string) {
	value = strings.TrimSpace(value)
	var length [8]byte
	n := uint64(len(value))
	for i := len(length) - 1; i >= 0; i-- {
		length[i] = byte(n)
		n >>= 8
	}
	_, _ = h.Write(length[:])
	_, _ = h.Write([]byte(value))
}

func isLowerHex(value string) bool {
	for _, r := range value {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
		default:
			return false
		}
	}
	return true
}
