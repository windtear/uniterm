package session

import (
	"strings"
	"testing"
	"time"
)

func TestParseLsLongListing(t *testing.T) {
	out := "total 24\n" +
		"drwxr-xr-x  2 root root 4096 Jan 12 09:30 .\n" +
		"drwxr-xr-x 14 root root 4096 Jan 12 09:30 ..\n" +
		"-rw-r--r--  1 ys ys 1234 Feb  3 10:20 file.txt\n" +
		"drwxr-xr-x  3 root root 4096 Jan 12 09:30 a dir with spaces\n" +
		"lrwxrwxrwx  1 root root    7 Jan 12 09:30 link -> target\n" +
		"Welcome to the device!\n" +
		"-rwxr-xr-x 1 admin admin 99 Dec 25 2019 old.sh\n"
	entries := parseLsLongListing(out)
	// "." and ".." are intentionally dropped (the frontend synthesizes ".."),
	// banner noise is skipped.
	if len(entries) != 4 {
		t.Fatalf("expected 4 entries, got %d: %+v", len(entries), entries)
	}
	f := entries[0]
	if f.Name != "file.txt" || f.Size != 1234 || f.Owner != "ys" || f.Group != "ys" {
		t.Errorf("file.txt wrong: %+v", f)
	}
	if f.ModTime.Month() != time.February || f.ModTime.Day() != 3 || f.ModTime.Minute() != 20 {
		t.Errorf("file.txt modtime wrong: %+v", f.ModTime)
	}
	if entries[1].Name != "a dir with spaces" {
		t.Errorf("spaces name wrong: %+v", entries[1])
	}
	l := entries[2]
	if l.Name != "link" || !strings.HasPrefix(l.Mode, "l") || l.Size != 7 {
		t.Errorf("symlink wrong: %+v", l)
	}
}

func TestParseLsLineISODate(t *testing.T) {
	// toybox / ISO date format
	e, ok := parseLsLine("drwxrwx--x 2 root root 4096 2020-01-01 10:00 data")
	if !ok {
		t.Fatal("ISO line not parsed")
	}
	if e.Name != "data" || e.Size != 4096 || e.Owner != "root" || e.Group != "root" {
		t.Errorf("ISO entry wrong: %+v", e)
	}
	if e.ModTime.Year() != 2020 || e.ModTime.Month() != time.January {
		t.Errorf("ISO modtime wrong: %+v", e.ModTime)
	}
}

func TestParseLsLineYearForm(t *testing.T) {
	e, ok := parseLsLine("-rw-r--r-- 1 root root 512 Jan  5  2018 old.log")
	if !ok {
		t.Fatal("year-form line not parsed")
	}
	if e.Name != "old.log" || e.Size != 512 {
		t.Errorf("year-form entry wrong: %+v", e)
	}
	if e.ModTime.Year() != 2018 {
		t.Errorf("year-form modtime wrong: %+v", e.ModTime)
	}
}

func TestParseLsLineACLAndRejects(t *testing.T) {
	if _, ok := parseLsLine("-rw-r--r--+ 1 root root 10 Jan 1 10:00 acl.txt"); !ok {
		t.Error("ACL mode (+ suffix) line should parse")
	}
	if _, ok := parseLsLine(""); ok {
		t.Error("empty line should not parse")
	}
	if _, ok := parseLsLine("total 12"); ok {
		t.Error("total line should not parse")
	}
	if _, ok := parseLsLine("random banner text"); ok {
		t.Error("banner noise should not parse")
	}
}

func TestParseLsLineHidden(t *testing.T) {
	e, ok := parseLsLine("-rw------- 1 root root 88 Mar 9 23:59 .ssh_config")
	if !ok || e.Name != ".ssh_config" {
		t.Fatalf("hidden file wrong: %+v ok=%v", e, ok)
	}
}

func TestScpDirectiveName(t *testing.T) {
	if got := scpDirectiveName("C0644 123 file name.txt"); got != "file name.txt" {
		t.Errorf("C name wrong: %q", got)
	}
	if got := scpDirectiveName("D0755 0 subdir"); got != "subdir" {
		t.Errorf("D name wrong: %q", got)
	}
	if got := scpDirectiveName("E"); got != "" {
		t.Errorf("E should have no name: %q", got)
	}
}

func TestSanitizeScpName(t *testing.T) {
	if got := sanitizeScpName("bad\nname\r.txt"); got != "badname.txt" {
		t.Errorf("sanitize wrong: %q", got)
	}
	if got := sanitizeScpName("normal.txt"); got != "normal.txt" {
		t.Errorf("sanitize wrong: %q", got)
	}
}

// --- symlink creation / navigation commands ----------------------------------

func TestScpSymlinkCommand(t *testing.T) {
	cases := []struct {
		target, link, want string
	}{
		{"target", "link", "ln -s 'target' 'link'"},
		{"target dir", "my link", "ln -s 'target dir' 'my link'"},
		{"/a/b/../c", "/home/u/l", "ln -s '/a/b/../c' '/home/u/l'"},
		{"it's", "link", "ln -s 'it'\\''s' 'link'"},
	}
	for _, c := range cases {
		if got := scpSymlinkCommand(c.target, c.link); got != c.want {
			t.Errorf("scpSymlinkCommand(%q, %q) = %q, want %q", c.target, c.link, got, c.want)
		}
	}
}

// ChangeRemoteDir must report the PHYSICAL directory (pwd -P), so entering a
// directory symlink lands on its real target — matching SFTP's RealPath and
// the WSL backend's readlink fallback.
func TestScpChangeDirCommand(t *testing.T) {
	cases := []struct{ target, want string }{
		{"/home/u", "cd '/home/u' 2>/dev/null && pwd -P"},
		{"/my dir", "cd '/my dir' 2>/dev/null && pwd -P"},
	}
	for _, c := range cases {
		if got := scpChangeDirCommand(c.target); got != c.want {
			t.Errorf("scpChangeDirCommand(%q) = %q, want %q", c.target, got, c.want)
		}
	}
}
