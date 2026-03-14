package geoip

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"

	"github.com/oschwald/geoip2-golang/v2"
)

const (
	geolite2DBName = "GeoLite2-Country.mmdb"
	geolite2URL   = "https://github.com/P3TERX/GeoLite.mmdb/raw/download/GeoLite2-Country.mmdb"
)

// GeoLite2Resolver looks up country using a local GeoLite2-Country.mmdb (opens with geoip2-golang).
// If the file does not exist under dataDir, it is downloaded from a public mirror.
type GeoLite2Resolver struct {
	db *geoip2.Reader
}

// NewGeoLite2Resolver opens or downloads GeoLite2-Country.mmdb from dataDir and returns a Resolver.
// dataDir is created if missing. If the mmdb file is missing, it is downloaded from the default URL.
func NewGeoLite2Resolver(dataDir string) (Resolver, error) {
	if dataDir == "" {
		dataDir = "data"
	}
	dbPath := filepath.Join(dataDir, geolite2DBName)
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		if err := os.MkdirAll(dataDir, 0755); err != nil {
			return nil, fmt.Errorf("create data dir for GeoLite2: %w", err)
		}
		log.Printf("[geoip] downloading %s to %s", geolite2URL, dbPath)
		if err := downloadFile(geolite2URL, dbPath); err != nil {
			return nil, fmt.Errorf("download GeoLite2-Country.mmdb: %w", err)
		}
		log.Printf("[geoip] GeoLite2-Country.mmdb ready at %s", dbPath)
	}
	db, err := geoip2.Open(dbPath)
	if err != nil {
		return nil, fmt.Errorf("open GeoLite2-Country.mmdb: %w", err)
	}
	return &GeoLite2Resolver{db: db}, nil
}

func downloadFile(url, path string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: status %d", url, resp.StatusCode)
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}

// Country returns the ISO 3166-1 alpha-2 country code for the IP.
func (r *GeoLite2Resolver) Country(ip string) (string, error) {
	if ip == "" {
		return "", nil
	}
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return "", err
	}
	record, err := r.db.Country(addr)
	if err != nil {
		return "", err
	}
	if !record.HasData() {
		return "", nil
	}
	return record.Country.ISOCode, nil
}

// Close closes the underlying database. Call when shutting down if needed.
func (r *GeoLite2Resolver) Close() error {
	if r.db != nil {
		return r.db.Close()
	}
	return nil
}
