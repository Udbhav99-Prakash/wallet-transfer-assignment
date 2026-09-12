package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// ErrMissingDatabaseURL is returned when DATABASE_URL is omitted in non-development environments.
var ErrMissingDatabaseURL = errors.New("DATABASE_URL must be explicitly configured in non-development environments")

// ErrMissingAdminKey is returned when ADMIN_KEY is omitted in non-development environments.
var ErrMissingAdminKey = errors.New("ADMIN_KEY must be explicitly configured in non-development environments")

// Config holds runtime configuration settings.
type Config struct {
	Environment string
	Port        string
	DatabaseURL string
	AdminKey    string
}

// Load reads configuration from environment variables.
// In non-development environments (e.g. production, staging), an explicitly configured
// DATABASE_URL and ADMIN_KEY are strictly required to prevent accidentally running with default credentials.
// In local development, it defaults to a least-privileged application user ('wallet_app')
// and a local development admin key ('admin-secret-dev').
func Load() (*Config, error) {
	env := os.Getenv("APP_ENV")
	if env == "" {
		env = os.Getenv("ENV")
	}
	if env == "" {
		env = "development"
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		if isNonDevelopment(env) {
			return nil, fmt.Errorf("%w (environment: %s)", ErrMissingDatabaseURL, env)
		}

		// Use least-privileged application user ('wallet_app') for local development
		// rather than the PostgreSQL superuser ('postgres').
		dbURL = "postgres://wallet_app:wallet_app_password@localhost:5432/wallet_db?sslmode=disable"
	}

	adminKey := os.Getenv("ADMIN_KEY")
	if adminKey == "" {
		adminKey = os.Getenv("ADMIN_API_KEY")
	}
	if adminKey == "" {
		if isNonDevelopment(env) {
			return nil, fmt.Errorf("%w (environment: %s)", ErrMissingAdminKey, env)
		}
		adminKey = "admin-secret-dev"
	}

	return &Config{
		Environment: env,
		Port:        port,
		DatabaseURL: dbURL,
		AdminKey:    adminKey,
	}, nil
}

func isNonDevelopment(env string) bool {
	normalized := strings.ToLower(strings.TrimSpace(env))
	switch normalized {
	case "development", "dev", "local", "test":
		return false
	default:
		return true
	}
}
