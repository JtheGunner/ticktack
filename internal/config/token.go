package config

import (
	"errors"
	"fmt"

	"github.com/zalando/go-keyring"
)

const (
	keychainService = "ticktack"
	keychainUser    = "youtrack-token"
)

// ErrTokenNotSet is returned by GetToken when no token is stored in the keychain.
var ErrTokenNotSet = errors.New("no YouTrack token stored in the keychain")

// GetToken reads the YouTrack token from the OS keychain.
func GetToken() (string, error) {
	secret, err := keyring.Get(keychainService, keychainUser)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", ErrTokenNotSet
	}
	if err != nil {
		return "", fmt.Errorf("read token from keychain: %w", err)
	}
	return secret, nil
}

// SetToken stores the YouTrack token in the OS keychain. The token is never
// persisted anywhere else and is not included in returned errors.
func SetToken(secret string) error {
	if secret == "" {
		return errors.New("token must not be empty")
	}
	if err := keyring.Set(keychainService, keychainUser, secret); err != nil {
		return fmt.Errorf("write token to keychain: %w", err)
	}
	return nil
}
