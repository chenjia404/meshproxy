package ipfsunixfs

import (
	"context"
	"errors"
	"fmt"
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

// ChunkSizeFromSpec 解析 "size-1048576" 或回傳預設 1048576。
func ChunkSizeFromSpec(spec string) int {
	const def = 1048576
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

// CidBuilderV1UnixFS 依 hashFunction 建立 CIDv1 builder；預設 sha2-256。
func CidBuilderV1UnixFS(hashFunction string) (cid.Builder, error) {
	switch normalizeHashFunction(hashFunction) {
	case "", "sha2-256":
		return cid.V1Builder{Codec: cid.DagProtobuf, MhType: mh.SHA2_256}, nil
	case "sha2-512":
		return cid.V1Builder{Codec: cid.DagProtobuf, MhType: mh.SHA2_512}, nil
	default:
		return nil, fmt.Errorf("unsupported ipfs hash function: %s", hashFunction)
	}
}

// AddFileFromReader 以 balanced UnixFS 匯入單檔並寫入 DAGService。
func AddFileFromReader(
	ctx context.Context,
	ds ipld.DAGService,
	r io.Reader,
	chunkSize int,
	rawLeaves bool,
	cidVersion int,
	hashFunction string,
) (ipld.Node, error) {
	_ = ctx
	if cidVersion != 1 {
		return nil, fmt.Errorf("only cidVersion=1 supported")
	}
	builder, err := CidBuilderV1UnixFS(hashFunction)
	if err != nil {
		return nil, err
	}
	dbp := h.DagBuilderParams{
		Dagserv:    ds,
		Maxlinks:   h.DefaultLinksPerBlock,
		RawLeaves:  rawLeaves,
		CidBuilder: builder,
	}
	spl := chunker.NewSizeSplitter(r, int64(chunkSize))
	db, err := dbp.New(spl)
	if err != nil {
		return nil, err
	}
	return bal.Layout(db)
}

func normalizeHashFunction(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
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
