package publicchannel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	crypto "github.com/libp2p/go-libp2p/core/crypto"
	host "github.com/libp2p/go-libp2p/core/host"
	peer "github.com/libp2p/go-libp2p/core/peer"
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

	summary, err := svc.CreateChannelWithAvatar("avatar-channel", "bio", "avatar.png", "image/png", []byte("avatar-bytes"))
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
