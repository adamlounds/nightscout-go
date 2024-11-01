package config

type ServerConfig struct {
	PSQL   PostgresConfig
	Server struct {
		Address string
	}
}
