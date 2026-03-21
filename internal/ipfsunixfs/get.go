package ipfsunixfs

import (
	"context"
	"io"

	cid "github.com/ipfs/go-cid"
	ipld "github.com/ipfs/go-ipld-format"
)

// Get 將 UnixFS 檔案內容寫入 w。
func Get(ctx context.Context, ng ipld.NodeGetter, c cid.Cid, w io.Writer) error {
	rc, err := Cat(ctx, ng, c)
	if err != nil {
		return err
	}
	defer rc.Close()
	_, err = io.Copy(w, rc)
	return err
}
