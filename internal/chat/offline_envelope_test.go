package chat

import (
	"testing"
	"time"
)

func TestOfflineEnvelopeCreatedUnix(t *testing.T) {
	t.Parallel()
	if got := offlineEnvelopeCreatedUnix(&OfflineMessageEnvelope{CreatedAt: 1700000000}); got != 1700000000 {
		t.Fatalf("sec: got %d", got)
	}
	if got := offlineEnvelopeCreatedUnix(&OfflineMessageEnvelope{CreatedAt: 1700000000000}); got != 1700000000 {
		t.Fatalf("ms: got %d", got)
	}
}

func TestValidateOfflineEnvelopeTiming(t *testing.T) {
	t.Parallel()
	ttl30d := int64(30 * 24 * 3600)
	now := time.Unix(2000000000, 0).UTC()
	createdOk := now.Unix() - 5*24*3600 // 5d ago

	envFriend := func(created int64) *OfflineMessageEnvelope {
		return &OfflineMessageEnvelope{
			CreatedAt: created,
			TTLSec:    &ttl30d,
			Cipher:    OfflineCipherPayload{Algorithm: OfflineFriendAlgoECIES},
		}
	}
	envChat := func(created int64) *OfflineMessageEnvelope {
		return &OfflineMessageEnvelope{
			CreatedAt: created,
			TTLSec:    &ttl30d,
			Cipher:    OfflineCipherPayload{Algorithm: "chacha20-poly1305"},
		}
	}

	t.Run("friend within window", func(t *testing.T) {
		t.Parallel()
		if err := validateOfflineEnvelopeTiming(envFriend(createdOk), nil, offlineDefaultTTLSec, now); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("friend older than 30d", func(t *testing.T) {
		t.Parallel()
		createdOld := now.Unix() - 31*24*3600
		err := validateOfflineEnvelopeTiming(envFriend(createdOld), nil, offlineDefaultTTLSec, now)
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("store expire_at passed", func(t *testing.T) {
		t.Parallel()
		w := &StoredMessageWire{ExpireAt: now.Unix() - 1}
		err := validateOfflineEnvelopeTiming(envFriend(createdOk), w, offlineDefaultTTLSec, now)
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("created_at far future", func(t *testing.T) {
		t.Parallel()
		future := now.Unix() + offlineEnvelopeFutureSkewSec + 3600
		err := validateOfflineEnvelopeTiming(envFriend(future), nil, offlineDefaultTTLSec, now)
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("chat past ttl", func(t *testing.T) {
		t.Parallel()
		created := now.Unix() - 40*24*3600
		err := validateOfflineEnvelopeTiming(envChat(created), nil, offlineDefaultTTLSec, now)
		if err == nil {
			t.Fatal("expected error")
		}
	})
}
