package config

import pgstore "github.com/adamlounds/nightscout-go/stores/postgres"

type ServerConfig struct {
	APISecretHash string
	DefaultRole   string
	PSQL          pgstore.PostgresConfig
	Server        struct {
		Address string
	}
}
