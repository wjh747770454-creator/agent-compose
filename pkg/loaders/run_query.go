package loaders

import "time"

type LoaderRunPageFilter struct {
	LoaderIDs       []string
	BeforeStartedAt time.Time
	BeforeLoaderID  string
	BeforeRunID     string
	Limit           int
}
