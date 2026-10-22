package utils

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/boriskamtou96/cmfi-connect-backend/internal/media"
	"github.com/joho/godotenv"
)

type Config struct {
	DB     DBConfig
	Server ServerConfig
	Token  TokenConfig
	Media  MediaConfig
	Push   PushConfig
	Pay    PaymentsConfig
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

// ServerConfig: [CORSOrigins] (CORS_ORIGINS, comma-separated:
// "https://admin.cmfi-connect.com,https://cmfi-connect.com") are the sites
// whose browsers may call the API, the web back office on its own domain.
// Without them, outside release mode, http://localhost and http://127.0.0.1
// on any port; in release, none (the apps don't need it).
// [LogLevel] (LOG_LEVEL): debug, info (by default), warn or error.
type ServerConfig struct {
	Port        string
	GinMode     string
	CORSOrigins []string
	LogLevel    string
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
	// ffmpeg, to compress the sounds of the teachings (FFMPEG_PATH; empty:
	// the one of the PATH, if any)
	FFmpegPath string
}

// PushConfig: the notifications sent to the phones. Without
// [CredentialsFile] (the key of a Firebase service account, kept out of git)
// no push leaves; [PublicURL] ("https://api.cmfi-connect.com") makes the links
// of the images the phones fetch.
type PushConfig struct {
	CredentialsFile string
	PublicURL       string
}

// PaymentsConfig: the payments by mobile money (KPAY-NOW). Without
// [KPayAPIKey] and [KPaySecretKey] (KPAY_API_KEY, kpay_test_… in the sandbox,
// kpay_live_… in production; KPAY_SECRET_KEY) nobody can pay. Its webhooks
// are believed only when signed with [KPayWebhookSecret]
// (KPAY_WEBHOOK_SECRET). [KPayProviders] (KPAY_PROVIDERS) are the operators
// offered, those ticked in the KPAY application. [PremiumPrices] (PREMIUM_PRICES, "monthly:1500,yearly:12000",
// in [PremiumCurrency]) are the Premium plans sold on the website; Premium is
// then given in RevenueCat with [RevenueCatSecretKey] (REVENUECAT_SECRET_KEY,
// sk_…) to the entitlement [RevenueCatEntitlement].
type PaymentsConfig struct {
	KPayURL           string
	KPayAPIKey        string
	KPaySecretKey     string
	KPayWebhookSecret string
	KPayProviders     []string
	// cards (Visa, Mastercard) on KPAY's hosted page: KPAY_CARDS=off
	// when the KPAY application doesn't take them
	KPayCards bool
	// where a payer comes back after the card page (PAYMENT_RETURN_URL); a
	// page of the site that says to go back to the app
	ReturnURL             string
	PremiumPrices         map[string]int64
	PremiumCurrency       string
	RevenueCatSecretKey   string
	RevenueCatEntitlement string
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
			Port:        getEnv("PORT", "8080"),
			GinMode:     getEnv("GIN_MODE", "debug"),
			CORSOrigins: getEnvList("CORS_ORIGINS"),
			LogLevel:    getEnv("LOG_LEVEL", "info"),
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
			FFmpegPath:        getEnv("FFMPEG_PATH", ""),
		},
		Push: PushConfig{
			CredentialsFile: getEnv("FIREBASE_CREDENTIALS_FILE", ""),
			PublicURL:       getEnv("PUBLIC_URL", ""),
		},
	}
	prices, err := parsePrices(os.Getenv("PREMIUM_PRICES"))
	if err != nil {
		return nil, err
	}
	config.Pay = PaymentsConfig{
		KPayURL:               getEnv("KPAY_BASE_URL", "https://admin.kpay.site"),
		KPayAPIKey:            getEnv("KPAY_API_KEY", ""),
		KPaySecretKey:         getEnv("KPAY_SECRET_KEY", ""),
		KPayWebhookSecret:     getEnv("KPAY_WEBHOOK_SECRET", ""),
		KPayProviders:         getEnvList("KPAY_PROVIDERS"),
		KPayCards:             getEnv("KPAY_CARDS", "on") != "off",
		ReturnURL:             getEnv("PAYMENT_RETURN_URL", "https://cmfi-connect.com/paiement"),
		PremiumPrices:         prices,
		PremiumCurrency:       getEnv("PREMIUM_CURRENCY", "XAF"),
		RevenueCatSecretKey:   getEnv("REVENUECAT_SECRET_KEY", ""),
		RevenueCatEntitlement: getEnv("REVENUECAT_ENTITLEMENT", "cmfi_connect_pro"),
	}
	return config, nil
}

// parsePrices reads "monthly:1500,yearly:12000": a plan and its price, in
// whole units of the currency.
func parsePrices(value string) (map[string]int64, error) {
	prices := map[string]int64{}
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item == "" {
			continue
		}
		plan, amount, ok := strings.Cut(item, ":")
		price, err := strconv.ParseInt(strings.TrimSpace(amount), 10, 64)
		if !ok || err != nil || price <= 0 {
			return nil, fmt.Errorf("PREMIUM_PRICES: %q is not plan:price", item)
		}
		prices[strings.TrimSpace(plan)] = price
	}
	return prices, nil
}

func getEnv(key string, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// getEnvList reads a comma-separated variable, each value trimmed, the empty
// ones left out.
func getEnvList(key string) []string {
	values := []string{}
	for _, value := range strings.Split(os.Getenv(key), ",") {
		if value = strings.TrimSpace(value); value != "" {
			values = append(values, value)
		}
	}
	return values
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
