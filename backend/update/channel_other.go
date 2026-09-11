//go:build !windows && !darwin && !linux

package update

// detectChannel fallback for unsupported platforms (never shipped): keep
// self-update disabled rather than guessing.
func detectChannel() Channel {
	return ChannelPackage
}
