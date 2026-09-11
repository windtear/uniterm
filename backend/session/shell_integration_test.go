package session

import (
	"bytes"
	"strings"
	"testing"
)

func TestOSC7ScannerSplitAcrossChunks(t *testing.T) {
	var sc osc7Scanner
	// partial sequence in first chunk, rest in second
	cwd1, cleaned1, found1 := sc.Feed([]byte("hello\x1b]7;file://myhost/ho"))
	cwd2, cleaned2, found2 := sc.Feed([]byte("me/user\x1b\\world"))
	if found1 || cwd1 != "" {
		t.Fatal("must not report before terminator")
	}
	if !found2 || cwd2 != "/home/user" {
		t.Fatalf("cwd=%q found=%v", cwd2, found2)
	}
	combined := string(cleaned1) + string(cleaned2)
	if combined != "helloworld" {
		t.Fatalf("cleaned = %q, want helloworld", combined)
	}
	if bytes.Contains([]byte(combined), []byte("\x1b]7;")) {
		t.Fatal("cleaned output still contains the OSC-7 sequence")
	}
}

func TestOSC7ScannerSplitTerminator(t *testing.T) {
	var sc osc7Scanner
	cwd1, _, found1 := sc.Feed([]byte("\x1b]7;file://h/a\x1b"))
	cwd2, cleaned2, found2 := sc.Feed([]byte("\\next"))
	if found1 || cwd1 != "" {
		t.Fatal("must not report before terminator")
	}
	if !found2 || cwd2 != "/a" {
		t.Fatalf("cwd=%q found=%v", cwd2, found2)
	}
	if string(cleaned2) != "next" {
		t.Fatalf("cleaned = %q, want next", cleaned2)
	}
}

func TestOSC7UrlDecoding(t *testing.T) {
	var sc osc7Scanner
	cwd, _, found := sc.Feed([]byte("\x1b]7;file://h/space%20dir\x07"))
	if !found || cwd != "/space dir" {
		t.Fatalf("cwd=%q found=%v", cwd, found)
	}
}

func TestOSC7ScannerEmptyHostBEL(t *testing.T) {
	var sc osc7Scanner
	// BEL-terminated sequence with an empty host part (file:///...)
	cwd, cleaned, found := sc.Feed([]byte("pre\x1b]7;file:///home/x\x07post"))
	if !found || cwd != "/home/x" {
		t.Fatalf("cwd=%q found=%v", cwd, found)
	}
	if string(cleaned) != "prepost" {
		t.Fatalf("cleaned = %q, want prepost", cleaned)
	}
}

func TestBashBootstrapChainsUserRc(t *testing.T) {
	files, args, ok := buildShellBootstrap("/bin/bash")
	if !ok {
		t.Fatal("bash must be supported")
	}
	rc := files["rcfile"]
	if !strings.Contains(rc, "[ -f \"$HOME/.bashrc\" ] && . \"$HOME/.bashrc\"") {
		t.Fatalf("bootstrap must source user rc first: %s", rc)
	}
	// chain, never overwrite: ours prepends, user's command survives
	if !strings.Contains(rc, "__uniterm_osc7") || !strings.Contains(rc, "${PROMPT_COMMAND:+") {
		t.Fatalf("PROMPT_COMMAND must be chained: %s", rc)
	}
	// bash 5.1+ array form handled
	if !strings.Contains(rc, "declare -a") {
		t.Fatal("must special-case array PROMPT_COMMAND")
	}
	if len(args) < 2 || args[0] != "--rcfile" {
		t.Fatalf("startArgs = %v", args)
	}
}

func TestBashBootstrapPreservesExistingPromptCommand(t *testing.T) {
	files, _, ok := buildShellBootstrap("/bin/bash")
	if !ok {
		t.Fatal("bash must be supported")
	}
	rc := files["rcfile"]
	// The chained assignment keeps any PROMPT_COMMAND the user's rc set.
	if !strings.Contains(rc, "${PROMPT_COMMAND:+;$PROMPT_COMMAND}") {
		t.Fatalf("pre-existing PROMPT_COMMAND must be preserved: %s", rc)
	}
}

func TestZshBootstrapZDOTDIR(t *testing.T) {
	files, args, ok := buildShellBootstrap("/usr/bin/zsh")
	if !ok {
		t.Fatal("zsh must be supported")
	}
	if files[".zshrc"] == "" || files[".zshenv"] == "" {
		t.Fatalf("zsh needs .zshrc + .zshenv in the redirected ZDOTDIR: %v", files)
	}
	if !strings.Contains(files[".zshrc"], "precmd_functions+=(__uniterm_osc7)") {
		t.Fatal("zsh must append to precmd_functions, not replace")
	}
	// the redirected .zshenv must chain the user's own ~/.zshenv
	if !strings.Contains(files[".zshenv"], "$HOME/.zshenv") {
		t.Fatalf("redirected .zshenv must chain the user's ~/.zshenv: %s", files[".zshenv"])
	}
	found := false
	for _, a := range args {
		if strings.HasPrefix(a, "ZDOTDIR=") {
			found = true
		}
	}
	if !found {
		t.Fatalf("startArgs must set ZDOTDIR: %v", args)
	}
}

func TestShellIntegrationUnsupportedShell(t *testing.T) {
	for _, shell := range []string{"/bin/tcsh", "/usr/bin/ksh", ""} {
		if _, _, ok := buildShellBootstrap(shell); ok {
			t.Fatalf("shell %q must degrade to a plain shell", shell)
		}
	}
}
