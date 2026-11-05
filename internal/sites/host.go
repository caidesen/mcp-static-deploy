package sites

import (
	"crypto/rand"
	"net"
	"net/url"
	"regexp"
	"strings"
)

const (
	slugLen = 6
	slugABC = "abcdefghijklmnopqrstuvwxyz"
)

var slugRE = regexp.MustCompile(`^[a-z]{6}$`)

// ValidSlug reports whether s is a site slug this server generates.
func ValidSlug(s string) bool {
	return slugRE.MatchString(s)
}

// RandomSlug returns a short lowercase slug such as "alksgi".
func RandomSlug() (string, error) {
	buf := make([]byte, slugLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	for i := range buf {
		buf[i] = slugABC[int(buf[i])%len(slugABC)]
	}
	return string(buf), nil
}

// NormalizeDomain lowercases a host and drops a trailing dot or port.
func NormalizeDomain(d string) string {
	d = strings.ToLower(strings.TrimSpace(d))
	d = strings.TrimSuffix(d, ".")
	if host, port, err := net.SplitHostPort(d); err == nil && port != "" {
		d = host
	}
	return strings.TrimSuffix(d, ".")
}

// SlugFromHost returns the site slug when hostport is {slug}.{baseDomain}.
// The apex host, extra labels, and any other host return ok=false.
func SlugFromHost(hostport, baseDomain string) (string, bool) {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.ToLower(strings.TrimSpace(host))
	host = strings.TrimPrefix(host, "[")
	host = strings.TrimSuffix(host, "]")
	host = strings.TrimSuffix(host, ".")
	base := NormalizeDomain(baseDomain)
	if host == "" || base == "" || host == base {
		return "", false
	}
	suffix := "." + base
	if !strings.HasSuffix(host, suffix) {
		return "", false
	}
	label := strings.TrimSuffix(host, suffix)
	if strings.Contains(label, ".") || !ValidSlug(label) {
		return "", false
	}
	return label, true
}

// PublicURL is the URL browsers use for a published site.
// Scheme and port come from baseURL; the host is {slug}.{baseDomain}.
func PublicURL(baseURL, baseDomain, slug string) string {
	scheme := "https"
	var port string
	if u, err := url.Parse(baseURL); err == nil && u.Scheme != "" {
		scheme = u.Scheme
		port = u.Port()
	}
	host := slug + "." + NormalizeDomain(baseDomain)
	if port != "" {
		host = net.JoinHostPort(host, port)
	}
	return scheme + "://" + host + "/"
}
