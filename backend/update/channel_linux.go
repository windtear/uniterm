//go:build linux

package update

import (
	"os"
	"strings"
)

// detectChannel: deb/rpm installs live under /usr (and nfpm configures
// /usr/bin/uniterm); anything else is a portable tarball run.
func detectChannel() Channel {
	exe, err := os.Executable()
	if err != nil {
		return ChannelPortable
	}
	if strings.HasPrefix(exe, "/usr/") || strings.HasPrefix(exe, "/opt/") {
		return ChannelPackage
	}
	// Fallback signal: not writable means a privileged/managed install.
	if f, err := os.OpenFile(exe, os.O_WRONLY, 0); err == nil {
		f.Close()
	} else if os.IsPermission(err) {
		return ChannelPackage
	}
	return ChannelPortable
}
