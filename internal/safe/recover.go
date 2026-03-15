package safe

import (
	"log"
	"runtime/debug"
)

// Recover logs a panic and stack trace. It does not handle fatal runtime throws.
func Recover(name string) {
	if r := recover(); r != nil {
		log.Printf("[panic] scope=%s err=%v\n%s", name, r, debug.Stack())
	}
}

// Go starts fn in a goroutine with panic logging.
func Go(name string, fn func()) {
	go func() {
		defer Recover(name)
		fn()
	}()
}
