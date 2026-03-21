package ipfsstore

import (
	blockstore "github.com/ipfs/boxo/blockstore"
	ds "github.com/ipfs/go-datastore"
)

// NewBlockstore 由 batching datastore 建立預設 blockstore。
func NewBlockstore(d ds.Batching) blockstore.Blockstore {
	return blockstore.NewBlockstore(d)
}
