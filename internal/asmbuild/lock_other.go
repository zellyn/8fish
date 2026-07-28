//go:build !unix

package asmbuild

// lockDir is a no-op where flock is unavailable. The in-process mutex in
// withBuildLock still serializes builds within one test binary; only
// cross-process serialization is lost, which matters just for a parallel
// `go test ./...` on such a platform.
func lockDir(string) (func(), error) { return func() {}, nil }
