package main

import (
	"flag"
	"log/slog"
	"os"

	"scalp-assist/internal/config"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to YAML configuration file")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Error("failed to load config", "path", *configPath, "error", err)
		os.Exit(1)
	}

	slog.Info("config loaded",
		"path", *configPath,
		"domain", cfg.Domain,
		"db_path", cfg.DBPath,
		"dev_http", cfg.DevHTTP,
		"top_n", cfg.Settings.TopN,
	)
}
