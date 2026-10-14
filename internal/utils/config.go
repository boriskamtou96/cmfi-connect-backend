package utils

import (
	"os"

	"github.com/boriskamtou96/cmfi-connect-backend/internal/media"
	"github.com/joho/godotenv"
)

type Config struct {
	DB     DBConfig
	Server ServerConfig
	Token  TokenConfig
	Media  MediaConfig
	Push   PushConfig
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

// MediaConfig: where the images (posters, photos, profile photos) are kept.
// With [S3Bucket] they go to a bucket (Cloudflare R2 in production), else to
// the folder [Dir]. [PublicURL] ("https://media.cmfi-connect.com", the CDN in
// front of the bucket) makes their links; without it the API serves them at
// /v1/media/<key>.
type MediaConfig struct {
	Dir       string
	PublicURL string

	S3Endpoint        string
	S3Bucket          string
	S3AccessKeyID     string
	S3SecretAccessKey string
	S3Region          string
}

// PushConfig: the notifications sent to the phones. Without
// [CredentialsFile] (the key of a Firebase service account, kept out of git)
// no push leaves; [PublicURL] ("https://api.cmfi-connect.com") makes the links
// of the images the phones fetch.
type PushConfig struct {
	CredentialsFile string
	PublicURL       string
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
		Media: MediaConfig{
			Dir:               getEnv("MEDIA_DIR", "data/media"),
			PublicURL:         getEnv("MEDIA_PUBLIC_URL", ""),
			S3Endpoint:        getEnv("S3_ENDPOINT", ""),
			S3Bucket:          getEnv("S3_BUCKET", ""),
			S3AccessKeyID:     getEnv("S3_ACCESS_KEY_ID", ""),
			S3SecretAccessKey: getEnv("S3_SECRET_ACCESS_KEY", ""),
			S3Region:          getEnv("S3_REGION", "auto"),
		},
		Push: PushConfig{
			CredentialsFile: getEnv("FIREBASE_CREDENTIALS_FILE", ""),
			PublicURL:       getEnv("PUBLIC_URL", ""),
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

// S3 is the bucket of the images, if one is set.
func (m MediaConfig) S3() media.S3Config {
	return media.S3Config{
		Endpoint:        m.S3Endpoint,
		Bucket:          m.S3Bucket,
		AccessKeyID:     m.S3AccessKeyID,
		SecretAccessKey: m.S3SecretAccessKey,
		Region:          m.S3Region,
	}
}
