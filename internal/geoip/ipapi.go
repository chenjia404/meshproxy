package geoip

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const ipAPIBase = "http://ip-api.com/json/"

// ipAPIResponse matches the minimal fields we need from ip-api.com JSON.
type ipAPIResponse struct {
	CountryCode string `json:"countryCode"`
	Status      string `json:"status"`
}

// IPAPIResolver looks up country via ip-api.com (free, no key; rate limit ~45/min).
type IPAPIResolver struct {
	Client  *http.Client
	Timeout time.Duration
}

// NewIPAPIResolver returns an IPAPIResolver with a default timeout.
func NewIPAPIResolver() *IPAPIResolver {
	return &IPAPIResolver{
		Timeout: 5 * time.Second,
		Client:  &http.Client{Timeout: 5 * time.Second},
	}
}

// Country returns the ISO country code for the given IP.
func (r *IPAPIResolver) Country(ip string) (string, error) {
	if ip == "" {
		return "", nil
	}
	client := r.Client
	if client == nil {
		client = &http.Client{Timeout: r.Timeout}
	}
	if client.Timeout == 0 && r.Timeout != 0 {
		client = &http.Client{Timeout: r.Timeout}
	}
	url := ipAPIBase + ip + "?fields=status,countryCode"
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var body ipAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	if body.Status != "success" {
		return "", fmt.Errorf("ip-api returned status %q", body.Status)
	}
	return body.CountryCode, nil
}
