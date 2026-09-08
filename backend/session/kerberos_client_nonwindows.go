//go:build !windows

package session

import (
	unitermkrb5 "github.com/ys-ll/uniterm/backend/krb5"
	"golang.org/x/crypto/ssh"
)

func kerberosGSSAPIClient() (ssh.GSSAPIClient, error) {
	configPath, err := kerberosConfigPath()
	if err != nil {
		return nil, err
	}
	cachePath, cleanup, err := kerberosCachePath()
	if err != nil {
		return nil, err
	}
	defer cleanup()
	return unitermkrb5.NewInitiatorClientWithCache(configPath, cachePath)
}
