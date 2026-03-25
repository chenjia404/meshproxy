package publicchannel

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ipfs/go-cid"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	crypto "github.com/libp2p/go-libp2p/core/crypto"
	host "github.com/libp2p/go-libp2p/core/host"
	peer "github.com/libp2p/go-libp2p/core/peer"
	corerouting "github.com/libp2p/go-libp2p/core/routing"
	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	ma "github.com/multiformats/go-multiaddr"
)

func TestApplyMessageRejectsSameVersionDifferentSignature(t *testing.T) {
	t.Parallel()

	priv, localPeerID := mustTestIdentity(t)
	store := mustTestStore(t)
	now := time.Now().Unix()

	profile := ChannelProfile{
		ChannelID:      mustUUIDv7(t),
		OwnerPeerID:    localPeerID,
		OwnerVersion:   1,
		Name:           "test",
		ProfileVersion: 1,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := signProfile(priv, &profile); err != nil {
		t.Fatal(err)
	}
	head := ChannelHead{
		ChannelID:      profile.ChannelID,
		OwnerPeerID:    localPeerID,
		OwnerVersion:   1,
		LastMessageID:  0,
		ProfileVersion: 1,
		LastSeq:        0,
		UpdatedAt:      now,
	}
	if err := signHead(priv, &head); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateOwnedChannel(profile, head, now, localPeerID); err != nil {
		t.Fatal(err)
	}

	msg := ChannelMessage{
		ChannelID:     profile.ChannelID,
		MessageID:     1,
		Version:       1,
		Seq:           1,
		OwnerVersion:  1,
		CreatorPeerID: localPeerID,
		AuthorPeerID:  localPeerID,
		CreatedAt:     now,
		UpdatedAt:     now,
		Content:       NormalizeMessageContent("hello", nil),
		MessageType:   MessageTypeText,
	}
	if err := signMessage(priv, &msg); err != nil {
		t.Fatal(err)
	}
	if err := store.ApplyMessage(msg); err != nil {
		t.Fatal(err)
	}

	conflict := msg
	conflict.MessageType = MessageTypeSystem
	if err := signMessage(priv, &conflict); err != nil {
		t.Fatal(err)
	}
	if err := store.ApplyMessage(conflict); err == nil {
		t.Fatal("expected same-version conflicting signature to be rejected")
	}
}

func TestVerifyProfileRejectsChangedRetentionMinutes(t *testing.T) {
	t.Parallel()

	priv, localPeerID := mustTestIdentity(t)
	now := time.Now().Unix()
	profile := ChannelProfile{
		ChannelID:               mustUUIDv7(t),
		OwnerPeerID:             localPeerID,
		OwnerVersion:            1,
		Name:                    "retention-signature",
		MessageRetentionMinutes: 60,
		ProfileVersion:          1,
		CreatedAt:               now,
		UpdatedAt:               now,
	}
	if err := signProfile(priv, &profile); err != nil {
		t.Fatal(err)
	}
	if err := verifyProfile(profile); err != nil {
		t.Fatalf("verify signed profile: %v", err)
	}
	profile.MessageRetentionMinutes = 30
	if err := verifyProfile(profile); err == nil {
		t.Fatal("expected verifyProfile to reject changed message_retention_minutes")
	}
}

func TestCleanupExpiredMessagesDeletesExpiredRows(t *testing.T) {
	t.Parallel()

	priv, localPeerID := mustTestIdentity(t)
	store := mustTestStore(t)
	now := time.Now().Unix()

	profile := ChannelProfile{
		ChannelID:               mustUUIDv7(t),
		OwnerPeerID:             localPeerID,
		OwnerVersion:            1,
		Name:                    "retention-cleanup",
		MessageRetentionMinutes: 1,
		ProfileVersion:          1,
		CreatedAt:               now,
		UpdatedAt:               now,
	}
	if err := signProfile(priv, &profile); err != nil {
		t.Fatal(err)
	}
	head := ChannelHead{
		ChannelID:      profile.ChannelID,
		OwnerPeerID:    localPeerID,
		OwnerVersion:   1,
		LastMessageID:  0,
		ProfileVersion: 1,
		LastSeq:        0,
		UpdatedAt:      now,
	}
	if err := signHead(priv, &head); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateOwnedChannel(profile, head, now, localPeerID); err != nil {
		t.Fatal(err)
	}

	expiredAt := now - 120
	expiredMsg := ChannelMessage{
		ChannelID:     profile.ChannelID,
		MessageID:     1,
		Version:       1,
		Seq:           1,
		OwnerVersion:  1,
		CreatorPeerID: localPeerID,
		AuthorPeerID:  localPeerID,
		CreatedAt:     expiredAt,
		UpdatedAt:     now,
		Content:       NormalizeMessageContent("expired", nil),
		MessageType:   MessageTypeText,
	}
	if err := signMessage(priv, &expiredMsg); err != nil {
		t.Fatal(err)
	}
	head.LastMessageID = 1
	head.LastSeq = 1
	head.UpdatedAt = expiredAt
	if err := signHead(priv, &head); err != nil {
		t.Fatal(err)
	}
	change1 := changeFromMessage(expiredMsg, localPeerID)
	if err := store.CommitOwnedMessageChange(expiredMsg, head, change1); err != nil {
		t.Fatal(err)
	}

	freshMsg := ChannelMessage{
		ChannelID:     profile.ChannelID,
		MessageID:     2,
		Version:       1,
		Seq:           2,
		OwnerVersion:  1,
		CreatorPeerID: localPeerID,
		AuthorPeerID:  localPeerID,
		CreatedAt:     now,
		UpdatedAt:     now,
		Content:       NormalizeMessageContent("fresh", nil),
		MessageType:   MessageTypeText,
	}
	if err := signMessage(priv, &freshMsg); err != nil {
		t.Fatal(err)
	}
	head.LastMessageID = 2
	head.LastSeq = 2
	head.UpdatedAt = now
	if err := signHead(priv, &head); err != nil {
		t.Fatal(err)
	}
	change2 := changeFromMessage(freshMsg, localPeerID)
	if err := store.CommitOwnedMessageChange(freshMsg, head, change2); err != nil {
		t.Fatal(err)
	}

	if err := store.CleanupExpiredMessages(time.Unix(now, 0).UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetChannelMessage(profile.ChannelID, 1); err != sql.ErrNoRows {
		t.Fatalf("want expired message deleted, got err=%v", err)
	}
	msg2, err := store.GetChannelMessage(profile.ChannelID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if msg2.MessageID != 2 {
		t.Fatalf("want fresh message 2, got %d", msg2.MessageID)
	}
	changes, err := store.GetChannelChanges(profile.ChannelID, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes.Items) != 1 || changes.Items[0].MessageID == nil || *changes.Items[0].MessageID != 2 {
		t.Fatalf("want only fresh change after cleanup, got %#v", changes.Items)
	}
	state, err := store.GetChannelSyncState(profile.ChannelID)
	if err != nil {
		t.Fatal(err)
	}
	if state.LatestLoadedMessageID != 2 || state.OldestLoadedMessageID != 2 {
		t.Fatalf("want loaded range reset to message 2, got latest=%d oldest=%d", state.LatestLoadedMessageID, state.OldestLoadedMessageID)
	}
}

func TestIsMessageExpiredUsesCreatedAt(t *testing.T) {
	t.Parallel()

	svc := &Service{}
	profile := ChannelProfile{MessageRetentionMinutes: 1}
	message := ChannelMessage{
		CreatedAt: time.Now().Add(-2 * time.Minute).Unix(),
		UpdatedAt: time.Now().Unix(),
	}
	expired, err := svc.isMessageExpired(profile, message)
	if err != nil {
		t.Fatal(err)
	}
	if !expired {
		t.Fatal("expected message to expire based on created_at even if updated_at is newer")
	}
}

func TestSubscribeReceivesPubSubAndSyncsChanges(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	mn := mocknet.New()
	defer func() { _ = mn.Close() }()

	ownerPriv, ownerPeerID := mustTestIdentity(t)
	readerPriv, _ := mustTestIdentity(t)
	addr1 := mustMultiaddr(t, "/ip6/::1/tcp/12001")
	addr2 := mustMultiaddr(t, "/ip6/::1/tcp/12002")
	ownerHost, err := mn.AddPeer(ownerPriv, addr1)
	if err != nil {
		t.Fatal(err)
	}
	readerHost, err := mn.AddPeer(readerPriv, addr2)
	if err != nil {
		t.Fatal(err)
	}
	if err := mn.LinkAll(); err != nil {
		t.Fatal(err)
	}
	if _, err := mn.ConnectPeers(ownerHost.ID(), readerHost.ID()); err != nil {
		t.Fatal(err)
	}

	ownerPS, err := pubsub.NewGossipSub(ctx, ownerHost)
	if err != nil {
		t.Fatal(err)
	}
	readerPS, err := pubsub.NewGossipSub(ctx, readerHost)
	if err != nil {
		t.Fatal(err)
	}

	ownerSvc := mustTestService(t, ctx, ownerHost, ownerPS, ownerPriv, filepath.Join(t.TempDir(), "owner.db"))
	readerSvc := mustTestService(t, ctx, readerHost, readerPS, readerPriv, filepath.Join(t.TempDir(), "reader.db"))

	summary, err := ownerSvc.CreateChannel(CreateChannelInput{Name: "channel-a", Bio: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	channelID := summary.Profile.ChannelID

	first, err := ownerSvc.CreateChannelMessage(channelID, UpsertMessageInput{Text: "first"})
	if err != nil {
		t.Fatal(err)
	}
	if first.MessageID != 1 {
		t.Fatalf("want first message id 1, got %d", first.MessageID)
	}

	result, err := readerSvc.SubscribeChannel(ctx, channelID, []string{ownerPeerID}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if result.Profile.ChannelID != channelID {
		t.Fatalf("subscribe profile channel mismatch: want %s got %s", channelID, result.Profile.ChannelID)
	}
	if len(result.Messages) != 1 {
		t.Fatalf("want 1 initial message, got %d", len(result.Messages))
	}

	if _, err := ownerSvc.CreateChannelMessage(channelID, UpsertMessageInput{Text: "second"}); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		msg, err := readerSvc.GetChannelMessage(channelID, 2)
		if err == nil && msg.MessageID == 2 && msg.Content.Text == "second" {
			return
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatal("reader did not receive second message via pubsub+sync")
}

func TestSubscribeChannelFiltersExpiredMessagesInResponse(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	mn := mocknet.New()
	defer func() { _ = mn.Close() }()

	ownerPriv, ownerPeerID := mustTestIdentity(t)
	readerPriv, _ := mustTestIdentity(t)
	addr1 := mustMultiaddr(t, "/ip6/::1/tcp/12005")
	addr2 := mustMultiaddr(t, "/ip6/::1/tcp/12006")
	ownerHost, err := mn.AddPeer(ownerPriv, addr1)
	if err != nil {
		t.Fatal(err)
	}
	readerHost, err := mn.AddPeer(readerPriv, addr2)
	if err != nil {
		t.Fatal(err)
	}
	if err := mn.LinkAll(); err != nil {
		t.Fatal(err)
	}
	if _, err := mn.ConnectPeers(ownerHost.ID(), readerHost.ID()); err != nil {
		t.Fatal(err)
	}

	ownerPS, err := pubsub.NewGossipSub(ctx, ownerHost)
	if err != nil {
		t.Fatal(err)
	}
	readerPS, err := pubsub.NewGossipSub(ctx, readerHost)
	if err != nil {
		t.Fatal(err)
	}

	ownerSvc := mustTestService(t, ctx, ownerHost, ownerPS, ownerPriv, filepath.Join(t.TempDir(), "owner-subscribe-filter.db"))
	readerSvc := mustTestService(t, ctx, readerHost, readerPS, readerPriv, filepath.Join(t.TempDir(), "reader-subscribe-filter.db"))

	summary, err := ownerSvc.CreateChannel(CreateChannelInput{Name: "retention-subscribe", MessageRetentionMinutes: 1})
	if err != nil {
		t.Fatal(err)
	}
	channelID := summary.Profile.ChannelID
	profile, err := ownerSvc.store.GetChannelProfile(channelID)
	if err != nil {
		t.Fatal(err)
	}
	head, err := ownerSvc.store.GetChannelHead(channelID)
	if err != nil {
		t.Fatal(err)
	}
	expiredAt := time.Now().Add(-2 * time.Minute).Unix()
	expiredMsg := ChannelMessage{
		ChannelID:     channelID,
		MessageID:     1,
		Version:       1,
		Seq:           1,
		OwnerVersion:  profile.OwnerVersion,
		CreatorPeerID: ownerPeerID,
		AuthorPeerID:  ownerPeerID,
		CreatedAt:     expiredAt,
		UpdatedAt:     time.Now().Unix(),
		Content:       NormalizeMessageContent("expired", nil),
		MessageType:   MessageTypeText,
	}
	if err := signMessage(ownerPriv, &expiredMsg); err != nil {
		t.Fatal(err)
	}
	head.LastMessageID = expiredMsg.MessageID
	head.LastSeq = expiredMsg.Seq
	head.UpdatedAt = expiredMsg.UpdatedAt
	if err := signHead(ownerPriv, &head); err != nil {
		t.Fatal(err)
	}
	if err := ownerSvc.store.CommitOwnedMessageChange(expiredMsg, head, changeFromMessage(expiredMsg, ownerPeerID)); err != nil {
		t.Fatal(err)
	}

	result, err := readerSvc.SubscribeChannel(ctx, channelID, []string{ownerPeerID}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Messages) != 0 {
		t.Fatalf("want expired snapshot messages to be filtered from subscribe response, got %d", len(result.Messages))
	}
	if _, err := readerSvc.store.GetChannelMessage(channelID, 1); err != sql.ErrNoRows {
		t.Fatalf("want expired message filtered from local store too, got err=%v", err)
	}
}

func TestSubscribeChannelRecordsPlaceholderBeforeSnapshot(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	mn := mocknet.New()
	defer func() { _ = mn.Close() }()

	priv, _ := mustTestIdentity(t)
	addr := mustMultiaddr(t, "/ip6/::1/tcp/12007")
	h, err := mn.AddPeer(priv, addr)
	if err != nil {
		t.Fatal(err)
	}
	ps, err := pubsub.NewGossipSub(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	svc := mustTestService(t, ctx, h, ps, priv, filepath.Join(t.TempDir(), "subscribe-placeholder.db"))

	channelID := mustUUIDv7(t)
	_, seedPeerID := mustTestIdentity(t)
	result, err := svc.SubscribeChannel(ctx, channelID, []string{seedPeerID}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if result.Profile.ChannelID != channelID {
		t.Fatalf("want placeholder profile channel %q, got %q", channelID, result.Profile.ChannelID)
	}
	state, err := svc.store.GetChannelSyncState(channelID)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Subscribed {
		t.Fatal("placeholder subscription should be stored as subscribed")
	}
	providers, err := svc.store.ListProviders(channelID)
	if err != nil {
		t.Fatal(err)
	}
	if len(providers) != 1 || providers[0].PeerID != seedPeerID {
		t.Fatalf("want placeholder seed provider %q, got %#v", seedPeerID, providers)
	}
}

func TestSubscribeChannelResumesFromLastSeenSeq(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	mn := mocknet.New()
	defer func() { _ = mn.Close() }()

	ownerPriv, ownerPeerID := mustTestIdentity(t)
	readerPriv, _ := mustTestIdentity(t)
	addr1 := mustMultiaddr(t, "/ip6/::1/tcp/12011")
	addr2 := mustMultiaddr(t, "/ip6/::1/tcp/12012")
	ownerHost, err := mn.AddPeer(ownerPriv, addr1)
	if err != nil {
		t.Fatal(err)
	}
	readerHost, err := mn.AddPeer(readerPriv, addr2)
	if err != nil {
		t.Fatal(err)
	}
	if err := mn.LinkAll(); err != nil {
		t.Fatal(err)
	}
	if _, err := mn.ConnectPeers(ownerHost.ID(), readerHost.ID()); err != nil {
		t.Fatal(err)
	}

	ownerPS, err := pubsub.NewGossipSub(ctx, ownerHost)
	if err != nil {
		t.Fatal(err)
	}
	readerPS, err := pubsub.NewGossipSub(ctx, readerHost)
	if err != nil {
		t.Fatal(err)
	}

	ownerSvc := mustTestService(t, ctx, ownerHost, ownerPS, ownerPriv, filepath.Join(t.TempDir(), "owner-resume.db"))
	readerSvc := mustTestService(t, ctx, readerHost, readerPS, readerPriv, filepath.Join(t.TempDir(), "reader-resume.db"))

	summary, err := ownerSvc.CreateChannel(CreateChannelInput{Name: "resume-channel"})
	if err != nil {
		t.Fatal(err)
	}
	channelID := summary.Profile.ChannelID

	for i := 1; i <= 140; i++ {
		if _, err := ownerSvc.CreateChannelMessage(channelID, UpsertMessageInput{Text: fmt.Sprintf("msg-%03d", i)}); err != nil {
			t.Fatalf("create message %d: %v", i, err)
		}
	}

	var remoteProfile ChannelProfile
	if err := readerSvc.rpcCall(ctx, ownerPeerID, "get_channel_profile", struct {
		ChannelID string `json:"channel_id"`
	}{ChannelID: channelID}, &remoteProfile); err != nil {
		t.Fatalf("rpc profile: %v", err)
	}
	if err := verifyProfile(remoteProfile); err != nil {
		t.Fatalf("verify profile: %v", err)
	}
	var remoteHead ChannelHead
	if err := readerSvc.rpcCall(ctx, ownerPeerID, "get_channel_head", struct {
		ChannelID string `json:"channel_id"`
	}{ChannelID: channelID}, &remoteHead); err != nil {
		t.Fatalf("rpc head: %v", err)
	}
	if err := verifyHead(remoteHead); err != nil {
		t.Fatalf("verify head: %v", err)
	}
	var remoteMsgs GetMessagesResponse
	if err := readerSvc.rpcCall(ctx, ownerPeerID, "get_channel_messages", struct {
		ChannelID string `json:"channel_id"`
		Limit     int    `json:"limit"`
	}{ChannelID: channelID, Limit: DefaultPageLimit}, &remoteMsgs); err != nil {
		t.Fatalf("rpc messages: %v", err)
	}
	for _, item := range remoteMsgs.Items {
		if err := readerSvc.verifyMessageAgainstProfile(remoteProfile, item); err != nil {
			t.Fatalf("verify message %d: %v", item.MessageID, err)
		}
	}

	result, err := readerSvc.SubscribeChannel(ctx, channelID, []string{ownerPeerID}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if result.Head.LastSeq != 140 {
		t.Fatalf("want remote last seq 140, got %d", result.Head.LastSeq)
	}

	msg11, err := readerSvc.GetChannelMessage(channelID, 11)
	if err != nil {
		t.Fatalf("message 11 should be resumed from changes: %v", err)
	}
	if msg11.MessageID != 11 {
		t.Fatalf("want message 11, got %d", msg11.MessageID)
	}
	msg115, err := readerSvc.GetChannelMessage(channelID, 115)
	if err != nil {
		t.Fatalf("message 115 should be resumed across paginated changes: %v", err)
	}
	if msg115.MessageID != 115 {
		t.Fatalf("want message 115, got %d", msg115.MessageID)
	}

	state, err := readerSvc.store.GetChannelSyncState(channelID)
	if err != nil {
		t.Fatal(err)
	}
	if state.LastSeenSeq != 140 {
		t.Fatalf("want last_seen_seq 140, got %d", state.LastSeenSeq)
	}
	if state.LastSyncedSeq != 140 {
		t.Fatalf("want last_synced_seq 140, got %d", state.LastSyncedSeq)
	}
}

func TestSyncAfterFallsBackFromStaleProvider(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	mn := mocknet.New()
	defer func() { _ = mn.Close() }()

	ownerPriv, ownerPeerID := mustTestIdentity(t)
	stalePriv, stalePeerID := mustTestIdentity(t)
	readerPriv, _ := mustTestIdentity(t)
	addr1 := mustMultiaddr(t, "/ip6/::1/tcp/12015")
	addr2 := mustMultiaddr(t, "/ip6/::1/tcp/12016")
	addr3 := mustMultiaddr(t, "/ip6/::1/tcp/12017")
	ownerHost, err := mn.AddPeer(ownerPriv, addr1)
	if err != nil {
		t.Fatal(err)
	}
	staleHost, err := mn.AddPeer(stalePriv, addr2)
	if err != nil {
		t.Fatal(err)
	}
	readerHost, err := mn.AddPeer(readerPriv, addr3)
	if err != nil {
		t.Fatal(err)
	}
	if err := mn.LinkAll(); err != nil {
		t.Fatal(err)
	}
	if _, err := mn.ConnectPeers(ownerHost.ID(), staleHost.ID()); err != nil {
		t.Fatal(err)
	}
	if _, err := mn.ConnectPeers(ownerHost.ID(), readerHost.ID()); err != nil {
		t.Fatal(err)
	}
	if _, err := mn.ConnectPeers(staleHost.ID(), readerHost.ID()); err != nil {
		t.Fatal(err)
	}

	ownerPS, err := pubsub.NewGossipSub(ctx, ownerHost)
	if err != nil {
		t.Fatal(err)
	}
	stalePS, err := pubsub.NewGossipSub(ctx, staleHost)
	if err != nil {
		t.Fatal(err)
	}
	readerPS, err := pubsub.NewGossipSub(ctx, readerHost)
	if err != nil {
		t.Fatal(err)
	}

	ownerSvc := mustTestService(t, ctx, ownerHost, ownerPS, ownerPriv, filepath.Join(t.TempDir(), "owner-sync-fallback.db"))
	staleSvc := mustTestService(t, ctx, staleHost, stalePS, stalePriv, filepath.Join(t.TempDir(), "stale-sync-fallback.db"))
	readerSvc := mustTestService(t, ctx, readerHost, readerPS, readerPriv, filepath.Join(t.TempDir(), "reader-sync-fallback.db"))

	summary, err := ownerSvc.CreateChannel(CreateChannelInput{Name: "sync-fallback"})
	if err != nil {
		t.Fatal(err)
	}
	channelID := summary.Profile.ChannelID

	for i := 1; i <= 20; i++ {
		if _, err := ownerSvc.CreateChannelMessage(channelID, UpsertMessageInput{Text: fmt.Sprintf("sync-%02d", i)}); err != nil {
			t.Fatalf("create initial message %d: %v", i, err)
		}
	}

	if _, err := staleSvc.SubscribeChannel(ctx, channelID, []string{ownerPeerID}, 0); err != nil {
		t.Fatalf("stale subscribe: %v", err)
	}
	if err := staleSvc.UnsubscribeChannel(channelID); err != nil {
		t.Fatalf("stale unsubscribe: %v", err)
	}

	for i := 21; i <= 40; i++ {
		if _, err := ownerSvc.CreateChannelMessage(channelID, UpsertMessageInput{Text: fmt.Sprintf("sync-%02d", i)}); err != nil {
			t.Fatalf("create newer message %d: %v", i, err)
		}
	}

	if _, err := readerSvc.SubscribeChannel(ctx, channelID, []string{stalePeerID}, 0); err != nil {
		t.Fatalf("reader subscribe from stale provider: %v", err)
	}
	now := time.Now().Unix()
	if err := readerSvc.store.UpsertProvider(channelID, stalePeerID, "seed", now); err != nil {
		t.Fatalf("upsert stale provider: %v", err)
	}
	if err := readerSvc.store.UpsertProvider(channelID, ownerPeerID, "seed", now-1); err != nil {
		t.Fatalf("upsert owner provider: %v", err)
	}

	if err := readerSvc.SyncChannel(ctx, channelID); err != nil {
		t.Fatal(err)
	}

	msg40, err := readerSvc.GetChannelMessage(channelID, 40)
	if err != nil {
		t.Fatalf("reader should fetch newer message from owner after stale provider: %v", err)
	}
	if msg40.MessageID != 40 {
		t.Fatalf("want message 40, got %d", msg40.MessageID)
	}
	state, err := readerSvc.store.GetChannelSyncState(channelID)
	if err != nil {
		t.Fatal(err)
	}
	if state.LastSyncedSeq != 40 {
		t.Fatalf("want last_synced_seq 40, got %d", state.LastSyncedSeq)
	}
}

func TestLoadMessagesFromProvidersFallsBackToNextProvider(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	mn := mocknet.New()
	defer func() { _ = mn.Close() }()

	ownerPriv, ownerPeerID := mustTestIdentity(t)
	partialPriv, partialPeerID := mustTestIdentity(t)
	readerPriv, _ := mustTestIdentity(t)
	addr1 := mustMultiaddr(t, "/ip6/::1/tcp/12021")
	addr2 := mustMultiaddr(t, "/ip6/::1/tcp/12022")
	addr3 := mustMultiaddr(t, "/ip6/::1/tcp/12023")
	ownerHost, err := mn.AddPeer(ownerPriv, addr1)
	if err != nil {
		t.Fatal(err)
	}
	partialHost, err := mn.AddPeer(partialPriv, addr2)
	if err != nil {
		t.Fatal(err)
	}
	readerHost, err := mn.AddPeer(readerPriv, addr3)
	if err != nil {
		t.Fatal(err)
	}
	if err := mn.LinkAll(); err != nil {
		t.Fatal(err)
	}
	if _, err := mn.ConnectPeers(ownerHost.ID(), partialHost.ID()); err != nil {
		t.Fatal(err)
	}
	if _, err := mn.ConnectPeers(ownerHost.ID(), readerHost.ID()); err != nil {
		t.Fatal(err)
	}
	if _, err := mn.ConnectPeers(partialHost.ID(), readerHost.ID()); err != nil {
		t.Fatal(err)
	}

	ownerPS, err := pubsub.NewGossipSub(ctx, ownerHost)
	if err != nil {
		t.Fatal(err)
	}
	partialPS, err := pubsub.NewGossipSub(ctx, partialHost)
	if err != nil {
		t.Fatal(err)
	}
	readerPS, err := pubsub.NewGossipSub(ctx, readerHost)
	if err != nil {
		t.Fatal(err)
	}

	ownerSvc := mustTestService(t, ctx, ownerHost, ownerPS, ownerPriv, filepath.Join(t.TempDir(), "owner-history.db"))
	partialSvc := mustTestService(t, ctx, partialHost, partialPS, partialPriv, filepath.Join(t.TempDir(), "partial-history.db"))
	readerSvc := mustTestService(t, ctx, readerHost, readerPS, readerPriv, filepath.Join(t.TempDir(), "reader-history.db"))

	summary, err := ownerSvc.CreateChannel(CreateChannelInput{Name: "history-channel"})
	if err != nil {
		t.Fatal(err)
	}
	channelID := summary.Profile.ChannelID

	for i := 1; i <= 40; i++ {
		if _, err := ownerSvc.CreateChannelMessage(channelID, UpsertMessageInput{Text: fmt.Sprintf("history-%02d", i)}); err != nil {
			t.Fatalf("create message %d: %v", i, err)
		}
	}

	if _, err := partialSvc.SubscribeChannel(ctx, channelID, []string{ownerPeerID}, 0); err != nil {
		t.Fatalf("partial subscribe: %v", err)
	}
	if _, err := readerSvc.SubscribeChannel(ctx, channelID, []string{partialPeerID}, 0); err != nil {
		t.Fatalf("reader subscribe: %v", err)
	}
	now := time.Now().Unix()
	if err := readerSvc.store.UpsertProvider(channelID, partialPeerID, "seed", now); err != nil {
		t.Fatalf("upsert partial provider: %v", err)
	}
	if err := readerSvc.store.UpsertProvider(channelID, ownerPeerID, "seed", now-1); err != nil {
		t.Fatalf("upsert owner provider: %v", err)
	}

	items, err := readerSvc.LoadMessagesFromProviders(ctx, channelID, 21, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 20 {
		t.Fatalf("want 20 older messages, got %d", len(items))
	}
	if items[0].MessageID != 20 {
		t.Fatalf("want first loaded message id 20, got %d", items[0].MessageID)
	}
	if items[len(items)-1].MessageID != 1 {
		t.Fatalf("want last loaded message id 1, got %d", items[len(items)-1].MessageID)
	}
}

func TestCreateChannelReturnsBeforeProvideCompletes(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	mn := mocknet.New()
	defer func() { _ = mn.Close() }()

	priv, _ := mustTestIdentity(t)
	addr := mustMultiaddr(t, "/ip6/::1/tcp/13005")
	h, err := mn.AddPeer(priv, addr)
	if err != nil {
		t.Fatal(err)
	}

	routing := &blockingRouting{
		provideStarted: make(chan struct{}, 1),
		releaseProvide: make(chan struct{}),
	}
	svc, err := NewService(ctx, filepath.Join(t.TempDir(), "create-fast.db"), h, routing, nil)
	if err != nil {
		t.Fatal(err)
	}
	svc.SetNodePrivateKey(priv)
	t.Cleanup(func() {
		close(routing.releaseProvide)
		_ = svc.Close()
	})

	done := make(chan struct {
		summary ChannelSummary
		err     error
	}, 1)
	go func() {
		summary, err := svc.CreateChannel(CreateChannelInput{Name: "fast-create"})
		done <- struct {
			summary ChannelSummary
			err     error
		}{summary: summary, err: err}
	}()

	select {
	case res := <-done:
		if res.err != nil {
			t.Fatal(res.err)
		}
		if res.summary.Profile.ChannelID == "" {
			t.Fatal("create channel should return channel_id immediately")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("create channel should not wait for provide to finish")
	}
}

func TestCreateChannelDefaultsToSubscribed(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	mn := mocknet.New()
	defer func() { _ = mn.Close() }()

	priv, _ := mustTestIdentity(t)
	addr := mustMultiaddr(t, "/ip6/::1/tcp/13007")
	h, err := mn.AddPeer(priv, addr)
	if err != nil {
		t.Fatal(err)
	}
	ps, err := pubsub.NewGossipSub(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	svc := mustTestService(t, ctx, h, ps, priv, filepath.Join(t.TempDir(), "default-subscribed.db"))

	summary, err := svc.CreateChannel(CreateChannelInput{Name: "default-subscribed"})
	if err != nil {
		t.Fatal(err)
	}
	if !summary.Sync.Subscribed {
		t.Fatal("created channel should default to subscribed")
	}
	state, err := svc.store.GetChannelSyncState(summary.Profile.ChannelID)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Subscribed {
		t.Fatal("stored sync state should mark created channel as subscribed")
	}
}

func TestListSubscribedChannels(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	mn := mocknet.New()
	defer func() { _ = mn.Close() }()

	priv, _ := mustTestIdentity(t)
	addr := mustMultiaddr(t, "/ip6/::1/tcp/13006")
	h, err := mn.AddPeer(priv, addr)
	if err != nil {
		t.Fatal(err)
	}
	ps, err := pubsub.NewGossipSub(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	svc := mustTestService(t, ctx, h, ps, priv, filepath.Join(t.TempDir(), "subscriptions.db"))

	first, err := svc.CreateChannel(CreateChannelInput{Name: "first"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.CreateChannel(CreateChannelInput{Name: "second"})
	if err != nil {
		t.Fatal(err)
	}
	third, err := svc.CreateChannel(CreateChannelInput{Name: "third"})
	if err != nil {
		t.Fatal(err)
	}

	if err := svc.UnsubscribeChannel(second.Profile.ChannelID); err != nil {
		t.Fatal(err)
	}
	if err := svc.store.UpdateSyncState(first.Profile.ChannelID, first.Head.LastSeq, first.Head.LastSeq, true, time.Now().Unix()+100); err != nil {
		t.Fatal(err)
	}

	items, err := svc.ListSubscribedChannels()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("want 3 channels including owned unsubscribed one, got %d", len(items))
	}
	if items[0].Profile.ChannelID != first.Profile.ChannelID {
		t.Fatalf("want first subscribed channel %q, got %q", first.Profile.ChannelID, items[0].Profile.ChannelID)
	}
	if items[1].Profile.ChannelID != third.Profile.ChannelID {
		t.Fatalf("want second channel %q, got %q", third.Profile.ChannelID, items[1].Profile.ChannelID)
	}
	if items[2].Profile.ChannelID != second.Profile.ChannelID {
		t.Fatalf("want owned unsubscribed channel %q, got %q", second.Profile.ChannelID, items[2].Profile.ChannelID)
	}
}

func TestCreateChannelFileMessagePinsToIPFS(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	mn := mocknet.New()
	defer func() { _ = mn.Close() }()

	priv, _ := mustTestIdentity(t)
	addr := mustMultiaddr(t, "/ip6/::1/tcp/13001")
	h, err := mn.AddPeer(priv, addr)
	if err != nil {
		t.Fatal(err)
	}
	ps, err := pubsub.NewGossipSub(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	svc := mustTestService(t, ctx, h, ps, priv, filepath.Join(t.TempDir(), "owner.db"))
	svc.SetIPFSFilePinner(fakeIPFSPinner{cid: "bafybeigdyrzt-test"})

	summary, err := svc.CreateChannel(CreateChannelInput{Name: "files"})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := svc.CreateChannelFileMessage(summary.Profile.ChannelID, "hello", "demo.txt", "text/plain", []byte("hello world"))
	if err != nil {
		t.Fatal(err)
	}
	if msg.MessageType != MessageTypeFile {
		t.Fatalf("want message type %q, got %q", MessageTypeFile, msg.MessageType)
	}
	if len(msg.Content.Files) != 1 {
		t.Fatalf("want 1 file entry, got %d", len(msg.Content.Files))
	}
	file := msg.Content.Files[0]
	if file.BlobID != "bafybeigdyrzt-test" {
		t.Fatalf("want blob_id bafybeigdyrzt-test, got %q", file.BlobID)
	}
	if file.URL != "/ipfs/bafybeigdyrzt-test/demo.txt" {
		t.Fatalf("want ipfs url, got %q", file.URL)
	}
	sum := sha256.Sum256([]byte("hello world"))
	if file.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("sha256 mismatch: got %q", file.SHA256)
	}
}

func TestCreateChannelFileMessageDetectsImageType(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	mn := mocknet.New()
	defer func() { _ = mn.Close() }()

	priv, _ := mustTestIdentity(t)
	addr := mustMultiaddr(t, "/ip6/::1/tcp/13003")
	h, err := mn.AddPeer(priv, addr)
	if err != nil {
		t.Fatal(err)
	}
	ps, err := pubsub.NewGossipSub(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	svc := mustTestService(t, ctx, h, ps, priv, filepath.Join(t.TempDir(), "image.db"))
	svc.SetIPFSFilePinner(fakeIPFSPinner{cid: "bafybeiimage"})

	summary, err := svc.CreateChannel(CreateChannelInput{Name: "images"})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := svc.CreateChannelFileMessage(summary.Profile.ChannelID, "img", "a.png", "image/png", []byte("pngdata"))
	if err != nil {
		t.Fatal(err)
	}
	if msg.MessageType != MessageTypeImage {
		t.Fatalf("want message type %q, got %q", MessageTypeImage, msg.MessageType)
	}
}

func TestCreateChannelWithAvatarPinsToIPFS(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	mn := mocknet.New()
	defer func() { _ = mn.Close() }()

	priv, _ := mustTestIdentity(t)
	addr := mustMultiaddr(t, "/ip6/::1/tcp/13002")
	h, err := mn.AddPeer(priv, addr)
	if err != nil {
		t.Fatal(err)
	}
	ps, err := pubsub.NewGossipSub(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	svc := mustTestService(t, ctx, h, ps, priv, filepath.Join(t.TempDir(), "avatar.db"))
	svc.SetIPFSFilePinner(fakeIPFSPinner{cid: "bafybeiavatarest"})

	summary, err := svc.CreateChannelWithAvatar("avatar-channel", "bio", 0, "avatar.png", "image/png", []byte("avatar-bytes"))
	if err != nil {
		t.Fatal(err)
	}
	avatar := summary.Profile.Avatar
	if avatar.BlobID != "bafybeiavatarest" {
		t.Fatalf("want avatar blob_id bafybeiavatarest, got %q", avatar.BlobID)
	}
	if avatar.URL != "/ipfs/bafybeiavatarest/avatar.png" {
		t.Fatalf("want avatar ipfs url, got %q", avatar.URL)
	}
	sum := sha256.Sum256([]byte("avatar-bytes"))
	if avatar.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("avatar sha256 mismatch: got %q", avatar.SHA256)
	}
}

func mustTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := NewStore(filepath.Join(t.TempDir(), "public_channels.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func mustTestService(t *testing.T, ctx context.Context, h host.Host, ps *pubsub.PubSub, priv crypto.PrivKey, dbPath string) *Service {
	t.Helper()
	svc, err := NewService(ctx, dbPath, h, nil, ps)
	if err != nil {
		t.Fatal(err)
	}
	svc.SetNodePrivateKey(priv)
	t.Cleanup(func() { _ = svc.Close() })
	return svc
}

func mustTestIdentity(t *testing.T) (crypto.PrivKey, string) {
	t.Helper()
	priv, _, err := crypto.GenerateEd25519Key(nil)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := peer.IDFromPrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return priv, pid.String()
}

func mustUUIDv7(t *testing.T) string {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	return id.String()
}

func mustMultiaddr(t *testing.T, raw string) ma.Multiaddr {
	t.Helper()
	addr, err := ma.NewMultiaddr(raw)
	if err != nil {
		t.Fatal(err)
	}
	return addr
}

type fakeIPFSPinner struct {
	cid string
}

func (f fakeIPFSPinner) PinAvatar(ctx context.Context, fileName string, data []byte) (string, error) {
	return f.cid, nil
}

func (f fakeIPFSPinner) PinChatFile(ctx context.Context, fileName string, data []byte) (string, error) {
	return f.cid, nil
}

type blockingRouting struct {
	provideStarted chan struct{}
	releaseProvide chan struct{}
}

func (r *blockingRouting) Provide(ctx context.Context, _ cid.Cid, _ bool) error {
	select {
	case r.provideStarted <- struct{}{}:
	default:
	}
	select {
	case <-r.releaseProvide:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *blockingRouting) FindProvidersAsync(ctx context.Context, _ cid.Cid, _ int) <-chan peer.AddrInfo {
	ch := make(chan peer.AddrInfo)
	close(ch)
	return ch
}

func (r *blockingRouting) FindPeer(context.Context, peer.ID) (peer.AddrInfo, error) {
	return peer.AddrInfo{}, corerouting.ErrNotFound
}

func (r *blockingRouting) PutValue(context.Context, string, []byte, ...corerouting.Option) error {
	return nil
}

func (r *blockingRouting) GetValue(context.Context, string, ...corerouting.Option) ([]byte, error) {
	return nil, corerouting.ErrNotFound
}

func (r *blockingRouting) SearchValue(context.Context, string, ...corerouting.Option) (<-chan []byte, error) {
	ch := make(chan []byte)
	close(ch)
	return ch, nil
}

func (r *blockingRouting) Bootstrap(context.Context) error {
	return nil
}
