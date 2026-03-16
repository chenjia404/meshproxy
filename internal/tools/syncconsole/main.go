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

	src := filepath.Clean(filepath.Join(wd, "..", "..", "web", "console", "index.html"))
	dst := filepath.Clean(filepath.Join(wd, "console", "index.html"))

	data, err := os.ReadFile(src)
	if err != nil {
		fail("read source console: %v", err)
	}
	if existing, err := os.ReadFile(dst); err == nil && bytes.Equal(existing, data) {
		return
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		fail("write embedded console: %v", err)
	}
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
