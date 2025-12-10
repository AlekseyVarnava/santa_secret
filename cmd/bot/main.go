package main

import (
	"log"

	"santa/internal/config"
	"santa/internal/db"
	"santa/internal/logger"
	"santa/internal/telegram"

	"go.uber.org/zap"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config load: %v", err)
	}

	// Инициализируем логгер
	logg := logger.New(cfg.LogLevel)
	defer func() {
		_ = logg.Sync()
	}()

	logg.Info("Starting Secret Santa bot")

	// Подключение к БД
	database, err := db.Connect(cfg.DatabaseURL)
	if err != nil {
		logg.Fatal("DB connect failed", zap.Error(err))
	}
	logg.Info("Connected to database")

	// Создаём экземпляр бота, передаём db, token и logger
	bot, err := telegram.NewBot(database, cfg.BotToken, logg)
	if err != nil {
		logg.Fatal("Bot init error", zap.Error(err))
	}

	// Запуск бота (blocking)
	bot.Run()

	// При необходимости - graceful shutdown можно вызвать bot.Stop()
}
