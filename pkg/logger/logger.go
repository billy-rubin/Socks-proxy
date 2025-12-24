package logger

import (
	"log/slog"
	"os"
)

// Setup инициализирует логгер. Можно сделать JSON или Text формат.
func Setup() *slog.Logger {
	opts := &slog.HandlerOptions{
		Level: slog.LevelDebug, // Чтобы видеть всё
	}
	// Используем TextHandler для читаемости в консоли (или JSONHandler для продакшена)
	handler := slog.NewTextHandler(os.Stdout, opts)
	return slog.New(handler)
}
