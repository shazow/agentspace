package loghandler

import (
	"io"
	"log/slog"
	"os"

	"github.com/lmittmann/tint"
)

type Options = tint.Options

func NewHandler(w io.Writer, opts *Options) slog.Handler {
	return tint.NewHandler(w, opts)
}

func NewLogger(w io.Writer, opts *Options) *slog.Logger {
	return slog.New(NewHandler(w, opts))
}

func ColorEnabled(file *os.File) bool {
	if _, ok := os.LookupEnv("NO_COLOR"); ok {
		return false
	}
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
