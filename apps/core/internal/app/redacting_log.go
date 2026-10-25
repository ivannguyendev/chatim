package app

import (
	"log/slog"

	"github.com/ivannguyendev/chatim/apps/core/internal/config"
	"github.com/ivannguyendev/chatim/pkg/redact"
)

func redactedLogger(log *slog.Logger, cfg config.Config) *slog.Logger {
	return slog.New(redact.NewHandler(log.Handler(), []string{cfg.MongoURI, cfg.NATSURL}, cfg.Secrets()))
}
