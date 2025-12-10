package config

import (
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	BotToken    string
	DatabaseURL string
	LogLevel    string
}

func Load() (*Config, error) {
	_ = godotenv.Load()

	cfg := &Config{
		BotToken:    os.Getenv("BOT_TOKEN"),
		DatabaseURL: os.Getenv("DATABASE_URL"),
		LogLevel:    os.Getenv("LOG_LEVEL"),
	}
	return cfg, nil
}
