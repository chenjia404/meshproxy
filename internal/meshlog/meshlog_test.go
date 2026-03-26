package meshlog

import (
	"bytes"
	"strings"
	"testing"
)

func TestSetDefaultTextHandler(t *testing.T) {
	var buf bytes.Buffer
	SetDefault(NewLogger(NewTextHandler(&buf, LevelInfo)))
	r := Root()
	r.Info("hello", "k", "v")
	if !strings.Contains(buf.String(), "hello") {
		t.Fatalf("expected log output, got %q", buf.String())
	}
}

func TestSubLoggerWithModule(t *testing.T) {
	var buf bytes.Buffer
	SetDefault(NewLogger(NewTextHandler(&buf, LevelDebug)))
	l := New("module", "chat")
	l.Debug("fetch", "seq", 1)
	s := buf.String()
	if !strings.Contains(s, "chat") || !strings.Contains(s, "fetch") {
		t.Fatalf("unexpected output: %q", s)
	}
}

func TestLevelString(t *testing.T) {
	if LevelString(LevelTrace) != "trace" {
		t.Fatal(LevelString(LevelTrace))
	}
}
