// Package meshlog 提供与 go-ethereum/log 类似的结构化日志 API（基于 log/slog），供新代码逐步迁移使用；与标准库 log 包并存。
//
// 用法示例：
//
//	meshlog.SetDefault(meshlog.NewLogger(meshlog.NewTextHandler(os.Stderr, meshlog.LevelInfo)))
//	l := meshlog.New("module", "chat")
//	l.Info("offline fetch ok", "peer", peerID, "seq", seq)
package meshlog

import (
	"context"
	"log/slog"
	"math"
	"os"
	"runtime"
	"time"
)

const errorKey = "LOG_ERROR"

// slog 扩展级别（与 go-ethereum/log 对齐）。
const (
	levelMaxVerbosity slog.Level = math.MinInt
	LevelTrace        slog.Level = -8
	LevelDebug        = slog.LevelDebug
	LevelInfo         = slog.LevelInfo
	LevelWarn         = slog.LevelWarn
	LevelError        = slog.LevelError
	LevelCrit         slog.Level = 12

	LvlTrace = LevelTrace
	LvlInfo  = LevelInfo
	LvlDebug = LevelDebug
)

// LevelString 返回级别的简短名称。
func LevelString(l slog.Level) string {
	switch l {
	case LevelTrace:
		return "trace"
	case slog.LevelDebug:
		return "debug"
	case slog.LevelInfo:
		return "info"
	case slog.LevelWarn:
		return "warn"
	case slog.LevelError:
		return "error"
	case LevelCrit:
		return "crit"
	default:
		return "unknown"
	}
}

// LevelAlignedString 返回 5 字符对齐的级别名（便于终端对齐）。
func LevelAlignedString(l slog.Level) string {
	switch l {
	case LevelTrace:
		return "TRACE"
	case slog.LevelDebug:
		return "DEBUG"
	case slog.LevelInfo:
		return "INFO "
	case slog.LevelWarn:
		return "WARN "
	case slog.LevelError:
		return "ERROR"
	case LevelCrit:
		return "CRIT "
	default:
		return "UNKNW"
	}
}

// Logger 与 go-ethereum/log.Logger 对齐：键值对上下文 + 分级输出。
type Logger interface {
	With(ctx ...interface{}) Logger
	New(ctx ...interface{}) Logger
	Log(level slog.Level, msg string, ctx ...interface{})
	Trace(msg string, ctx ...interface{})
	Debug(msg string, ctx ...interface{})
	Info(msg string, ctx ...interface{})
	Warn(msg string, ctx ...interface{})
	Error(msg string, ctx ...interface{})
	Crit(msg string, ctx ...interface{})
	Write(level slog.Level, msg string, attrs ...any)
	Enabled(ctx context.Context, level slog.Level) bool
	Handler() slog.Handler
}

type logger struct {
	inner *slog.Logger
}

// NewLogger 使用指定 Handler 创建 Logger。
func NewLogger(h slog.Handler) Logger {
	if h == nil {
		h = DiscardHandler()
	}
	return &logger{inner: slog.New(h)}
}

func (l *logger) Handler() slog.Handler {
	if l == nil || l.inner == nil {
		return DiscardHandler()
	}
	return l.inner.Handler()
}

// Write 写入一条记录；attrs 为 key/value 对（长度须为偶数，否则会补齐占位）。
func (l *logger) Write(level slog.Level, msg string, attrs ...any) {
	if l == nil || l.inner == nil {
		return
	}
	if !l.inner.Enabled(context.Background(), level) {
		return
	}
	var pcs [1]uintptr
	runtime.Callers(3, pcs[:])
	if len(attrs)%2 != 0 {
		attrs = append(attrs, nil, errorKey, "参数个数为奇数，已用 nil 补齐")
	}
	r := slog.NewRecord(time.Now(), level, msg, pcs[0])
	r.Add(attrs...)
	_ = l.inner.Handler().Handle(context.Background(), r)
}

func (l *logger) Log(level slog.Level, msg string, attrs ...any) {
	l.Write(level, msg, attrs...)
}

func (l *logger) With(ctx ...interface{}) Logger {
	if l == nil || l.inner == nil {
		return NewLogger(DiscardHandler())
	}
	return &logger{l.inner.With(ctx...)}
}

func (l *logger) New(ctx ...interface{}) Logger {
	return l.With(ctx...)
}

func (l *logger) Enabled(ctx context.Context, level slog.Level) bool {
	if l == nil || l.inner == nil {
		return false
	}
	return l.inner.Enabled(ctx, level)
}

func (l *logger) Trace(msg string, ctx ...interface{}) {
	l.Write(LevelTrace, msg, ctx...)
}

func (l *logger) Debug(msg string, ctx ...interface{}) {
	l.Write(slog.LevelDebug, msg, ctx...)
}

func (l *logger) Info(msg string, ctx ...interface{}) {
	l.Write(slog.LevelInfo, msg, ctx...)
}

func (l *logger) Warn(msg string, ctx ...interface{}) {
	l.Write(slog.LevelWarn, msg, ctx...)
}

func (l *logger) Error(msg string, ctx ...interface{}) {
	l.Write(slog.LevelError, msg, ctx...)
}

func (l *logger) Crit(msg string, ctx ...interface{}) {
	l.Write(LevelCrit, msg, ctx...)
	os.Exit(1)
}
