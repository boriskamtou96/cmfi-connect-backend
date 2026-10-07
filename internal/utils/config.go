package utils

import (
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	DB     DBConfig
	Server ServerConfig
	Token  TokenConfig
}

type DBConfig struct {
	Driver   string
	Host     string
	Port     string
	Name     string
	Username string
	Password string
	User     string
	SSLMode  string
	Dsn      string
}

type ServerConfig struct {
	Port    string
	GinMode string
}

type TokenConfig struct {
	SecretKey string
	Duration  string
}

// LoadConfig loads the configuration from environment variables and returns a Config struct.
func LoadConfig() (*Config, error) {
	return (&Config{}).loadConfig()
}

func (c *Config) LoadConfig() (*Config, error) {
	return c.loadConfig()
}

func (c *Config) loadConfig() (*Config, error) {
	_ = godotenv.Load()

	config := &Config{
		DB: DBConfig{
			Driver:   getEnv("DB_DRIVER", "postgres"),
			Host:     getEnv("DB_HOST", "localhost"),
			Port:     getEnv("DB_PORT", "5432"),
			Name:     getEnv("DB_NAME", "cmfi_connect"),
			User:     getEnv("DB_USER", "postgres"),
			Password: getEnv("DB_PASSWORD", "postgres"),
			SSLMode:  getEnv("DB_SSLMODE", "disable"),
			Dsn:      getEnv("DB_DSN", "postgresql://postgres:postgres@localhost:5432/cmfi_connect?sslmode=disable"),
		},
		Server: ServerConfig{
			Port:    getEnv("PORT", "8080"),
			GinMode: getEnv("GIN_MODE", "debug"),
		},
		Token: TokenConfig{
			SecretKey: getEnv("TOKEN_SECRET", ""),
			Duration:  getEnv("TOKEN_DURATION", "168h"),
		},
	}
	return config, nil
}

func getEnv(key string, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
