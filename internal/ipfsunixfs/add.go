package ipfsunixfs

import (
	"context"
	"errors"
	"io"
	"strconv"
	"strings"

	chunker "github.com/ipfs/boxo/chunker"
	mdag "github.com/ipfs/boxo/ipld/merkledag"
	unixfs "github.com/ipfs/boxo/ipld/unixfs"
	bal "github.com/ipfs/boxo/ipld/unixfs/importer/balanced"
	h "github.com/ipfs/boxo/ipld/unixfs/importer/helpers"
	cid "github.com/ipfs/go-cid"
	ipld "github.com/ipfs/go-ipld-format"
	mh "github.com/multiformats/go-multihash"
)

// ChunkSizeFromSpec 解析 "size-262144" 或回傳預設 262144。
func ChunkSizeFromSpec(spec string) int {
	const def = 262144
	if spec == "" {
		return def
	}
	if strings.HasPrefix(spec, "size-") {
		if n, err := strconv.Atoi(strings.TrimPrefix(spec, "size-")); err == nil && n > 0 {
			return n
		}
	}
	return def
}

// CidBuilderV1UnixFS 預設 CIDv1 + dag-pb + sha2-256（與 kubo 預設一致）。
func CidBuilderV1UnixFS() cid.Builder {
	return cid.V1Builder{Codec: cid.DagProtobuf, MhType: mh.SHA2_256}
}

// AddFileFromReader 以 balanced UnixFS 匯入單檔並寫入 DAGService。
func AddFileFromReader(ctx context.Context, ds ipld.DAGService, r io.Reader, chunkSize int, rawLeaves bool) (ipld.Node, error) {
	_ = ctx
	dbp := h.DagBuilderParams{
		Dagserv:    ds,
		Maxlinks:   h.DefaultLinksPerBlock,
		RawLeaves:  rawLeaves,
		CidBuilder: CidBuilderV1UnixFS(),
	}
	spl := chunker.NewSizeSplitter(r, int64(chunkSize))
	db, err := dbp.New(spl)
	if err != nil {
		return nil, err
	}
	return bal.Layout(db)
}

// UnixFSFileSize 回傳 UnixFS 檔案根節點的邏輯大小（位元組）。
func UnixFSFileSize(n ipld.Node) (int64, error) {
	switch nd := n.(type) {
	case *mdag.RawNode:
		return int64(len(nd.RawData())), nil
	case *mdag.ProtoNode:
		fsNode, err := unixfs.FSNodeFromBytes(nd.Data())
		if err != nil {
			return 0, err
		}
		switch fsNode.Type() {
		case unixfs.TFile, unixfs.TRaw:
			return int64(fsNode.FileSize()), nil
		default:
			return 0, errors.New("not a unixfs file node")
		}
	default:
		return 0, errors.New("unsupported node type for file size")
	}
}
