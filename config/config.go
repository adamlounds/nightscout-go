package config

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	postgres "github.com/adamlounds/nightscout-go/stores/postgres"
	"github.com/thanos-io/objstore/providers/s3"
	"gopkg.in/yaml.v2"
	"log/slog"
	"os"
	"strings"
)

var logLevels = map[string]slog.Level{
	"debug": slog.LevelDebug,
	"info":  slog.LevelInfo,
	"warn":  slog.LevelWarn,
	"error": slog.LevelError,
}

// ServerConfig is the root config for a nightscout server
type ServerConfig struct {
	APISecretHash string
	DefaultRole   string
	PSQL          postgres.PostgresConfig
	S3Config      s3.Config
	Server        struct {
		Address string
	}
	LogLevel slog.Level
}

// RegisterEnv registers config from the environment
func (c *ServerConfig) RegisterEnv() error {
	c.PSQL = postgres.PostgresConfig{
		Host:     os.Getenv("POSTGRES_HOST"),
		Port:     os.Getenv("POSTGRES_PORT"),
		User:     os.Getenv("POSTGRES_USER"),
		Password: os.Getenv("POSTGRES_PASSWORD"),
		Database: os.Getenv("POSTGRES_DB"),
		SSLMode:  os.Getenv("POSTGRES_SSLMODE"),
	}
	c.Server.Address = os.Getenv("SERVER_ADDRESS")

	// authn may be performed using a sha1 of API_SECRET
	apiSecret := os.Getenv("API_SECRET")
	hasher := sha1.New()
	hasher.Write([]byte(apiSecret))
	c.APISecretHash = hex.EncodeToString(hasher.Sum(nil))
	c.DefaultRole = os.Getenv("DEFAULT_ROLE")
	if c.DefaultRole == "" {
		c.DefaultRole = "readable"
	}

	logLevel, ok := logLevels[strings.ToLower(os.Getenv("LOG_LEVEL"))]
	if !ok {
		logLevel = slog.LevelInfo
	}
	c.LogLevel = logLevel

	// nb "yaml is a superset of json", so we can load json from env while
	// using the standard Thanos yaml code
	// future: support additional object stores,
	// see https://github.com/thanos-io/objstore/blob/main/client/factory.go
	var s3Config s3.Config
	err := yaml.Unmarshal([]byte(os.Getenv("S3_CONFIG")), &s3Config)
	if err != nil {
		return fmt.Errorf("cannot parse S3 config: %w", err)
	}
	c.S3Config = s3Config

	return nil
}
