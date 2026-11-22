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

func TestDeleteRemovesTheToken(t *testing.T) {
	keyring.MockInit()
	if err := Delete(); !errors.Is(err, ErrNotSet) {
		t.Fatalf("deleting a missing token: want ErrNotSet, got %v", err)
	}
	if err := Set("perm:secret"); err != nil {
		t.Fatal(err)
	}
	if err := Delete(); err != nil {
		t.Fatal(err)
	}
	if _, err := Get(); !errors.Is(err, ErrNotSet) {
		t.Fatalf("token still readable after Delete: %v", err)
	}
}

func TestDeleteReportsKeychainFailure(t *testing.T) {
	failure := errors.New("keychain locked")
	keyring.MockInitWithError(failure)
	t.Cleanup(keyring.MockInit)
	if err := Delete(); !errors.Is(err, failure) || errors.Is(err, ErrNotSet) {
		t.Fatalf("want the keychain failure, got %v", err)
	}
}
