package ipfspin

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	cid "github.com/ipfs/go-cid"
)

// FileStore 以 pins.json 保存遞迴 pin 的根 CID（首版簡化，見 ipfs.md）。
type FileStore struct {
	path string
	mu   sync.Mutex
	data pinFile
}

type pinFile struct {
	Recursive []string `json:"recursive"`
	Direct    []string `json:"direct"`
}

// NewFileStore 載入或建立 pin 狀態檔。
func NewFileStore(path string) (*FileStore, error) {
	s := &FileStore{path: path, data: pinFile{Recursive: []string{}, Direct: []string{}}}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	if len(b) == 0 {
		return s, nil
	}
	if err := json.Unmarshal(b, &s.data); err != nil {
		return nil, fmt.Errorf("parse pins file: %w", err)
	}
	return s, nil
}

func (s *FileStore) persistLocked() error {
	b, err := json.MarshalIndent(&s.data, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// PinRecursive 將 CID 字串加入遞迴 pin 集合。
func (s *FileStore) PinRecursive(c cid.Cid) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cs := c.String()
	for _, x := range s.data.Recursive {
		if x == cs {
			return s.persistLocked()
		}
	}
	s.data.Recursive = append(s.data.Recursive, cs)
	return s.persistLocked()
}

// UnpinRecursive 自遞迴 pin 集合移除。
func (s *FileStore) UnpinRecursive(c cid.Cid) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cs := c.String()
	out := s.data.Recursive[:0]
	for _, x := range s.data.Recursive {
		if x != cs {
			out = append(out, x)
		}
	}
	s.data.Recursive = out
	return s.persistLocked()
}

// IsPinnedRecursive 是否被遞迴 pin。
func (s *FileStore) IsPinnedRecursive(c cid.Cid) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	cs := c.String()
	for _, x := range s.data.Recursive {
		if x == cs {
			return true
		}
	}
	return false
}

// ErrNotImplemented 首版未實作 direct pin 差異時回傳。
var ErrNotImplemented = errors.New("direct pin not implemented in v1")
