package session

import (
	"bytes"
	"fmt"
	"net/url"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/ys-ll/uniterm/backend/log"
)

// TerminalCwdSink, when installed, receives each cwd reported by an injected
// shell integration (OSC-7) so the App layer can forward it to the frontend
// as a Wails event. Installed once in NewApp, next to TransferEventSink.
var TerminalCwdSink func(sessionID, cwd string)

const (
	osc7Prefix = "\x1b]7;"
	osc7BEL    = "\x07"
	osc7ST     = "\x1b\\"

	// sshIntegrationTimeout bounds every one-shot exec round-trip of the SSH
	// integration (shell detection, temp file writes). On any timeout or
	// error the session silently degrades to a plain shell.
	sshIntegrationTimeout = 5 * time.Second

	// maxOSCPending caps how long an unterminated OSC-7 payload may buffer
	// display output before it is dropped as garbage. Without this a program
	// emitting a bare "\x1b]7;" with no terminator would swallow the whole
	// rest of the terminal stream.
	maxOSCPending = 4096
)

// osc7Scanner extracts OSC-7 cwd reports from a terminal byte stream,
// tolerating sequences split across read chunks, and removes them from the
// display output (xterm.js would hide them, but stripping here keeps the
// raw stream clean for decoding/logging paths).
//
// State is a single leftover buffer that holds everything from the start of
// an unfinished sequence (prefix included) or, when no sequence is open, the
// tail bytes that could be a partial "\x1b]7;" prefix. Bytes before an
// unfinished sequence are emitted immediately, so the cleaned output of a
// chunk is always complete display text except for the held tail.
type osc7Scanner struct {
	leftover []byte
}

// Feed consumes the next chunk of the terminal byte stream. It returns the
// cwd of any OSC-7 sequence completed within this chunk (percent-decoded,
// "file://host" prefix stripped), the cleaned display bytes with all OSC-7
// sequences removed, and whether a cwd was found. cleaned must always be
// used in place of the input for display, even when nothing was found: a
// partially arrived sequence is withheld and flushed on a later Feed.
func (sc *osc7Scanner) Feed(data []byte) (cwd string, cleaned []byte, found bool) {
	buf := make([]byte, 0, len(sc.leftover)+len(data))
	buf = append(buf, sc.leftover...)
	buf = append(buf, data...)
	sc.leftover = nil

	var out []byte
	for {
		i := bytes.Index(buf, []byte(osc7Prefix))
		if i < 0 {
			// No sequence start in buf: flush everything except a tail that
			// could be a split prefix.
			keep := partialPrefixLen(buf)
			out = append(out, buf[:len(buf)-keep]...)
			sc.leftover = append(sc.leftover, buf[len(buf)-keep:]...)
			break
		}
		out = append(out, buf[:i]...)
		rest := buf[i+len(osc7Prefix):]
		// The payload ends at whichever terminator comes FIRST. Real prompts
		// emit an ST-terminated OSC-7 immediately followed by a BEL-terminated
		// OSC-0 title; searching for BEL first would swallow the ST plus the
		// whole title into the payload (field-reproduced).
		belIdx := bytes.IndexByte(rest, osc7BEL[0])
		stIdx := bytes.Index(rest, []byte(osc7ST))
		end, termLen := -1, 0
		if belIdx >= 0 && (stIdx < 0 || belIdx < stIdx) {
			end, termLen = belIdx, 1
		} else if stIdx >= 0 {
			end, termLen = stIdx, len(osc7ST)
		}
		if end < 0 {
			// Terminator not yet arrived: hold everything from the sequence
			// start and re-process it on the next Feed. Display bytes
			// collected so far (out) are returned now.
			if len(rest) > maxOSCPending {
				// Garbage (never-terminated sequence): drop it rather than
				// stalling the display stream forever.
				log.Writef("osc7: dropping unterminated sequence (%d bytes)", len(rest))
				buf = rest
				continue
			}
			sc.leftover = append(sc.leftover, buf[i:]...)
			return cwd, out, found
		}
		raw := string(rest[:end])
		buf = rest[end+termLen:]
		cwd = decodeOSC7Payload(raw)
		found = true
	}
	return cwd, out, found
}

// partialPrefixLen returns the length of the longest suffix of buf that is a
// proper prefix of osc7Prefix, i.e. how many trailing bytes must be withheld
// because a sequence start may be split across the chunk boundary.
func partialPrefixLen(buf []byte) int {
	max := len(osc7Prefix) - 1
	if len(buf) < max {
		max = len(buf)
	}
	for k := max; k > 0; k-- {
		if bytes.HasPrefix([]byte(osc7Prefix), buf[len(buf)-k:]) {
			return k
		}
	}
	return 0
}

// decodeOSC7Payload converts an OSC-7 payload ("file://host/path" or a bare
// path) into a plain path. url.Parse already percent-decodes u.Path, so the
// result is proper UTF-8 regardless of the session's display encoding; the
// value never re-enters the terminal stream, only the cwd sink.
func decodeOSC7Payload(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Path != "" {
		return u.Path
	}
	return raw
}

// buildShellBootstrap returns the temp files to materialize remotely and the
// argument list for starting the shell with integration injected. The user's
// own rc files are sourced FIRST; our hook is chained (prepended/appended),
// never overwriting user hooks. ok=false for unsupported/unknown shells.
//
// The <rcfile>/<dir> placeholders in startArgs are replaced by the real
// remote temp paths at start time (injectShellIntegration / wslShellIntegration).
func buildShellBootstrap(shell string) (files map[string]string, startArgs []string, ok bool) {
	base := shellBasename(shell)
	const oscFn = `__uniterm_osc7() { printf '\033]7;file://%s\033\\' "$PWD" 2>/dev/null; }`
	switch {
	case base == "bash" || base == "sh":
		rc := "[ -f \"$HOME/.bashrc\" ] && . \"$HOME/.bashrc\"\n" +
			"[ -f \"$HOME/.bash_profile\" ] && . \"$HOME/.bash_profile\"\n" +
			oscFn + "\n" +
			"case \"$(declare -p PROMPT_COMMAND 2>/dev/null)\" in\n" +
			"  \"declare -a\"*) PROMPT_COMMAND=(\"__uniterm_osc7\" \"${PROMPT_COMMAND[@]}\") ;;\n" +
			"  *) PROMPT_COMMAND=\"__uniterm_osc7${PROMPT_COMMAND:+;$PROMPT_COMMAND}\" ;;\n" +
			"esac\n"
		return map[string]string{"rcfile": rc}, []string{"--rcfile", "<rcfile>"}, true
	case base == "zsh":
		rc := "[ -f \"$HOME/.zshrc\" ] && . \"$HOME/.zshrc\"\n" +
			oscFn + "\n" +
			"precmd_functions+=(__uniterm_osc7)\n"
		// zsh always sources $ZDOTDIR/.zshenv; ours chains the user's own
		// ~/.zshenv so nothing the user relies on is lost.
		env := "[ -f \"$HOME/.zshenv\" ] && . \"$HOME/.zshenv\"\n"
		return map[string]string{".zshrc": rc, ".zshenv": env}, []string{"ZDOTDIR=<dir>"}, true
	case base == "fish":
		cmd := "functions -c fish_prompt __uniterm_orig_prompt; " +
			"function fish_prompt; __uniterm_osc7; __uniterm_orig_prompt; end; " +
			"function __uniterm_osc7; printf '\\e]7;file://%s\\e\\\\' $PWD; end"
		return nil, []string{"-C", cmd}, true
	}
	return nil, nil, false
}

// shellBasename returns the basename of a shell path ("/usr/bin/zsh" →
// "zsh").
func shellBasename(shell string) string {
	if i := strings.LastIndexByte(shell, '/'); i >= 0 {
		return shell[i+1:]
	}
	return shell
}

// injectShellIntegration tries to start the SSH session's shell with an
// OSC-7 hook injected. It returns the command to pass to session.Start, or
// "" when integration is unavailable (unknown shell, or any detection/write
// step failed) — the caller then starts a plain shell instead. It never
// fails the connection.
func injectShellIntegration(client *ssh.Client) string {
	if client == nil {
		return ""
	}
	shell, err := sshRunCommand(client, "echo $SHELL", "", sshIntegrationTimeout)
	if err != nil {
		log.Writef("ssh: shell integration skipped (detect shell: %v)", err)
		return ""
	}
	shell = strings.TrimSpace(shell)
	log.Writef("ssh: shell integration detected remote shell %q", shell)
	files, args, ok := buildShellBootstrap(shell)
	if !ok {
		log.Writef("ssh: shell integration unsupported shell %q", shell)
		return ""
	}
	switch shellBasename(shell) {
	case "bash":
		content, ok := files["rcfile"]
		if !ok {
			return ""
		}
		path, err := sshWriteRemoteFile(client, content)
		if err != nil {
			log.Writef("ssh: shell integration skipped (write rcfile: %v)", err)
			return ""
		}
		return "exec bash --rcfile " + path
	case "zsh":
		dir, err := sshWriteRemoteFiles(client, files, []string{".zshrc", ".zshenv"})
		if err != nil {
			log.Writef("ssh: shell integration skipped (write zsh dir: %v)", err)
			return ""
		}
		return "exec env ZDOTDIR=" + dir + " zsh"
	case "fish":
		// The exec request is parsed by the user's login shell, which for a
		// fish user is fish itself — quote the -C argument with fish rules.
		if len(args) < 2 {
			return ""
		}
		return "exec fish -C " + fishSingleQuote(args[1])
	}
	return ""
}

// sshRunCommand runs a one-shot command on an existing SSH client with a
// timeout, optionally feeding stdin, and returns stdout.
func sshRunCommand(client *ssh.Client, cmd, stdin string, timeout time.Duration) (string, error) {
	sess, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer sess.Close()
	if stdin != "" {
		sess.Stdin = strings.NewReader(stdin)
	}
	type sshCmdResult struct {
		out []byte
		err error
	}
	done := make(chan sshCmdResult, 1)
	go func() {
		out, err := sess.Output(cmd)
		done <- sshCmdResult{out, err}
	}()
	select {
	case r := <-done:
		if r.err != nil {
			return "", r.err
		}
		return string(r.out), nil
	case <-time.After(timeout):
		// Close the channel so the goroutine's Output returns; the buffered
		// result channel keeps it from leaking.
		sess.Close()
		return "", fmt.Errorf("command timed out after %s", timeout)
	}
}

// sshWriteRemoteFile materializes content in a remote temp file (created via
// mktemp so the path is space-free) and returns the path.
func sshWriteRemoteFile(client *ssh.Client, content string) (string, error) {
	out, err := sshRunCommand(client,
		`f=$(mktemp /tmp/uniterm-XXXXXX); cat > "$f"; printf '%s' "$f"`,
		content, sshIntegrationTimeout)
	if err != nil {
		return "", err
	}
	return cleanRemoteTempPath(out)
}

// sshWriteRemoteFiles materializes multiple named files inside a fresh
// remote temp directory and returns the directory path (used for zsh's
// ZDOTDIR redirection).
func sshWriteRemoteFiles(client *ssh.Client, files map[string]string, names []string) (string, error) {
	out, err := sshRunCommand(client,
		`d=$(mktemp -d /tmp/uniterm-XXXXXX); printf '%s' "$d"`,
		"", sshIntegrationTimeout)
	if err != nil {
		return "", err
	}
	dir, err := cleanRemoteTempPath(out)
	if err != nil {
		return "", err
	}
	for _, name := range names {
		content, ok := files[name]
		if !ok {
			return "", fmt.Errorf("missing bootstrap file %q", name)
		}
		if _, err := sshRunCommand(client, "cat > '"+dir+"/"+name+"'", content, sshIntegrationTimeout); err != nil {
			return "", err
		}
	}
	return dir, nil
}

// cleanRemoteTempPath validates a path captured from mktemp output: it must
// be non-empty and free of whitespace and quotes so it can be embedded in
// shell commands unquoted.
func cleanRemoteTempPath(out string) (string, error) {
	path := strings.TrimSpace(out)
	if path == "" || strings.ContainsAny(path, " \t\"'\\\r\n") {
		return "", fmt.Errorf("unexpected remote temp path %q", path)
	}
	return path, nil
}

// fishSingleQuote quotes s for fish's single-quote rules (inside single
// quotes only backslash and single quote are escapable), used when the SSH
// exec request is parsed by a fish login shell.
func fishSingleQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `'`, `\'`)
	return `'` + s + `'`
}
