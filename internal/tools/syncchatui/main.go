package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	wd, err := os.Getwd()
	if err != nil {
		fail("getwd: %v", err)
	}
	src := filepath.Clean(filepath.Join(wd, "..", "..", "web", "chat", "index.html"))
	dstDir := filepath.Join(wd, "chat")
	dst := filepath.Join(dstDir, "index.html")
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		fail("mkdir chat ui dir: %v", err)
	}
	data, err := os.ReadFile(src)
	if err != nil {
		fail("read source chat ui: %v", err)
	}
	if existing, err := os.ReadFile(dst); err == nil && bytes.Equal(existing, data) {
		return
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		fail("write embedded chat ui: %v", err)
	}
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
