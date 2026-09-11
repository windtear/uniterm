//go:build linux

package update

// applyPlatform replaces the portable binary in place. Package-managed
// installs (deb/rpm) are classified as ChannelPackage and never reach Apply.
func (m *Manager) applyPlatform(pend *PendingUpdate, onProgress func(Progress)) error {
	return applyBinary(pend.NewBinary, onProgress)
}
