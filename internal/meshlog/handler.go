package meshlog

import (
	"context"
	"io"
	"log/slog"
	"os"
)

type discardHandler struct{}

// DiscardHandler 返回不输出任何内容的 Handler（与 go-ethereum 默认根日志一致，需 SetDefault 后才有输出）。
func DiscardHandler() slog.Handler {
	return &discardHandler{}
}

func (h *discardHandler) Handle(_ context.Context, _ slog.Record) error { return nil }
func (h *discardHandler) Enabled(_ context.Context, _ slog.Level) bool { return false }
func (h *discardHandler) WithGroup(string) slog.Handler                { return h }
func (h *discardHandler) WithAttrs([]slog.Attr) slog.Handler          { return &discardHandler{} }

type levelVar struct{ min slog.Level }

func (v *levelVar) Level() slog.Level { return v.min }

// NewTextHandler 使用 slog 文本格式写入 wr，仅输出不低于 minLevel 的记录（slog 中数值越大越严重：Info < Warn < Error）。
// 适合作为 SetDefault(NewLogger(NewTextHandler(os.Stderr, meshlog.LevelInfo))) 的便捷构造。
func NewTextHandler(w io.Writer, minLevel slog.Level) slog.Handler {
	if w == nil {
		w = io.Discard
	}
	return slog.NewTextHandler(w, &slog.HandlerOptions{
		Level:     &levelVar{min: minLevel},
		AddSource: false,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.LevelKey {
				if lv, ok := a.Value.Any().(slog.Level); ok {
					return slog.String(slog.LevelKey, LevelString(lv))
				}
			}
			return a
		},
	})
}

// NewJSONHandler JSON 行日志，便于采集。
func NewJSONHandler(w io.Writer, minLevel slog.Level) slog.Handler {
	if w == nil {
		w = io.Discard
	}
	return slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level: &levelVar{min: minLevel},
	})
}

// StderrTextHandler 等同 NewTextHandler(os.Stderr, minLevel)。
func StderrTextHandler(minLevel slog.Level) slog.Handler {
	return NewTextHandler(os.Stderr, minLevel)
}
