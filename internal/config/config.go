package config

import (
	"os"
)

// Config holds runtime configuration settings.
type Config struct {
	Port        string
	DatabaseURL string
}

// Load reads configuration from environment variables with sensible defaults.
func Load() *Config {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://postgres:postgrespassword@localhost:5432/wallet_db?sslmode=disable"
	}

	return &Config{
		Port:        port,
		DatabaseURL: dbURL,
	}
}
