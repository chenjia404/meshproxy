package geoip

// NoopResolver always returns empty country (no lookup).
type NoopResolver struct{}

func (NoopResolver) Country(ip string) (string, error) {
	return "", nil
}
