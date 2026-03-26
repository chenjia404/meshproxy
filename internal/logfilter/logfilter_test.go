package logfilter

import (
	"bytes"
	"strings"
	"testing"
)

func TestParseModules(t *testing.T) {
	if parseModules("") != nil {
		t.Fatal("empty should be nil")
	}
	if parseModules("  ") != nil {
		t.Fatal("spaces only should be nil")
	}
	got := parseModules(" chat , Path_Selector ")
	if len(got) != 2 || got[0] != "chat" || got[1] != "path_selector" {
		t.Fatalf("got %#v", got)
	}
}

func TestNewWriterFiltersByModule(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf, "chat")
	if _, err := w.Write([]byte("2024/01/01 12:00:00 [chat] ok\n")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "ok") {
		t.Fatalf("want chat line, got %q", buf.String())
	}
	buf.Reset()
	if _, err := w.Write([]byte("2024/01/01 12:00:00 [p2p] noise\n")); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Fatalf("p2p line should drop, got %q", buf.String())
	}
}

func TestNewWriterPassesUnprefixedLines(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf, "chat")
	if _, err := w.Write([]byte("meshproxy starting no bracket\n")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "starting") {
		t.Fatalf("unprefixed should pass, got %q", buf.String())
	}
}

func TestNewWriterChunkedLine(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf, "chat")
	if _, err := w.Write([]byte("2024/01/01 12:00:00 [ch")); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("at] split\n")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "split") {
		t.Fatalf("chunked write should assemble line, got %q", buf.String())
	}
}

func TestNewWriterEmptyMeansNoFilter(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf, "")
	if _, err := w.Write([]byte("plain")); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "plain" {
		t.Fatalf("empty filter should write through, got %q", buf.String())
	}
}
