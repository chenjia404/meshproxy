package geoip

// Resolver returns the ISO 3166-1 alpha-2 country code for an IP address.
// Returns empty string when unknown or disabled.
type Resolver interface {
	Country(ip string) (string, error)
}
