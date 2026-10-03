package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

var (
	buildOnce sync.Once
	binPath   string
	buildErr  error
)

// binary compiles the command once and returns the path to it. These tests
// exercise the real binary because that is what an operator runs, and the
// dispatch in main is exactly what went wrong before.
func binary(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "updatesite-bin-")
		if err != nil {
			buildErr = err
			return
		}
		binPath = filepath.Join(dir, "updatesite")
		if os.PathSeparator == '\\' {
			binPath += ".exe"
		}
		cmd := exec.Command("go", "build", "-o", binPath, ".")
		if out, err := cmd.CombinedOutput(); err != nil {
			buildErr = err
			t.Logf("go build output: %s", out)
		}
	})
	if buildErr != nil {
		t.Fatalf("cannot build the command: %v", buildErr)
	}
	return binPath
}

// run executes the binary with a short timeout and returns its output.
func run(t *testing.T, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(binary(t), args...)
	// A directory with nothing in it, so an accidental serve does not pick up
	// stray state.
	cmd.Env = append(os.Environ(), "DATA_DIR="+t.TempDir(), "CACHE_DIR="+t.TempDir())
	out, err := cmd.CombinedOutput()
	code := 0
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatalf("running %v: %v", args, err)
	}
	return string(out), code
}

// A mistyped subcommand must fail loudly. It used to fall through to serving,
// so `docker run <image> updatesite token x` looked like it had hung: the
// container quietly became a web server instead of printing a token.
func TestUnknownCommandDoesNotServe(t *testing.T) {
	out, code := run(t, "updatesite", "token", "-gen-secret", "-q")
	if code == 0 {
		t.Errorf("exit code = 0, want non-zero; output:\n%s", out)
	}
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.Contains(out, "未知命令") {
		t.Errorf("output does not report the unknown command:\n%s", out)
	}
	if strings.Contains(out, "listening on") {
		t.Errorf("the unknown command started a server:\n%s", out)
	}
}

func TestHelpExitsCleanly(t *testing.T) {
	out, code := run(t, "help")
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if !strings.Contains(out, "用法") {
		t.Errorf("help does not print usage:\n%s", out)
	}
}

// The token subcommand has to work without a site, a data directory or any
// other state; it is the only thing an administrator needs from the image.
func TestGenSecretExitsWithoutServing(t *testing.T) {
	out, code := run(t, "token", "-gen-secret", "-q")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; output:\n%s", code, out)
	}
	secret := strings.TrimSpace(out)
	if len(secret) < 40 {
		t.Errorf("secret %q looks too short", secret)
	}
	if strings.Contains(out, "listening on") {
		t.Errorf("token started a server:\n%s", out)
	}
}

// A token needs the deployment secret, and the error must say how to get one.
func TestTokenWithoutSecretExplainsItself(t *testing.T) {
	out, code := run(t, "token", "myapp")
	if code == 0 {
		t.Errorf("exit code = 0, want non-zero; output:\n%s", out)
	}
	if !strings.Contains(out, "UPLOAD_SECRET") {
		t.Errorf("the error does not mention UPLOAD_SECRET:\n%s", out)
	}
	if strings.Contains(out, "listening on") {
		t.Errorf("token started a server:\n%s", out)
	}
}
