package session

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pkg/sftp"
)

// newTestSFTPSession serves a temp directory over an in-process SFTP server
// (pkg/sftp speaks over a plain io.ReadWriteCloser; no SSH involved) and
// returns a session wired to a connected client. Tests address files by
// absolute path inside the returned root.
//
// Note: pkg/sftp v1.13.10 only offers NewServer(rwc io.ReadWriteCloser, ...);
// there is no NewServerWithFS / listener-based constructor, so the server
// serves the real filesystem and the tests confine themselves to temp paths.
func newTestSFTPSession(t *testing.T) (*SFTPSession, string) {
	t.Helper()
	root := t.TempDir()
	lst, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lst.Close() })

	c1, err := net.DialTimeout("tcp", lst.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	c2, err := lst.Accept()
	if err != nil {
		t.Fatal(err)
	}
	srv, err := sftp.NewServer(c2)
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve()
	t.Cleanup(func() { srv.Close() })

	client, err := sftp.NewClientPipe(c1, c1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close(); c1.Close(); c2.Close() })

	s := NewSFTPSession("test-sftp")
	s.sftpClient = client
	s.sshClient = nil // not needed for transfers
	return s, root
}

// captureTransferEvents installs a TransferEventSink recorder for the
// duration of the test and returns the event stream. Needed because done
// tasks are deleted from s.transfers, so the map alone cannot be polled for
// the outcome of a fast transfer.
func captureTransferEvents(t *testing.T) <-chan map[string]any {
	t.Helper()
	ch := make(chan map[string]any, 512)
	prev := TransferEventSink
	TransferEventSink = func(sessionID string, payload map[string]any) {
		select {
		case ch <- payload:
		default:
		}
	}
	t.Cleanup(func() { TransferEventSink = prev })
	return ch
}

// waitForTask drains the event stream until the given task emits its
// "complete" event, returning that payload.
func waitForTask(t *testing.T, events <-chan map[string]any, id string) map[string]any {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case ev := <-events:
			if ev["taskId"] == id && ev["event"] == "complete" {
				return ev
			}
		case <-deadline:
			t.Fatal("transfer did not finish in time")
			return nil
		}
	}
}

func TestDirDownloadPerFileTrackingAndRetrySkip(t *testing.T) {
	s, root := newTestSFTPSession(t)
	events := captureTransferEvents(t)
	remote := filepath.Join(root, "src")
	if err := os.MkdirAll(remote, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(remote, "a.txt"), []byte("AAA"), 0o644)

	os.WriteFile(filepath.Join(remote, "b.txt"), []byte("BB"), 0o644)
	os.MkdirAll(filepath.Join(remote, "sub"), 0o755)
	os.WriteFile(filepath.Join(remote, "sub", "c.txt"), []byte("C"), 0o644)

	local := filepath.Join(root, "out")
	id, err := s.startDirTransfer("download", local, remote, nil)
	if err != nil {
		t.Fatal(err)
	}
	ev := waitForTask(t, events, id)
	if ev["status"] != "done" {
		t.Fatalf("status = %v, failed=%v", ev["status"], ev["failedFiles"])
	}
	if ev["fileCount"] != 3 || ev["completedFiles"] != 3 {
		t.Fatalf("fileCount=%v completed=%v", ev["fileCount"], ev["completedFiles"])
	}
	b, _ := os.ReadFile(filepath.Join(local, "sub", "c.txt"))
	if string(b) != "C" {
		t.Fatalf("content = %q", b)
	}

	// Retry with skip: a.txt is now read-only locally, so a re-attempt to
	// create it would fail - a skipped file must be considered complete
	// without touching it at all.
	ro := filepath.Join(local, "a.txt")
	if err := os.Chmod(ro, 0o444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(ro, 0o644) })
	id2, err := s.startDirTransfer("download", local, remote, []string{"a.txt"})
	if err != nil {
		t.Fatal(err)
	}
	ev2 := waitForTask(t, events, id2)
	if ev2["status"] != "done" || ev2["completedFiles"] != 3 {
		t.Fatalf("retry-skip: status=%v completed=%v failed=%v",
			ev2["status"], ev2["completedFiles"], ev2["failedFiles"])
	}
}

func TestDirDownloadContinuesAfterPerFileFailure(t *testing.T) {
	s, root := newTestSFTPSession(t)
	events := captureTransferEvents(t)
	remote := filepath.Join(root, "src")
	if err := os.MkdirAll(remote, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(remote, "a.txt"), []byte("A"), 0o644)
	os.WriteFile(filepath.Join(remote, "b.txt"), []byte("B"), 0o644)
	os.WriteFile(filepath.Join(remote, "c.txt"), []byte("C"), 0o644)

	local := filepath.Join(root, "out")
	os.MkdirAll(filepath.Join(local, "b.txt"), 0o755) // blocks b.txt's os.Create

	id, err := s.startDirTransfer("download", local, remote, nil)
	if err != nil {
		t.Fatal(err)
	}
	ev := waitForTask(t, events, id)
	if ev["status"] != "error" {
		t.Fatalf("status = %v, want error", ev["status"])
	}
	failed, _ := ev["failedFiles"].([]FileFailure)
	if len(failed) != 1 || !strings.HasSuffix(failed[0].Path, "b.txt") {
		t.Fatalf("failedFiles = %v", ev["failedFiles"])
	}
	if ev["completedFiles"] != 2 {
		t.Fatalf("completed = %v, want 2 (failure must not abort)", ev["completedFiles"])
	}
	// The failed task must be RETAINED for retry.
	s.mu.RLock()
	_, kept := s.transfers[id]
	s.mu.RUnlock()
	if !kept {
		t.Fatal("failed task was deleted from transfers map")
	}
}
