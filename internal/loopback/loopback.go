// Package loopback restricts URLs and connections to the local machine. It
// backs the one carve-out from ticktack's "only network call is the YouTrack
// sync" rule: the optional ActivityWatch importer may talk to a loopback
// server and nothing else.
package loopback

import (
	"errors"
	"net"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
)

// ErrNotLoopback is returned for a URL or address that is not loopback-only.
var ErrNotLoopback = errors.New("must be an http(s) URL on localhost, 127.0.0.0/8 or ::1 without credentials")

// CheckURL parses raw and verifies that it is an http or https URL without
// userinfo whose host is "localhost" or a loopback IP literal. The error never
// contains the URL, because it could carry credentials.
func CheckURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, ErrNotLoopback
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, ErrNotLoopback
	}
	if u.User != nil || u.Opaque != "" {
		return nil, ErrNotLoopback
	}
	if !isLoopbackHost(u.Hostname()) {
		return nil, ErrNotLoopback
	}
	return u, nil
}

func isLoopbackHost(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "localhost" {
		return true
	}
	addr, err := netip.ParseAddr(host)
	return err == nil && addr.IsLoopback()
}

// Control is a net.Dialer Control function that refuses to connect to any
// address that is not a loopback IP. It guards against a name such as
// "localhost" resolving to a remote address.
func Control(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return ErrNotLoopback
	}
	addr, err := netip.ParseAddr(host)
	if err != nil || !addr.Unmap().IsLoopback() {
		return ErrNotLoopback
	}
	return nil
}
