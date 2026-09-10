//go:build windows
// +build windows

package session

import (
	"fmt"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"
)

// The wsl.localhost 9P share reports Linux symlinks as plain, unstattable
// files, so symlink entries must be corrected from `ls -lAn` output while
// ordinary entries only pick up owner/group.

func TestApplyLsEnrichment(t *testing.T) {
	// UNC-derived listing: symlinks come through as anonymous plain files.
	files := []FileItem{
		{Name: "flink", Size: 0, ModTime: "", Mode: "----------", IsDir: false},
		{Name: "dlink", Size: 0, ModTime: "", Mode: "----------", IsDir: false},
		{Name: "real.txt", Size: 3, ModTime: "2026-09-10T12:00:00+08:00", Mode: "-rw-r--r--", IsDir: false},
		{Name: "not-in-ls", Size: 1, Mode: "-rw-r--r--", IsDir: false},
	}
	users := map[int]string{1000: "me"}
	groups := map[int]string{1000: "me"}
	symDirs := map[string]bool{"dlink": true}

	mt := time.Date(2026, time.September, 10, 12, 1, 0, 0, time.UTC)
	applyLsEnrichment(files, []lsEntry{
		{Name: "real.txt", Mode: "-rw-r--r--", Size: 3, Owner: "1000", Group: "1000", ModTime: mt},
		{Name: "flink", Mode: "lrwxrwxrwx", Size: 9, Owner: "1000", Group: "1000", ModTime: mt},
		{Name: "dlink", Mode: "lrwxrwxrwx", Size: 4, Owner: "1000", Group: "1000", ModTime: mt},
		{Name: "sub2", Mode: "drwxr-xr-x", Size: 4096, Owner: "1000", Group: "1000", ModTime: mt},
	}, symDirs, users, groups)

	// Symlinks take mode/size/mtime/dir-ness from ls.
	if files[0].Mode != "lrwxrwxrwx" || files[0].Size != 9 || files[0].IsDir {
		t.Errorf("flink wrong: %+v", files[0])
	}
	if files[1].Mode != "lrwxrwxrwx" || !files[1].IsDir {
		t.Errorf("dlink (dir symlink) wrong: %+v", files[1])
	}
	if files[0].Owner != "me" || files[0].Group != "me" {
		t.Errorf("flink owner/group wrong: %+v", files[0])
	}
	// Ordinary entries keep their UNC-derived fields, gain owner/group only.
	if files[2].Mode != "-rw-r--r--" || files[2].Size != 3 || files[2].Owner != "me" {
		t.Errorf("real.txt wrong: %+v", files[2])
	}
	// Entries absent from ls stay untouched.
	if files[3].Owner != "" || files[3].Mode != "-rw-r--r--" {
		t.Errorf("not-in-ls should be untouched: %+v", files[3])
	}
}

// The batched "which symlinks point at directories" probe script. It must be
// free of shell variables: wsl.exe re-quotes argv inside double quotes, so
// $n/$p/$((...)) would be expanded to nothing by the login shell before sh
// ever sees the script (this broke the dir detection in production once).
func TestTestDirScript(t *testing.T) {
	got := testDirScript([]string{"/a b", "/c'd"})
	want := "if [ -d '/a b' ]; then echo '1 d'; fi; if [ -d '/c'\\''d' ]; then echo '2 d'; fi"
	if got != want {
		t.Errorf("testDirScript = %q, want %q", got, want)
	}
	if got := testDirScript(nil); got != "" {
		t.Errorf("testDirScript(nil) = %q, want empty", got)
	}
}

// The symlink-target probe must fail for non-links: `readlink` without -f
// errors out, so the whole chain only prints a target for actual symlinks.
func TestReadlinkFollowScript(t *testing.T) {
	got := readlinkFollowScript("/tmp/my link")
	want := "readlink '/tmp/my link' >/dev/null && readlink -f '/tmp/my link'"
	if got != want {
		t.Errorf("readlinkFollowScript = %q, want %q", got, want)
	}
}

func TestParseWslSymDirReply(t *testing.T) {
	paths := []string{"a", "b", "c"}
	got := parseSymDirReply("1 d\n2 f\nnoise\n3 d\n0 d\n99 d\nx\n", paths)
	want := map[string]bool{"a": true, "c": true}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseSymDirReply = %v, want %v", got, want)
	}
}

// /mnt Windows-drive mounts are denied through the wsl.localhost share (the
// drive→WSL→drive loopback), so any failure there gets the "use the local
// pane" guidance instead of the raw 9P error.
func TestWSLShareError(t *testing.T) {
	s := &WSLFileSession{distro: "Ubuntu"}
	cases := []struct {
		dir      string
		wantHint bool
	}{
		{"/mnt", true},
		{"/mnt/c", true},
		{"/mnt/c/Users", true},
		{"/home/user", false},
	}
	for _, c := range cases {
		raw := fmt.Errorf("read //wsl.localhost/Ubuntu%s: Incorrect function.", c.dir)
		err := s.shareError(c.dir, raw)
		if c.wantHint && !strings.Contains(err.Error(), "local pane") {
			t.Errorf("shareError(%q) = %q, want local-pane guidance", c.dir, err)
		}
		if !c.wantHint && err != raw {
			t.Errorf("shareError(%q) should pass through raw error, got %q", c.dir, err)
		}
	}
}

// wslTestDistro returns a usable WSL distro for integration tests, skipping
// the test when none is installed (docker-desktop internals don't count).
func wslTestDistro(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("wsl.exe", "-l", "-q").Output()
	if err != nil {
		t.Skipf("wsl.exe not usable: %v", err)
	}
	for _, name := range strings.Split(strings.ReplaceAll(string(out), "\x00", ""), "\n") {
		name = strings.TrimSpace(name)
		if name != "" && !strings.HasPrefix(name, "docker-") {
			return name
		}
	}
	t.Skip("no non-docker WSL distro installed")
	return ""
}

// ChangeRemoteDir must fall back to the link's real target when a directory
// symlink cannot be traversed through the wsl.localhost share (the 9P
// redirector reports the link as a non-dir reparse point).
func TestWSLChangeRemoteDirSymlinkFallback(t *testing.T) {
	distro := wslTestDistro(t)
	wsl := func(script string) {
		t.Helper()
		if out, err := exec.Command("wsl.exe", "-d", distro, "--", "sh", "-c", script).CombinedOutput(); err != nil {
			t.Fatalf("wsl %q: %v: %s", script, err, out)
		}
	}
	dir := fmt.Sprintf("/tmp/uniterm-symtest-%d", time.Now().UnixNano())
	wsl(fmt.Sprintf("mkdir -p %s/real && echo hi > %s/real/inside.txt && ln -sfn %s/real %s/dlink", dir, dir, dir, dir))
	defer exec.Command("wsl.exe", "-d", distro, "--", "rm", "-rf", dir).Run()

	s := &WSLFileSession{
		baseSession: baseSession{sessionType: "wsl-file"},
		distro:      distro,
		root:        "//wsl.localhost/" + distro,
		cwd:         dir,
	}
	res, err := s.ChangeRemoteDir("dlink")
	if err != nil {
		t.Fatalf("ChangeRemoteDir(dlink) failed: %v", err)
	}
	if want := dir + "/real"; res.Dir != want {
		t.Errorf("resolved dir = %q, want %q", res.Dir, want)
	}
	found := false
	for _, f := range res.Files {
		if f.Name == "inside.txt" {
			found = true
		}
	}
	if !found {
		t.Errorf("target listing missing inside.txt: %+v", res.Files)
	}
}

// File content operations must follow symlinks to their real targets: the
// wsl.localhost share cannot traverse links, but the target itself is
// reachable, so open/edit/save through a file symlink should just work.
func TestWSLContentThroughFileSymlink(t *testing.T) {
	distro := wslTestDistro(t)
	wsl := func(script string) {
		t.Helper()
		if out, err := exec.Command("wsl.exe", "-d", distro, "--", "sh", "-c", script).CombinedOutput(); err != nil {
			t.Fatalf("wsl %q: %v: %s", script, err, out)
		}
	}
	dir := fmt.Sprintf("/tmp/uniterm-symtest-%d", time.Now().UnixNano())
	wsl(fmt.Sprintf("mkdir -p %s && printf hi > %s/real.txt && ln -sf %s/real.txt %s/flink && ln -sf %s/gone.txt %s/dangling",
		dir, dir, dir, dir, dir, dir))
	defer exec.Command("wsl.exe", "-d", distro, "--", "rm", "-rf", dir).Run()

	s := &WSLFileSession{
		baseSession: baseSession{sessionType: "wsl-file"},
		distro:      distro,
		root:        "//wsl.localhost/" + distro,
		cwd:         dir,
	}

	got, err := s.GetContent("flink")
	if err != nil {
		t.Fatalf("GetContent(flink) failed: %v", err)
	}
	if string(got) != "hi" {
		t.Errorf("GetContent(flink) = %q, want %q", got, "hi")
	}

	if err := s.PutContent("flink", []byte("bye")); err != nil {
		t.Fatalf("PutContent(flink) failed: %v", err)
	}
	if got, _ := s.GetContent("real.txt"); string(got) != "bye" {
		t.Errorf("write through symlink missed the target: real.txt = %q, want %q", got, "bye")
	}

	// A dangling link has no target to fall back to — surface a clear error
	// rather than the raw 9P "cannot be resolved" wording.
	if _, err := s.GetContent("dangling"); err == nil {
		t.Error("GetContent(dangling) should fail")
	}
}

// ListRemote must classify symlinks: dir targets are directories (navigable
// via double-click), file targets stay files. Regression: the test -d probe
// silently returned "file" for everything once wsl.exe arg re-quoting
// expanded the script's shell variables to nothing.
func TestWSLListRemoteSymlinkTypes(t *testing.T) {
	distro := wslTestDistro(t)
	wsl := func(script string) {
		t.Helper()
		if out, err := exec.Command("wsl.exe", "-d", distro, "--", "sh", "-c", script).CombinedOutput(); err != nil {
			t.Fatalf("wsl %q: %v: %s", script, err, out)
		}
	}
	dir := fmt.Sprintf("/tmp/uniterm-symtest-%d", time.Now().UnixNano())
	wsl(fmt.Sprintf("mkdir -p %s/sub && ln -sfn %s/sub %s/dlink && printf hi > %s/sub/inside.txt && ln -sf %s/sub/inside.txt %s/flink",
		dir, dir, dir, dir, dir, dir))
	defer exec.Command("wsl.exe", "-d", distro, "--", "rm", "-rf", dir).Run()

	s := &WSLFileSession{
		baseSession: baseSession{sessionType: "wsl-file"},
		distro:      distro,
		root:        "//wsl.localhost/" + distro,
		cwd:         dir,
	}
	res, err := s.ListRemote(dir)
	if err != nil {
		t.Fatalf("ListRemote failed: %v", err)
	}
	byName := map[string]FileItem{}
	for _, f := range res.Files {
		byName[f.Name] = f
	}
	dl, ok := byName["dlink"]
	if !ok {
		t.Fatal("dlink missing from listing")
	}
	if !dl.IsDir {
		t.Errorf("dlink should be a directory: mode=%q isDir=%v", dl.Mode, dl.IsDir)
	}
	if !strings.HasPrefix(dl.Mode, "l") {
		t.Errorf("dlink mode should mark a symlink, got %q", dl.Mode)
	}
	fl, ok := byName["flink"]
	if !ok {
		t.Fatal("flink missing from listing")
	}
	if fl.IsDir {
		t.Errorf("flink should not be a directory: mode=%q isDir=%v", fl.Mode, fl.IsDir)
	}
	if !strings.HasPrefix(fl.Mode, "l") {
		t.Errorf("flink mode should mark a symlink, got %q", fl.Mode)
	}
}

// The wsl.exe invocation that creates a symbolic link inside the distro (the
// wsl.localhost UNC share cannot create Linux links).
func TestWSLSymlinkCmd(t *testing.T) {
	cmd := wslSymlinkCmd("Ubuntu-22.04", "/home/u/target", "/home/u/link")
	want := []string{"wsl.exe", "-d", "Ubuntu-22.04", "--", "ln", "-s", "/home/u/target", "/home/u/link"}
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Errorf("wslSymlinkCmd args = %v, want %v", cmd.Args, want)
	}
}
