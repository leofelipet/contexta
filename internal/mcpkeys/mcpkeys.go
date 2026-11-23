// Package mcpkeys holds the named, scoped bearer keys that authenticate MCP clients.
package mcpkeys

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"time"
)

const (
	AccessRead   = "read"   // every read-only tool, including ones added later
	AccessWrite  = "write"  // every tool
	AccessCustom = "custom" // only Tools
	tokenPrefix  = "ctx_"
)

type Key struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Access     string     `json:"access"`
	Tools      []string   `json:"tools"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

type Params struct {
	Name      string
	Access    string
	Tools     []string
	ExpiresAt *time.Time // create only
}

// Tool describes one MCP tool so keys can be scoped to it.
type Tool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	ReadOnly    bool   `json:"read_only"`
}

// Allows reports whether the key may call tool.
func (k Key) Allows(tool Tool) bool {
	switch k.Access {
	case AccessWrite:
		return true
	case AccessRead:
		return tool.ReadOnly
	}
	for _, name := range k.Tools {
		if name == tool.Name {
			return true
		}
	}
	return false
}

// Generate returns a new secret token, its display prefix and the hash to store.
func Generate() (token, prefix, hash string, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", "", err
	}
	token = tokenPrefix + base64.RawURLEncoding.EncodeToString(raw)
	return token, token[:len(tokenPrefix)+6], Hash(token), nil
}

// Hash is SHA-256: tokens carry 256 random bits, so a slow KDF adds nothing.
func Hash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
