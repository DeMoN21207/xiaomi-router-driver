package automation

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func cronWatchdogFixture(t *testing.T) (string, string, string, string) {
	t.Helper()
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	bootstrap := filepath.Join(root, "bootstrap.sh")
	prog := filepath.Join(root, "fake-manager")
	checks := filepath.Join(root, "checks")
	sleeps := filepath.Join(root, "sleeps")
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(name, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write(prog, "#!/bin/sh\nexit 0\n")
	// Exercise checks against a live identity on Linux without ever launching a
	// real service; other platforms stop at the isolated update lock.
	fakeProc := filepath.Join(root, "proc", "123")
	if err := os.MkdirAll(fakeProc, 0o755); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(fakeProc, "exe"), "")
	write(filepath.Join(root, "vpn-manager.pid"), "123\n")
	write(filepath.Join(root, "vpn-manager.updating"), "")
	write(filepath.Join(bin, "flock"), "#!/bin/sh\nexit 0\n")
	write(filepath.Join(bin, "readlink"), fmt.Sprintf("#!/bin/sh\nif [ \"$2\" = %s ]; then printf 'check\\n' >>%s; printf '%%s\\n' %s; elif [ \"$2\" = %s ]; then printf '%%s\\n' %s; else printf '/unrelated/exe\\n'; fi\n", shellSingleQuoted(prog), shellSingleQuoted(checks), shellSingleQuoted(prog), shellSingleQuoted(filepath.Join(fakeProc, "exe")), shellSingleQuoted(prog)))
	// Bound a broken recursive implementation instead of allowing a fork storm.
	write(filepath.Join(bin, "sleep"), fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$1\" >>%s\n[ \"$(wc -l <%s)\" -le 5 ]\n", shellSingleQuoted(sleeps), shellSingleQuoted(sleeps)))
	script := renderCronBootstrapScript(prog, root, "18080")
	script = strings.ReplaceAll(script, "/proc/", filepath.Join(root, "proc")+"/")
	for _, prefix := range []string{"/tmp/vpn-manager-watchdog", "/tmp/vpn-manager.pid", "/tmp/vpn-manager.log", "/tmp/vpn-manager.updating"} {
		script = strings.ReplaceAll(script, prefix, filepath.Join(root, filepath.Base(prefix)))
	}
	write(bootstrap, script)
	return bootstrap, root, checks, sleeps
}

func TestConcurrentCronLaunchersRecheckIdentityUnderLock(t *testing.T) {
	bootstrap, root, _, _ := cronWatchdogFixture(t)
	if err := os.Remove(filepath.Join(root, "vpn-manager.updating")); err != nil {
		t.Fatal(err)
	}
	prog := filepath.Join(root, "fake-manager")
	running := filepath.Join(root, "running")
	launched := filepath.Join(root, "launched")
	first := filepath.Join(root, "first-lock")
	second := filepath.Join(root, "second-lock")
	write := func(name, body string) {
		if err := os.WriteFile(name, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write(prog, fmt.Sprintf("#!/bin/sh\nprintf 'launch\\n' >>%s\ntouch %s\n", shellSingleQuoted(launched), shellSingleQuoted(running)))
	write(filepath.Join(root, "bin", "readlink"), fmt.Sprintf("#!/bin/sh\nif [ \"$2\" = %s ] || { [ \"$2\" = %s ] && [ -e %s ]; }; then printf '%%s\\n' %s; else printf '/unrelated/exe\\n'; fi\n", shellSingleQuoted(prog), shellSingleQuoted(filepath.Join(root, "proc", "123", "exe")), shellSingleQuoted(running), shellSingleQuoted(prog)))
	// Deterministically put both launchers past their initial identity check,
	// then let the second acquire the lock after the first has started a manager.
	write(filepath.Join(root, "bin", "flock"), fmt.Sprintf("#!/bin/sh\nif mkdir %s 2>/dev/null; then marker=%s; else touch %s; marker=%s; fi\nfor attempt in 1 2 3 4 5 6 7 8 9 10; do [ -e \"$marker\" ] && exit 0; /bin/sleep 0.1; done\nexit 1\n", shellSingleQuoted(first), shellSingleQuoted(second), shellSingleQuoted(second), shellSingleQuoted(running)))
	write(filepath.Join(root, "bin", "sleep"), "#!/bin/sh\nexec /bin/sleep \"$1\"\n")
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	commands := []*exec.Cmd{exec.CommandContext(ctx, "sh", bootstrap, "--once"), exec.CommandContext(ctx, "sh", bootstrap, "--once")}
	for _, command := range commands {
		command.WaitDelay = time.Second
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
	}
	for _, command := range commands {
		if err := command.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(time.Second)
	for countLines(launched) < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if count := countLines(launched); count != 1 {
		t.Fatalf("concurrent launchers started %d managers, want 1", count)
	}
}

func countLines(path string) int {
	data, _ := os.ReadFile(path)
	return len(strings.Fields(string(data)))
}

func TestCronWatchdogSchedulesBoundedTenSecondChecks(t *testing.T) {
	bootstrap, _, checks, sleeps := cronWatchdogFixture(t)
	if out, err := exec.Command("sh", bootstrap).CombinedOutput(); err != nil {
		t.Fatalf("bootstrap: %v %s", err, out)
	}
	deadline := time.Now().Add(3 * time.Second)
	for countLines(checks) < 6 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if n := countLines(checks); n != 6 {
		t.Fatalf("checks = %d, want immediate check plus five delayed checks", n)
	}
	data, _ := os.ReadFile(sleeps)
	for _, delay := range []string{"10", "20", "30", "40", "50"} {
		if !strings.Contains("\n"+string(data), "\n"+delay+"\n") {
			t.Fatalf("missing delay %s: %s", delay, data)
		}
	}
	if countLines(sleeps) != 5 {
		t.Fatalf("checks recursively spawned extra timers: %s", data)
	}
}

func TestCronWatchdogOneShotDoesNotSpawnTimers(t *testing.T) {
	bootstrap, _, checks, sleeps := cronWatchdogFixture(t)
	if out, err := exec.Command("sh", bootstrap, "--once").CombinedOutput(); err != nil {
		t.Fatalf("bootstrap: %v %s", err, out)
	}
	if countLines(checks) != 1 || countLines(sleeps) != 0 {
		t.Fatal("one-shot must perform one check without spawning timers")
	}
}

func TestCronWatchdogDoesNotPassItsKernelLockToManager(t *testing.T) {
	script := renderCronBootstrapScript("/app/vpn-manager", "/app", "18080")
	for _, fragment := range []string{`flock -n 9`, `-x "$PROG" 9>&-`, `</dev/null 9>&- &`, `[ -x "$SELF" ]`} {
		if !strings.Contains(script, fragment) {
			t.Fatalf("missing watchdog recovery safeguard: %s", fragment)
		}
	}
}

func TestCronWatchdogRemovedBootstrapCannotRestartService(t *testing.T) {
	bootstrap, root, checks, sleeps := cronWatchdogFixture(t)
	body := fmt.Sprintf("#!/bin/sh\nrm -f %s\nprintf '%%s\\n' \"$1\" >>%s\n", shellSingleQuoted(bootstrap), shellSingleQuoted(sleeps))
	if err := os.WriteFile(filepath.Join(root, "bin", "sleep"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("sh", bootstrap).CombinedOutput(); err != nil {
		t.Fatalf("bootstrap: %v %s", err, out)
	}
	deadline := time.Now().Add(3 * time.Second)
	for countLines(sleeps) < 5 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if countLines(sleeps) != 5 || countLines(checks) != 1 {
		t.Fatalf("removed service was restarted: checks=%d delayed=%d", countLines(checks), countLines(sleeps))
	}
}
