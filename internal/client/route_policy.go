package client

// RoutePolicy controls how paths are selected.
type RoutePolicy struct {
	// AllowSelfExit controls whether the local node is allowed to act as exit.
	AllowSelfExit bool
}

// DefaultRoutePolicy returns a conservative default route policy.
func DefaultRoutePolicy() RoutePolicy {
	return RoutePolicy{
		AllowSelfExit: false,
	}
}

