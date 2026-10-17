package youtube

import (
	"net/url"
	"regexp"
	"strings"
)

var idPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)

// ParseVideoID returns the 11-character video id in a bare id or a Studio,
// watch, shorts or youtu.be URL. ok is false when there is none.
func ParseVideoID(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if idPattern.MatchString(s) {
		return s, true
	}
	if !strings.Contains(s, "://") && strings.Contains(s, ".") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return "", false
	}
	host := strings.ToLower(u.Hostname())
	parts := strings.FieldsFunc(u.Path, func(r rune) bool { return r == '/' })
	at := func(i int) string {
		if i >= 0 && i < len(parts) {
			return parts[i]
		}
		return ""
	}
	switch {
	case host == "studio.youtube.com":
		for i, p := range parts {
			if p == "video" && idPattern.MatchString(at(i+1)) {
				return at(i + 1), true
			}
		}
	case host == "youtu.be":
		if idPattern.MatchString(at(0)) {
			return at(0), true
		}
	case host == "youtube.com" || strings.HasSuffix(host, ".youtube.com"):
		if v := u.Query().Get("v"); idPattern.MatchString(v) {
			return v, true
		}
		switch at(0) {
		case "shorts", "live", "embed":
			if idPattern.MatchString(at(1)) {
				return at(1), true
			}
		}
	}
	return "", false
}
