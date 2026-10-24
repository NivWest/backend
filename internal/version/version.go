// Package version holds the build version, injected at link time:
//
//	go build -ldflags "-X backend/internal/version.Version=$(cat VERSION)" ./cmd/api
package version

// Version is "dev" for local builds without ldflags.
var Version = "dev"
