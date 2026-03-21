package ipfsunixfs

import (
	"context"

	cid "github.com/ipfs/go-cid"
	ipld "github.com/ipfs/go-ipld-format"
)

// StatResult 為根節點的簡要統計。
type StatResult struct {
	Size     int64
	NumLinks int
}

// Stat 回傳根節點的邏輯大小與連結數（非檔案型別時 Size 可能為 0）。
func Stat(ctx context.Context, ng ipld.NodeGetter, c cid.Cid) (*StatResult, error) {
	n, err := ng.Get(ctx, c)
	if err != nil {
		return nil, err
	}
	sz, err := UnixFSFileSize(n)
	if err != nil {
		sz = 0
	}
	return &StatResult{
		Size:     sz,
		NumLinks: len(n.Links()),
	}, nil
}
