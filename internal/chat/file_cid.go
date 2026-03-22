package chat

import (
	"bytes"
	"context"
	"errors"
	"io"

	"github.com/ipfs/boxo/blockservice"
	"github.com/ipfs/boxo/blockstore"
	offlineexchange "github.com/ipfs/boxo/exchange/offline"
	"github.com/ipfs/boxo/ipld/merkledag"
	"github.com/ipfs/go-datastore"
	dssync "github.com/ipfs/go-datastore/sync"

	"github.com/chenjia404/meshproxy/internal/ipfsunixfs"
)

// ComputeChatFileCID 以與 Kubo 一致的 UnixFS 預設參數為檔案建立 CIDv1。
func ComputeChatFileCID(data []byte) (string, error) {
	if len(data) == 0 {
		return "", errors.New("file is empty")
	}
	return ComputeChatFileCIDFromReader(bytes.NewReader(data))
}

// ComputeChatFileCIDFromReader 以 UnixFS importer 計算檔案根節點 CID。
func ComputeChatFileCIDFromReader(r io.Reader) (string, error) {
	if r == nil {
		return "", errors.New("reader is nil")
	}
	if _, err := ipfsunixfs.CidBuilderV1UnixFS("sha2-256"); err != nil {
		return "", err
	}
	ds := dssync.MutexWrap(datastore.NewMapDatastore())
	bs := blockstore.NewBlockstore(ds)
	bsvc := blockservice.New(bs, offlineexchange.Exchange(bs))
	dag := merkledag.NewDAGService(bsvc)
	nd, err := ipfsunixfs.AddFileFromReader(
		context.Background(),
		dag,
		r,
		ipfsunixfs.ChunkSizeFromSpec("size-1048576"),
		true,
		1,
		"sha2-256",
	)
	if err != nil {
		return "", err
	}
	return nd.Cid().String(), nil
}
