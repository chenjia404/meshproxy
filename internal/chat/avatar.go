package chat

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func NormalizeAvatarFileName(name string) string {
	base := filepath.Base(strings.TrimSpace(name))
	if base == "." || base == string(filepath.Separator) {
		return ""
	}
	return base
}

func AvatarStoredName(sourceName string, data []byte) string {
	ext := filepath.Ext(NormalizeAvatarFileName(sourceName))
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]) + ext
}

func SaveAvatarFile(dir, sourceName string, data []byte) (string, error) {
	if len(data) == 0 {
		return "", errors.New("avatar is empty")
	}
	if len(data) > MaxProfileAvatarBytes {
		return "", fmt.Errorf("avatar too large: max %d bytes", MaxProfileAvatarBytes)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	stored := AvatarStoredName(sourceName, data)
	path := filepath.Join(dir, stored)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}
	return stored, nil
}

func AvatarPath(dir, name string) (string, error) {
	clean := NormalizeAvatarFileName(name)
	if clean == "" {
		return "", errors.New("avatar not found")
	}
	return filepath.Join(dir, clean), nil
}
