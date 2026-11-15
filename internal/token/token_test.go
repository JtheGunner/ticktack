package token

import (
	"errors"
	"testing"

	"github.com/zalando/go-keyring"
)

func TestTokenRoundTrip(t *testing.T) {
	keyring.MockInit()
	if _, err := Get(); !errors.Is(err, ErrNotSet) {
		t.Fatalf("want ErrNotSet, got %v", err)
	}
	if err := Set(""); err == nil {
		t.Fatal("empty token must be rejected")
	}
	if err := Set("perm:secret"); err != nil {
		t.Fatal(err)
	}
	got, err := Get()
	if err != nil || got != "perm:secret" {
		t.Fatalf("got %q, %v", got, err)
	}
}
