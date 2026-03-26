package meshlog

import (
	"log/slog"
	"os"
	"sync"
)

var (
	rootLock sync.RWMutex
	root     Logger
)

func init() {
	root = NewLogger(DiscardHandler())
}

// SetDefault 设置全局根 Logger；若 l 为 *logger（内部实现），同时 sync slog.SetDefault 以便与标准 slog 互操作。
func SetDefault(l Logger) {
	rootLock.Lock()
	defer rootLock.Unlock()
	if l == nil {
		root = NewLogger(DiscardHandler())
		slog.SetDefault(slog.New(DiscardHandler()))
		return
	}
	root = l
	if lg, ok := l.(*logger); ok && lg != nil && lg.inner != nil {
		slog.SetDefault(lg.inner)
	}
}

// Root 返回当前根 Logger。
func Root() Logger {
	rootLock.RLock()
	defer rootLock.RUnlock()
	return root
}

// New 返回带有额外属性的 Logger（等同 Root().With(ctx...)）。
func New(ctx ...interface{}) Logger {
	return Root().With(ctx...)
}

// Trace 等同 Root().Write(LevelTrace, ...)。
func Trace(msg string, ctx ...interface{}) {
	Root().Write(LevelTrace, msg, ctx...)
}

func Debug(msg string, ctx ...interface{}) {
	Root().Write(slog.LevelDebug, msg, ctx...)
}

func Info(msg string, ctx ...interface{}) {
	Root().Write(slog.LevelInfo, msg, ctx...)
}

func Warn(msg string, ctx ...interface{}) {
	Root().Write(slog.LevelWarn, msg, ctx...)
}

func Error(msg string, ctx ...interface{}) {
	Root().Write(slog.LevelError, msg, ctx...)
}

// Crit 记录后 os.Exit(1)。
func Crit(msg string, ctx ...interface{}) {
	Root().Write(LevelCrit, msg, ctx...)
	os.Exit(1)
}
