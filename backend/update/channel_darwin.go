//go:build darwin

package update

import (
	"os"
	"path/filepath"
	"strings"
)

// detectChannel: Homebrew installs are package-managed; .app bundles (manual
// drag-install) update in place.
func detectChannel() Channel {
	exe, err := os.Executable()
	if err != nil {
		return ChannelPortable
	}
	p := filepath.Clean(exe)
	if strings.Contains(p, "/opt/homebrew/") ||
		strings.Contains(p, "/usr/local/Cellar/") ||
		strings.Contains(p, "/usr/local/opt/") {
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
