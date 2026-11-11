//go:build !linux

package system

func statfs(string) (Usage, error) {
	return Usage{}, ErrNoStatfs
}
