package loopback

import (
	"errors"
	"strings"
	"testing"
)

func TestCheckURL(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		ok   bool
	}{
		{"localhost", "http://localhost:5600", true},
		{"localhost upper case", "http://LOCALHOST:5600/", true},
		{"ipv4 loopback", "http://127.0.0.1:5600", true},
		{"ipv4 loopback range", "http://127.5.6.7", true},
		{"ipv6 loopback", "http://[::1]:5600", true},
		{"https", "https://localhost", true},
		{"remote host", "http://example.com:5600", false},
		{"private address", "http://192.168.1.2:5600", false},
		{"unspecified address", "http://0.0.0.0:5600", false},
		{"look-alike suffix", "http://localhost.evil.com", false},
		{"look-alike prefix", "http://evil-localhost", false},
		{"userinfo trick", "http://localhost@evil.com", false},
		{"userinfo on loopback", "http://user:pw@localhost:5600", false},
		{"loopback in userinfo", "http://127.0.0.1:80@evil.com", false},
		{"ip look-alike", "http://127.0.0.1.evil.com", false},
		{"unsupported scheme", "ftp://localhost", false},
		{"no scheme", "localhost:5600", false},
		{"empty", "", false},
		{"garbage", "http://[::1", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := CheckURL(tt.raw)
			if tt.ok && err != nil {
				t.Fatalf("CheckURL(%q) = %v, want ok", tt.raw, err)
			}
			if !tt.ok && !errors.Is(err, ErrNotLoopback) {
				t.Fatalf("CheckURL(%q) = %v, want ErrNotLoopback", tt.raw, err)
			}
		})
	}
}

func TestCheckURLErrorDoesNotEchoInput(t *testing.T) {
	_, err := CheckURL("http://user:secret-pw@evil.com")
	if err == nil || strings.Contains(err.Error(), "secret-pw") || strings.Contains(err.Error(), "evil.com") {
		t.Fatalf("error leaks input: %v", err)
	}
}

func TestControl(t *testing.T) {
	for addr, ok := range map[string]bool{
		"127.0.0.1:5600":       true,
		"[::1]:5600":           true,
		"[::ffff:127.0.0.1]:1": true,
		"8.8.8.8:80":           false,
		"[2001:db8::1]:80":     false,
		"localhost:80":         false,
		"nonsense":             false,
	} {
		err := Control("tcp", addr, nil)
		if ok != (err == nil) {
			t.Errorf("Control(%q) = %v, want ok=%v", addr, err, ok)
		}
	}
}
