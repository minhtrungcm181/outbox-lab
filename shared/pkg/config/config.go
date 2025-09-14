package config

import (
	"log"
	"os"

	"github.com/joho/godotenv"
)

// Config holds all service configuration values
type Config struct {
	HTTPPort     string // e.g. ":8080"
	DBDSN        string // postgres://user:pass@host:5432/db?sslmode=disable
	KafkaBrokers string // e.g. "localhost:9092,localhost:9093"
	Env          string // "dev" | "staging" | "prod"
	Kafka        string
}

// MustLoad loads configuration from environment variables.
// If a required variable is missing, the service will exit immediately.
func MustLoad() Config {
	_ = godotenv.Load("../../../.env")
	//_ = godotenv.Load(".env")
	cfg := Config{
		HTTPPort: getEnv("PORT", "8080"),
		DBDSN:    getEnv("DB_DSN", ""),     // required → will check below
		Env:      getEnv("APP_ENV", "dev"), // default to dev
		Kafka:    getEnv("KAFKA_BROKERS", ""),
	}

	// Fail fast if required vars are missing
	if cfg.DBDSN == "" {
		log.Fatal("missing required env: DB_DSN")
	}

	return cfg
}

func getEnv(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}
