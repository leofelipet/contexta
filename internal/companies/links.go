package companies

import (
	"errors"
	"net/url"
	"strings"
)

var errInvalidLink = errors.New("invalid company link")

const (
	MaxLinks     = 50
	MaxLinkLabel = 200
	MaxLinkURL   = 2000
	MaxRulesLen  = 10000
)

func NormalizeLinks(links []Link) ([]Link, error) {
	if len(links) > MaxLinks {
		return nil, errInvalidLink
	}
	normalized := make([]Link, 0, len(links))
	for _, link := range links {
		label, raw := strings.TrimSpace(link.Label), strings.TrimSpace(link.URL)
		if label == "" && raw == "" {
			continue
		}
		if label == "" || len(label) > MaxLinkLabel || raw == "" || len(raw) > MaxLinkURL {
			return nil, errInvalidLink
		}
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return nil, errInvalidLink
		}
		normalized = append(normalized, Link{Label: label, URL: raw})
	}
	return normalized, nil
}
