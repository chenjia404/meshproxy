// Package logfilter 提供按日志模块名（如 [chat]）过滤标准库 log 输出的能力。
package logfilter

import (
	"bytes"
	"io"
	"log"
	"os"
	"regexp"
	"strings"
)

// 匹配日志中第一个形如 [module_name] 的标签（模块名仅含字母数字与 _-）。
var bracketModuleTag = regexp.MustCompile(`\[([a-zA-Z0-9_-]+)\]`)

// Apply 根据逗号分隔的模块名列表设置全局 log 输出；空字符串表示不过滤（直接写入 stderr）。
// 模块名与代码中 log.Printf("[name] ...") 的 name 对应，大小写不敏感。
func Apply(modulesCSV string) {
	log.SetOutput(NewWriter(os.Stderr, modulesCSV))
}

// NewWriter 返回写入 dst 的 Writer；modulesCSV 为空时返回 dst 本身。
func NewWriter(dst io.Writer, modulesCSV string) io.Writer {
	if dst == nil {
		dst = io.Discard
	}
	mods := parseModules(modulesCSV)
	if len(mods) == 0 {
		return dst
	}
	set := make(map[string]struct{}, len(mods))
	for _, m := range mods {
		set[m] = struct{}{}
	}
	return &lineFilterWriter{dst: dst, allow: set}
}

func parseModules(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, strings.ToLower(p))
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

type lineFilterWriter struct {
	dst   io.Writer
	allow map[string]struct{}
	buf   []byte
}

func (w *lineFilterWriter) Write(p []byte) (int, error) {
	if w == nil || w.dst == nil {
		return len(p), nil
	}
	w.buf = append(w.buf, p...)
	written := len(p)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		line := w.buf[:i+1]
		w.buf = w.buf[i+1:]
		if w.lineAllowed(line) {
			if _, err := w.dst.Write(line); err != nil {
				return written, err
			}
		}
	}
	return written, nil
}

func (w *lineFilterWriter) lineAllowed(line []byte) bool {
	if w == nil || len(w.allow) == 0 {
		return true
	}
	tag, ok := firstModuleTag(line)
	if !ok {
		// 无 [module] 标签的行（如启动提示、无前缀日志）始终输出，避免过滤后「完全静音」。
		return true
	}
	_, hit := w.allow[strings.ToLower(tag)]
	return hit
}

func firstModuleTag(line []byte) (string, bool) {
	m := bracketModuleTag.FindSubmatch(line)
	if len(m) < 2 {
		return "", false
	}
	return string(m[1]), true
}
