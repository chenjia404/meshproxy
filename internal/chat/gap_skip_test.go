package chat

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
	"testing"
)

func TestSyncFailureAllowsOfflineGapSkip(t *testing.T) {
	t.Parallel()
	if syncFailureAllowsOfflineGapSkip(nil) {
		t.Fatal("nil must be false")
	}
	if syncFailureAllowsOfflineGapSkip(context.Canceled) {
		t.Fatal("canceled must be false")
	}
	if !syncFailureAllowsOfflineGapSkip(context.DeadlineExceeded) {
		t.Fatal("deadline must be true")
	}
	if !syncFailureAllowsOfflineGapSkip(os.ErrDeadlineExceeded) {
		t.Fatal("os deadline must be true")
	}
	if !syncFailureAllowsOfflineGapSkip(errors.New("chat sync relay timeout")) {
		t.Fatal("relay timeout must be true")
	}
	if syncFailureAllowsOfflineGapSkip(fmt.Errorf("chat sync made no progress x")) {
		t.Fatal("made no progress must be false")
	}
	if syncFailureAllowsOfflineGapSkip(fmt.Errorf("chat sync exceeded max rounds x")) {
		t.Fatal("max rounds must be false")
	}
	if !syncFailureAllowsOfflineGapSkip(fmt.Errorf("wrap: %w", context.DeadlineExceeded)) {
		t.Fatal("wrapped deadline must be true")
	}
	opErr := &net.OpError{Err: syscall.ECONNREFUSED}
	if !syncFailureAllowsOfflineGapSkip(opErr) {
		t.Fatal("ECONNREFUSED op must be true")
	}
}
