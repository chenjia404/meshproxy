package ipfsstore

import (
	"fmt"
	ds "github.com/ipfs/go-datastore"
	leveldb "github.com/ipfs/go-ds-leveldb"
	"os"
)

// OpenLevelDB 在 path 建立或開啟 LevelDB datastore（用於 blockstore）。
func OpenLevelDB(rootDataDir string) (ds.Batching, func() error, error) {
	if err := os.MkdirAll(rootDataDir, 0o755); err != nil {
		return nil, nil, fmt.Errorf("create datastore dir: %w", err)
	}
	lds, err := leveldb.NewDatastore(rootDataDir, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("open leveldb datastore: %w", err)
	}
	closeFn := func() error {
		return lds.Close()
	}
	return lds, closeFn, nil
}
