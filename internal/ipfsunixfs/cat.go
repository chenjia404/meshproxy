package ipfsunixfs

import (
	"context"
	"io"

	uio "github.com/ipfs/boxo/ipld/unixfs/io"
	cid "github.com/ipfs/go-cid"
	ipld "github.com/ipfs/go-ipld-format"
)

// Cat 以 UnixFS 匯出串流讀取（僅支援檔案類根節點；目錄請用閘道）。
func Cat(ctx context.Context, ng ipld.NodeGetter, c cid.Cid) (io.ReadCloser, error) {
	n, err := ng.Get(ctx, c)
	if err != nil {
		return nil, err
	}
	dr, err := uio.NewDagReader(ctx, n, ng)
	if err != nil {
		return nil, err
	}
	return io.NopCloser(dr), nil
}
