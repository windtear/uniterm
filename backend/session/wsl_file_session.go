//go:build windows
// +build windows

package session

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// The wsl.localhost 9P share cannot report Linux symbolic links: they surface
// as plain files whose stat fails ("cannot be resolved by the system"), so
// type, mode, size and mtime of symlinks cannot come from the share itself.
// The helpers below correct the listing from `ls -lAn` output and let
// directory navigation fall back to the link's real target.

// testDirScript builds the sh snippet that reports which paths resolve to a
// directory: one "<n> d" line per directory, in order; files print nothing.
// It must be free of shell variables — wsl.exe re-quotes argv inside double
// quotes before handing it to the login shell, so $vars/$((...)) would be
// expanded to nothing before sh ever sees the script.
func testDirScript(paths []string) string {
	var b strings.Builder
	for i, p := range paths {
		fmt.Fprintf(&b, "; if [ -d %s ]; then echo %s; fi", shellEscape(p), shellEscape(fmt.Sprintf("%d d", i+1)))
	}
	return strings.TrimPrefix(b.String(), "; ")
}

// readlinkFollowScript builds the sh snippet that prints the canonical target
// of path — and only for actual symlinks: plain readlink errors on non-links,
// which fails the whole chain.
func readlinkFollowScript(p string) string {
	return "readlink " + shellEscape(p) + " >/dev/null && readlink -f " + shellEscape(p)
}

// parseSymDirReply parses the reply of testDirScript into a name→is-dir set.
// Reply lines are "<n> d"; noise lines are skipped (files print nothing).
func parseSymDirReply(out string, paths []string) map[string]bool {
	res := make(map[string]bool)
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		idx, err := strconv.Atoi(f[0])
		if err != nil || idx < 1 || idx > len(paths) {
			continue
		}
		if f[1] == "d" {
			res[paths[idx-1]] = true
		}
	}
	return res
}

// applyLsEnrichment merges `ls -lAn` output into the UNC-derived listing:
// uid/gid-derived owner/group for every entry, plus — for symlink entries,
// which the share reports as anonymous plain files — mode, size, mtime and
// dir-ness straight from ls. Entries absent from ls stay untouched.
func applyLsEnrichment(files []FileItem, entries []lsEntry, symDirs map[string]bool, users, groups map[int]string) {
	byName := make(map[string]lsEntry, len(entries))
	for _, e := range entries {
		byName[e.Name] = e
	}
	for i := range files {
		e, ok := byName[files[i].Name]
		if !ok {
			continue
		}
		uid, err1 := strconv.Atoi(e.Owner)
		gid, err2 := strconv.Atoi(e.Group)
		if err1 == nil && err2 == nil {
			files[i].Owner = linuxIDName(users, uid)
			files[i].Group = linuxIDName(groups, gid)
		}
		if strings.HasPrefix(e.Mode, "l") {
			// The share cannot describe symlinks — take everything from ls.
			files[i].Mode = e.Mode
			files[i].Size = e.Size
			if !e.ModTime.IsZero() {
				files[i].ModTime = e.ModTime.Format(time.RFC3339)
			}
			files[i].IsDir = symDirs[files[i].Name]
		}
	}
}

// linuxIDName maps a numeric uid/gid to a display name, falling back to the
// number itself when /etc/passwd or /etc/group lacked the entry.
func linuxIDName(m map[int]string, id int) string {
	if n, ok := m[id]; ok {
		return n
	}
	return fmt.Sprintf("%d", id)
}

// isShareTraverseErr reports the wsl.localhost failure when an open crosses a
// symbolic link: the 9P redirector cannot follow it.
func isShareTraverseErr(err error) bool {
	return err != nil && strings.Contains(err.Error(), "cannot be resolved by the system")
}

// WSLFileSession is the file-transfer companion for a WSL terminal. The "remote"
// filesystem is the WSL distribution's own view reached through the Windows UNC
// path `//wsl.localhost/<distro>/...`, so every remote operation is a plain os
// call — no SFTP subsystem, no bundled server, no SSH credentials.
//
// It implements fileTransferSession, so all existing Sftp* bindings and the
// frontend file panel dispatch through it unchanged. Remote paths are kept in
// POSIX form (e.g. /home/user) so the frontend breadcrumb renders normally;
// they are translated to the UNC path at the os boundary.
//
// NOTE: the wsl.localhost share is only reachable with FORWARD slashes on this
// system (backslash UNC returns path-not-found), and `filepath` would rewrite
// them to backslashes, so all UNC path building uses "/" joining.
type WSLFileSession struct {
	baseSession
	distro string
	root   string // //wsl.localhost/<distro> (absolute; no trailing separator)
	cwd    string // remote cwd in POSIX form, e.g. /home/user

	localFSOps // Windows-local pane (ListLocal / Local* / ListLocalDrives)

	mu        sync.RWMutex
	transfers map[string]*TransferTask
	taskSeq   int64

	// uid/gid -> name maps loaded once from /etc/passwd and /etc/group so
	// listings can render real owner/group names instead of numbers.
	mapOnce  sync.Once
	userMap  map[int]string
	groupMap map[int]string

	connectCancel context.CancelFunc
}

// compile-time guarantees that the WSL file session satisfies the Session
// contract. (fileTransferSession is asserted at runtime via getSftp and lives
// in the app package, so it can't be referenced here.)
var _ Session = (*WSLFileSession)(nil)

func NewWSLFileSession(id string) *WSLFileSession {
	return &WSLFileSession{
		baseSession: baseSession{
			id:          id,
			sessionType: "wsl-file",
			status:      StatusDisconnected,
		},
		localFSOps: newLocalFSOps(),
		transfers:  make(map[string]*TransferTask),
	}
}

// Connect resolves the distro (config.Distro, else config.ShellPath wsl://name),
// probes the user home inside the distro and marks the session connected.
func (s *WSLFileSession) Connect(config ConnectionConfig) error {
	distro, _ := parseWSLPath(config.ShellPath)
	if distro == "" {
		s.setStatus(StatusError)
		return fmt.Errorf("empty WSL distribution name")
	}
	s.distro = distro
	s.root = `//wsl.localhost/` + distro
	s.setStatus(StatusConnecting)
	ctx, cancel := context.WithCancel(context.Background())
	s.connectCancel = cancel
	s.cwd = s.resolveHome(ctx, distro)
	s.setStatus(StatusConnected)
	return nil
}

// resolveHome asks the WSL distro for $HOME of its default user, returning a
// POSIX path. Falls back to / (distro root) if the probe fails or the distro is
// not running — the user can still navigate to a real directory afterwards.
func (s *WSLFileSession) resolveHome(ctx context.Context, distro string) string {
	if home, ok := s.probeHome(ctx, distro); ok {
		return home
	}
	return "/"
}

func (s *WSLFileSession) probeHome(ctx context.Context, distro string) (string, bool) {
	cmd := exec.CommandContext(ctx, "wsl.exe", "-d", distro, "--", "sh", "-c", "echo $HOME")
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	home := strings.TrimSpace(string(out))
	if home == "" || !strings.HasPrefix(home, "/") {
		return "", false
	}
	return home, true
}

// --- path helpers ----------------------------------------------------------

func (s *WSLFileSession) resolveRemote(p string) string {
	if p == "" {
		return path.Clean(s.cwd)
	}
	if !strings.HasPrefix(p, "/") {
		p = path.Join(s.cwd, p)
	}
	return path.Clean(p)
}

// uncPath translates a POSIX remote path to the absolute forward-slash UNC path
// under the distro root, e.g. /home/user -> //wsl.localhost/Ubuntu/home/user.
func (s *WSLFileSession) uncPath(p string) string {
	rel := strings.Trim(p, "/")
	if rel == "" {
		return s.root
	}
	return s.root + "/" + rel
}

func (s *WSLFileSession) resolveLocal(p string) string {
	if !filepath.IsAbs(p) {
		return filepath.Join(s.localCwd, p)
	}
	return filepath.Clean(p)
}

// joinEntry appends name to a directory. UNC roots stay forward-slashed (see the
// type doc comment); ordinary local paths use filepath.Join.
func joinEntry(dir, name string) string {
	if strings.HasPrefix(dir, `//`) || strings.HasPrefix(dir, `\\`) {
		return dir + "/" + name
	}
	return filepath.Join(dir, name)
}

// fileItemsFromDir builds FileItem entries from an os.ReadDir result.
func fileItemsFromDir(dir string, entries []os.DirEntry) []FileItem {
	files := make([]FileItem, 0, len(entries))
	for _, e := range entries {
		fi, err := e.Info()
		var mode os.FileMode
		var modTime time.Time
		var size int64
		if err == nil {
			mode = fi.Mode()
			modTime = fi.ModTime()
			size = fi.Size()
		}
		isDir := e.IsDir()
		full := joinEntry(dir, e.Name())
		if err == nil && mode&os.ModeSymlink != 0 {
			if target, terr := os.Stat(full); terr == nil && target.IsDir() {
				isDir = true
			}
		}
		isHidden := e.Name() != "" && e.Name()[0] == '.'
		if !isHidden {
			isHidden = isPathHidden(full)
		}
		files = append(files, FileItem{
			Name:     e.Name(),
			Size:     size,
			ModTime:  modTime.Format(time.RFC3339),
			Mode:     mode.String(),
			IsDir:    isDir,
			IsHidden: isHidden,
			Owner:    "",
		})
	}
	return files
}

// --- listing ---------------------------------------------------------------

func (s *WSLFileSession) ListRemote(dir string) (FileListResult, error) {
	d := s.resolveRemote(dir)
	entries, err := os.ReadDir(s.uncPath(d))
	if err != nil {
		return FileListResult{}, s.shareError(d, err)
	}
	files := fileItemsFromDir(s.uncPath(d), entries)
	s.enrichListing(files, d)
	return FileListResult{Files: files, Dir: d}, nil
}

// shareError converts a failed wsl.localhost access into user guidance where
// the failure is structural: /mnt Windows-drive mounts are denied through the
// share (the drive→WSL→drive loopback) — those files ARE the local drive, so
// the local pane is the way to reach them. Anything else passes through raw.
func (s *WSLFileSession) shareError(d string, err error) error {
	if d != "/mnt" && !strings.HasPrefix(d, "/mnt/") {
		return err
	}
	if d == "/mnt" {
		return fmt.Errorf("cannot list %s: Windows-drive mounts aren't reachable through the WSL file share (open C:\\ and the like from the local pane instead)", d)
	}
	return fmt.Errorf("cannot open %s: /mnt Windows-drive mounts aren't reachable through the WSL file share (open %s from the local pane instead)", d, s.wslMountDrive(d))
}

// --- owner/group resolution (matching SFTP's /etc/passwd + /etc/group) -------

func (s *WSLFileSession) ensureNameMaps() {
	s.mapOnce.Do(func() {
		s.userMap = map[int]string{}
		s.groupMap = map[int]string{}
		if b, err := os.ReadFile(s.uncPath("/etc/passwd")); err == nil {
			parseLinuxNameMap(b, s.userMap, 2)
		}
		if b, err := os.ReadFile(s.uncPath("/etc/group")); err == nil {
			parseLinuxNameMap(b, s.groupMap, 2)
		}
	})
}

// enrichListing post-processes a listing with a single `wsl ls -lAn` call:
// uid/gid-derived owner/group for every entry, plus — since the wsl.localhost
// 9P share cannot report Linux symlinks (they surface as plain, unstattable
// files) — mode, size, mtime and dir-ness for symlink entries, with dir
// symlinks probed in one extra batched round-trip. Failures degrade to the
// un-enriched listing rather than aborting it.
func (s *WSLFileSession) enrichListing(files []FileItem, dir string) {
	s.ensureNameMaps()
	if len(files) == 0 {
		return
	}
	out, err := exec.Command("wsl.exe", "-d", s.distro, "--", "ls", "-lAn", dir).Output()
	if err != nil {
		return
	}
	entries := parseLsLongListing(string(out))
	var symNames []string
	for _, e := range entries {
		if strings.HasPrefix(e.Mode, "l") {
			symNames = append(symNames, e.Name)
		}
	}
	symDirs := s.resolveSymlinkDirs(dir, symNames)
	applyLsEnrichment(files, entries, symDirs, s.userMap, s.groupMap)
}

// resolveSymlinkDirs determines, for a batch of entry names in dir, which
// symlinks point at directories — one sh round-trip instead of one per entry.
func (s *WSLFileSession) resolveSymlinkDirs(dir string, names []string) map[string]bool {
	if len(names) == 0 {
		return nil
	}
	paths := make([]string, len(names))
	for i, n := range names {
		paths[i] = path.Join(dir, n)
	}
	cmd := exec.Command("wsl.exe", "-d", s.distro, "--", "sh", "-c", testDirScript(paths))
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	return parseSymDirReply(string(out), names)
}

// readlinkTarget resolves p when it is a symbolic link, returning its
// canonical absolute target. Non-links (or wsl failures) yield an error.
func (s *WSLFileSession) readlinkTarget(p string) (string, error) {
	out, err := exec.Command("wsl.exe", "-d", s.distro, "--", "sh", "-c", readlinkFollowScript(p)).Output()
	if err != nil {
		return "", err
	}
	t := strings.TrimSpace(string(out))
	if !strings.HasPrefix(t, "/") {
		return "", fmt.Errorf("not a symlink: %s", p)
	}
	return t, nil
}

func (s *WSLFileSession) ChangeRemoteDir(dir string) (FileListResult, error) {
	d := s.resolveRemote(dir)
	fi, statErr := os.Stat(s.uncPath(d))
	if statErr == nil && fi.IsDir() {
		s.cwd = d
		return s.ListRemote(d)
	}
	// The share cannot traverse symbolic links: Stat surfaces the link as a
	// non-dir reparse point (or errors outright), and open fails with
	// "cannot be resolved by the system". When the path is a link to a
	// directory, continue at its real target so symlinked directories stay
	// navigable — like the SFTP/SCP backends — with the breadcrumb showing
	// the resolved path.
	if target, terr := s.readlinkTarget(d); terr == nil && target != d {
		if fi2, err2 := os.Stat(s.uncPath(target)); err2 == nil && fi2.IsDir() {
			s.cwd = target
			return s.ListRemote(target)
		}
	}
	if statErr != nil {
		// /mnt Windows-drive mounts are denied by the share (drive→WSL→drive
		// loopback); guide the user to the local pane instead of a raw error.
		return FileListResult{}, s.shareError(d, statErr)
	}
	return FileListResult{}, fmt.Errorf("not a directory: %s", d)
}

// wslMountDrive maps a /mnt/<letter> mount path back to its Windows drive
// (e.g. /mnt/c -> C:\) for the error hint above.
func (s *WSLFileSession) wslMountDrive(d string) string {
	rest := strings.TrimPrefix(d, "/mnt/")
	if i := strings.Index(rest, "/"); i >= 0 {
		rest = rest[:i]
	}
	if len(rest) == 1 {
		return strings.ToUpper(rest) + `:\`
	}
	return d
}

// The Windows-local pane (ListLocal / ChangeLocalDir / ListLocalDrives /
// LocalRemove / LocalRename / LocalMkdir / LocalGetContent / LocalPutContent /
// LocalCopy / LocalMove) is provided by the embedded localFSOps via method
// promotion, exactly like SMB/FTP/WebDAV/S3/SCP — nothing to delegate here.

// --- remote attributes / dirs ----------------------------------------------

func (s *WSLFileSession) MakeDir(dir string) error {
	return os.Mkdir(s.uncPath(s.resolveRemote(dir)), 0o755)
}

// wslSymlinkCmd builds the wsl.exe invocation that creates a symbolic link
// inside the distro (the wsl.localhost UNC share cannot create Linux links).
func wslSymlinkCmd(distro, target, linkPath string) *exec.Cmd {
	return exec.Command("wsl.exe", "-d", distro, "--", "ln", "-s", target, linkPath)
}

// Symlink creates a symbolic link inside the WSL distro. The link path is a
// POSIX path resolved against the session cwd; the target is stored verbatim
// (a relative target resolves against the link's own directory).
func (s *WSLFileSession) Symlink(target, linkPath string) error {
	cmd := wslSymlinkCmd(s.distro, target, s.resolveRemote(linkPath))
	if out, err := cmd.CombinedOutput(); err != nil {
		msg := strings.TrimSpace(string(out))
		if msg != "" {
			return fmt.Errorf("%s", msg)
		}
		return err
	}
	return nil
}

func (s *WSLFileSession) Remove(p string, recursive bool) error {
	c := s.resolveRemote(p)
	if c == "/" || c == "." {
		return fmt.Errorf("refusing to delete path: %s", c)
	}
	full := s.uncPath(c)
	if recursive {
		return os.RemoveAll(full)
	}
	return os.Remove(full)
}

func (s *WSLFileSession) Rename(oldName, newName string) error {
	return os.Rename(s.uncPath(s.resolveRemote(oldName)), s.uncPath(s.resolveRemote(newName)))
}

func (s *WSLFileSession) Chmod(p string, mode os.FileMode) error {
	return os.Chmod(s.uncPath(s.resolveRemote(p)), mode)
}

// --- content read/write ------------------------------------------------------

// uncPathResolved returns the UNC path for a POSIX remote path. Symbolic
// links cannot be traversed through the wsl.localhost share (Stat surfaces
// them as ModeIrregular reparse points and open fails), so when the path is
// a link it resolves to the real target's UNC form via `readlink -f`;
// ordinary paths return the plain UNC form unchanged.
func (s *WSLFileSession) uncPathResolved(remotePath string) string {
	full := s.uncPath(remotePath)
	if fi, err := os.Stat(full); err == nil && fi.Mode()&os.ModeIrregular == 0 {
		return full
	}
	if target, terr := s.readlinkTarget(remotePath); terr == nil {
		return s.uncPath(target)
	}
	return full
}

func (s *WSLFileSession) GetContent(remotePath string) ([]byte, error) {
	b, err := os.ReadFile(s.uncPathResolved(s.resolveRemote(remotePath)))
	if isShareTraverseErr(err) {
		return nil, fmt.Errorf("cannot open %s: symbolic links cannot be traversed through the WSL file share", remotePath)
	}
	return b, err
}

func (s *WSLFileSession) PutContent(remotePath string, content []byte) error {
	return os.WriteFile(s.uncPathResolved(s.resolveRemote(remotePath)), content, 0o644)
}

func (s *WSLFileSession) Copy(oldPath, newPath string) error {
	return copyPath(s.uncPath(s.resolveRemote(oldPath)), s.uncPath(s.resolveRemote(newPath)), nil)
}

func (s *WSLFileSession) Move(oldPath, newPath string) error {
	oldU := s.uncPath(s.resolveRemote(oldPath))
	newU := s.uncPath(s.resolveRemote(newPath))
	if err := os.Rename(oldU, newU); err == nil {
		return nil
	}
	if err := copyPath(oldU, newU, nil); err != nil {
		return err
	}
	return os.RemoveAll(oldU)
}

// --- transfers ---------------------------------------------------------------

func (s *WSLFileSession) Get(remotePath, localPath string, recursive bool) (string, error) {
	return s.startLocalTransfer("download", s.resolveLocal(localPath), s.uncPathResolved(s.resolveRemote(remotePath)))
}

func (s *WSLFileSession) Put(localPath, remotePath string, recursive bool) (string, error) {
	return s.startLocalTransfer("upload", s.resolveLocal(localPath), s.uncPathResolved(s.resolveRemote(remotePath)))
}

// startLocalTransfer copies a file or tree between the Windows-local pane and
// the WSL UNC path. Both endpoints are on the same machine, so this is a local
// copy; a Task is reported through the usual OSC 633 transfer events so the
// frontend TransferPanel stays in sync.
func (s *WSLFileSession) startLocalTransfer(tfType, local, remote string) (string, error) {
	src := remote
	dst := local
	if tfType == "upload" {
		src = local
		dst = remote
	}
	total, err := dirSize(src)
	if err != nil {
		return "", err
	}
	prefix := "ul"
	if tfType == "download" {
		prefix = "dl"
	}
	task := &TransferTask{
		ID:         s.nextTaskID(prefix),
		Type:       tfType,
		LocalPath:  local,
		RemotePath: remote,
		Total:      total,
		Status:     "running",
	}
	task.start()
	s.mu.Lock()
	s.transfers[task.ID] = task
	s.mu.Unlock()
	s.emitTransferStart(task)
	go func() {
		defer func() {
			task.done()
			s.mu.Lock()
			delete(s.transfers, task.ID)
			s.mu.Unlock()
		}()
		if err := copyPath(src, dst, task); err != nil {
			task.Status = "error"
			s.emitTransferEvent(task, err)
			return
		}
		task.Progress = task.Total
		task.Status = "done"
		s.emitTransferProgress(task)
		s.emitTransferComplete(task)
	}()
	return task.ID, nil
}

// dirSize sums the bytes of a file or all files under a directory tree.
func dirSize(p string) (int64, error) {
	fi, err := os.Stat(p)
	if err != nil {
		return 0, err
	}
	if !fi.IsDir() {
		return fi.Size(), nil
	}
	var total int64
	var walk func(string) error
	walk = func(dir string) error {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if ei, err := e.Info(); err == nil && !ei.IsDir() {
				total += ei.Size()
				continue
			}
			full := joinEntry(dir, e.Name())
			if fi2, err := os.Stat(full); err == nil && fi2.IsDir() {
				if err := walk(full); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return total, walk(p)
}

// copyPath copies a file or directory tree. task, if non-nil, receives byte
// progress updates and pauses/cancels via transferTask.
func copyPath(src, dst string, task *TransferTask) error {
	fi, err := os.Stat(src)
	if err != nil {
		return err
	}
	if fi.IsDir() {
		if err := os.MkdirAll(dst, 0o755); err != nil {
			return err
		}
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if err := copyPath(joinEntry(src, e.Name()), joinEntry(dst, e.Name()), task); err != nil {
				return err
			}
		}
		return nil
	}
	return copyFile(src, dst, task)
}

func copyFile(src, dst string, task *TransferTask) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if task != nil {
		task.waitIfPaused() // blocks while paused / returns on cancel
	}
	buf := make([]byte, 64*1024)
	for {
		task.waitIfPaused()
		n, rerr := in.Read(buf)
		if n > 0 {
			if _, werr := out.Write(buf[:n]); werr != nil {
				out.Close()
				return werr
			}
			if task != nil {
				task.Progress += int64(n)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			out.Close()
			return rerr
		}
	}
	return out.Close()
}

func (s *WSLFileSession) CancelTransfer(taskID string) error {
	s.mu.Lock()
	t, ok := s.transfers[taskID]
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("task not found: %s", taskID)
	}
	if t.cancel != nil {
		t.cancel()
	}
	return nil
}

func (s *WSLFileSession) PauseTransfer(taskID string) error {
	s.mu.Lock()
	t, ok := s.transfers[taskID]
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("task not found: %s", taskID)
	}
	t.paused = true
	t.Status = "paused"
	s.emitTransferComplete(t)
	return nil
}

func (s *WSLFileSession) ResumeTransfer(taskID string) error {
	s.mu.Lock()
	t, ok := s.transfers[taskID]
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("task not found: %s", taskID)
	}
	t.paused = false
	t.Status = "running"
	close(t.pauseCh)
	t.pauseCh = make(chan struct{})
	s.emitTransferStart(t)
	return nil
}

func (s *WSLFileSession) nextTaskID(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, atomic.AddInt64(&s.taskSeq, 1))
}

// --- Session interface --------------------------------------------------------

func (s *WSLFileSession) Write(data []byte) error {
	return fmt.Errorf("not a terminal session")
}

func (s *WSLFileSession) Resize(_, _ int) error { return nil }

func (s *WSLFileSession) IsConnected() bool {
	return s.Status() == StatusConnected
}

func (s *WSLFileSession) Disconnect() error {
	if s.connectCancel != nil {
		s.connectCancel()
	}
	s.mu.Lock()
	for _, t := range s.transfers {
		if t.cancel != nil {
			t.cancel()
		}
	}
	s.mu.Unlock()
	s.setStatus(StatusDisconnected)
	return nil
}

