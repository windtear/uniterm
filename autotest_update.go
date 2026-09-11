package main

import (
	"os"
	"os/exec"
	"time"

	"github.com/ys-ll/uniterm/backend/log"
	"github.com/ys-ll/uniterm/backend/update"
)

// autotestUpdate drives the full update pipeline (check → download → verify →
// apply → restart) unattended. It is inert unless UNITERM_UPDATE_AUTOTEST=1;
// combine with UNITERM_UPDATE_API_BASE pointing at a local release server to
// end-to-end test a locally built old/new version pair:
//
//	run 1 (old binary): check → download → apply → relaunch itself with the
//	                    same env, then quit
//	run 2 (new binary): check finds no newer version → log PASS → exit 0
//
// The verdict is observable in ~/.uniterm/uniterm.log:
//
//	[autotest] APPLIED vX → vY
//	[autotest] PASS: running latest vY
func (a *App) autotestUpdate() {
	time.Sleep(4 * time.Second) // let the app + event plumbing settle
	if a.window != nil {
		a.window.Hide() // defensive: headless e2e runs must not flash a window
	}

	log.Writef("[autotest] start, current=%s", Version)

	info, err := update.Check(Version, "github")
	if err != nil {
		log.Writef("[autotest] CHECK FAILED: %v", err)
		os.Exit(2)
	}
	if !info.HasUpdate {
		log.Writef("[autotest] PASS: running latest %s", info.Latest)
		os.Exit(0)
	}

	log.Writef("[autotest] update available: %s -> %s (%d assets)", Version, info.Latest, len(info.Assets))

	if _, err := updateManager.Download(info.Assets, func(p update.Progress) {
		log.Writef("[autotest] progress: %s received=%d total=%d", p.Phase, p.Received, p.Total)
	}); err != nil {
		log.Writef("[autotest] DOWNLOAD FAILED: %v", err)
		os.Exit(3)
	}
	log.Writef("[autotest] download verified, applying")

	if err := updateManager.Apply(func(p update.Progress) {
		log.Writef("[autotest] progress: %s", p.Phase)
	}); err != nil {
		log.Writef("[autotest] APPLY FAILED: %v", err)
		os.Exit(4)
	}

	log.Writef("[autotest] APPLIED %s -> %s; relaunching same env", Version, info.Latest)

	// Spawn the (now updated) binary directly so the test env — including
	// UNITERM_UPDATE_AUTOTEST — carries over to run 2. Production relaunches
	// go through RelaunchApp (LaunchServices), which does not propagate env.
	exe, err := os.Executable()
	if err == nil {
		cmd := exec.Command(exe)
		cmd.Env = os.Environ()
		if err := cmd.Start(); err != nil {
			log.Writef("[autotest] relaunch failed: %v", err)
		}
	}
	go func() {
		time.Sleep(800 * time.Millisecond)
		a.app.Quit()
	}()
}
