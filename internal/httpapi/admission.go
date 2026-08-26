package httpapi

import (
	"net"
	"strings"

	"github.com/zlrrr/multi-agent-system-turbo/internal/config"
	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

// Admit reports why a configuration must not open a listener, or nil.
//
// The requirement attaches to the bind address rather than to a flag, because
// that is what actually differs between the two cases. A loopback bind is
// already protected by the host; a `0.0.0.0` bind is protected by nothing. So
// the laptop keeps working with no configuration at all, and the exposed
// deployment cannot be unsafe by someone forgetting a setting
// (specs/009-api-authentication/design-hld.md §2).
//
// It refuses rather than warns. A warning at startup is read once, by a log
// nobody is watching; a refusal is read at the moment someone is looking.
func Admit(cfg config.ServerConfig) error {
	if isLoopback(cfg.Addr) {
		return nil
	}
	if len(cfg.Auth.Tokens) == 0 {
		return errs.New("MAS-7010", describeAddr(cfg.Addr))
	}
	// A bearer token on an unencrypted connection is a token on the wire.
	// Requiring this process to terminate TLS would be wrong for the common
	// deployment, where an ingress already does — and this process cannot see
	// that ingress, so it asks the operator to say so.
	if !cfg.TLS.Enabled() && !cfg.TLS.TerminatedByProxy {
		return errs.New("MAS-7011", describeAddr(cfg.Addr))
	}
	return nil
}

// isLoopback reports whether an address can only be reached from this host.
//
// An empty host, `0.0.0.0` and `::` are every interface and so are not
// loopback. A hostname that does not resolve is treated as non-loopback: that
// is the safe direction, and it produces a legible refusal rather than a silent
// exposure.
func isLoopback(addr string) bool {
	host := addr
	if h, _, err := net.SplitHostPort(addr); err == nil {
		host = h
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return false // ":8080" binds every interface
	}
	if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil {
		return ip.IsLoopback()
	}
	ips, err := net.LookupIP(host)
	if err != nil || len(ips) == 0 {
		return false
	}
	for _, ip := range ips {
		if !ip.IsLoopback() {
			return false
		}
	}
	return true
}

func describeAddr(addr string) string {
	if strings.TrimSpace(addr) == "" {
		return "(unset)"
	}
	return addr
}
