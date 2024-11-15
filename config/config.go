package config

import (
	pgstore "github.com/adamlounds/nightscout-go/stores/postgres"
	"log/slog"
)

type ServerConfig struct {
	APISecretHash string
	DefaultRole   string
	PSQL          pgstore.PostgresConfig
	Server        struct {
		Address string
	}
	LogLevel slog.Level
}
