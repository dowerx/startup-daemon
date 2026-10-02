package config

import (
	"os"
)

// Environment variable names and default values used to configure the daemon.
const (
	DEFAULT_LISTEN_ADDRESS = ":8080"
	LISTEN_ADDRESS_ENV     = "LISTEN_ADDRESS"
	POKE_TOKEN_ENV         = "POKE_TOKEN"
	LABEL_PREFIX_ENV       = "LABEL_PREFIX"
	DEFAULT_LABEL_PREFIX   = "startup"
)

// Config contains the daemon's runtime configuration.
type Config struct {
	ListenAddress string
	PokeToken     string
	LabelPrefix   string
}

var config *Config = nil

// Get returns the process configuration, loading environment overrides once.
func Get() Config {
	if config == nil {
		config = &Config{
			ListenAddress: DEFAULT_LISTEN_ADDRESS,
			PokeToken:     os.Getenv(POKE_TOKEN_ENV),
			LabelPrefix:   DEFAULT_LABEL_PREFIX,
		}

		if listenAddress, ok := os.LookupEnv(LISTEN_ADDRESS_ENV); ok {
			config.ListenAddress = listenAddress
		}
		if labelPrefix, ok := os.LookupEnv(LABEL_PREFIX_ENV); ok && labelPrefix != "" {
			config.LabelPrefix = labelPrefix
		}
	}

	return *config
}
