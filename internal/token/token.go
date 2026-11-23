// Package token stores the YouTrack token exclusively in the OS keychain.
package token

import (
	"errors"
	"fmt"

	"github.com/zalando/go-keyring"
)

const (
	keychainService = "ticktack"
	keychainUser    = "youtrack-token"
)

// ErrNotSet is returned by Get when no token is stored in the keychain.
var ErrNotSet = errors.New("no YouTrack token stored in the keychain")

// Get reads the YouTrack token from the OS keychain.
func Get() (string, error) {
	secret, err := keyring.Get(keychainService, keychainUser)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", ErrNotSet
	}
	if err != nil {
		return "", fmt.Errorf("read token from keychain: %w", err)
	}
	return secret, nil
}

// Delete removes the YouTrack token from the OS keychain. It returns
// ErrNotSet when no token is stored.
func Delete() error {
	err := keyring.Delete(keychainService, keychainUser)
	if errors.Is(err, keyring.ErrNotFound) {
		return ErrNotSet
	}
	if err != nil {
		return fmt.Errorf("delete token from keychain: %w", err)
	}
	return nil
}

// Set stores the YouTrack token in the OS keychain. The token is never
// persisted anywhere else and is not included in returned errors.
func Set(secret string) error {
	if secret == "" {
		return errors.New("token must not be empty")
	}
	if err := keyring.Set(keychainService, keychainUser, secret); err != nil {
		return fmt.Errorf("write token to keychain: %w", err)
	}
	return nil
}
