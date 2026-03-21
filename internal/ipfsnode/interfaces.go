package ipfsnode

import (
	"context"
	"io"

	cid "github.com/ipfs/go-cid"
)

// IPFSService 嵌入式 IPFS 能力（與 ipfs.md 第 9 節一致）。
type IPFSService interface {
	AddFile(ctx context.Context, r io.Reader, opt AddFileOptions) (cid.Cid, error)
	AddDir(ctx context.Context, path string, opt AddDirOptions) (cid.Cid, error)

	Cat(ctx context.Context, c cid.Cid) (io.ReadCloser, error)
	Get(ctx context.Context, c cid.Cid, w io.Writer) error
	Stat(ctx context.Context, c cid.Cid) (*ObjectStat, error)

	Pin(ctx context.Context, c cid.Cid, recursive bool) error
	Unpin(ctx context.Context, c cid.Cid, recursive bool) error
	IsPinned(ctx context.Context, c cid.Cid) (bool, error)

	Provide(ctx context.Context, c cid.Cid, recursive bool) error
	HasLocal(ctx context.Context, c cid.Cid) (bool, error)
}

// AddFileOptions 控制 UnixFS 匯入參數。
type AddFileOptions struct {
	Filename     string
	RawLeaves    bool
	CIDVersion   int
	HashFunction string
	Chunker      string
	Pin          bool
}

// AddDirOptions 目錄匯入（首版可未實作）。
type AddDirOptions struct {
	Wrap       bool
	RawLeaves  bool
	CIDVersion int
	Chunker    string
	Pin        bool
}

// ObjectStat 為 /api/ipfs/stat 與內部查詢使用。
type ObjectStat struct {
	CID      string
	Size     int64
	NumLinks int
	Local    bool
	Pinned   bool
}
