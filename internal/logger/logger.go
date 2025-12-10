package logger

import (
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func New(level string) *zap.Logger {
	var logLevel zapcore.Level
	switch level {
	case "debug":
		logLevel = zapcore.DebugLevel
	case "info":
		logLevel = zapcore.InfoLevel
	default:
		logLevel = zapcore.InfoLevel
	}

	cfg := zap.Config{
		Level:       zap.NewAtomicLevelAt(logLevel),
		Encoding:    "json",
		OutputPaths: []string{"stdout"},
		EncoderConfig: zapcore.EncoderConfig{
			TimeKey:     "ts",
			LevelKey:    "level",
			MessageKey:  "msg",
			EncodeTime:  zapcore.ISO8601TimeEncoder,
			EncodeLevel: zapcore.CapitalLevelEncoder,
		},
	}

	logger, _ := cfg.Build()
	return logger
}
