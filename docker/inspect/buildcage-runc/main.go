// Command buildcage-runc stands in front of BuildKit's own buildkit-runc for
// the `inspect` engine.
//
// BuildKit resolves its OCI worker binary with exec.LookPath and hands it to
// go-runc, and `[worker.oci] binary` in buildkitd.toml names which one. That
// lets buildcage sit in front of every RUN step without forking BuildKit: for
// the subcommands that carry a bundle it makes the step trust the proxy's CA,
// runs the real runc, then undoes that before BuildKit commits the snapshot.
//
// The engine intercepts at the network level, so nothing here injects proxy
// variables; a step needs no proxy configuration at all.
//
// Only files are undone. The environment is added to the OCI process spec,
// which BuildKit does not carry into the image config, so it cannot reach a
// layer in the first place.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
)

const (
	realRunc = "/usr/bin/buildkit-runc"
	caFile   = "/opt/buildcage/ca.pem"
	logFile  = "/var/log/buildcage/runc.log"
)

// Subcommands runc accepts. Only those carrying a bundle are acted on; the
// rest are passed through untouched.
var subcommands = map[string]bool{
	"run": true, "create": true, "start": true, "exec": true,
	"delete": true, "kill": true, "state": true, "ps": true,
	"pause": true, "resume": true, "list": true, "spec": true,
	"events": true, "update": true, "checkpoint": true, "restore": true,
}

func logf(format string, a ...any) {
	_ = os.MkdirAll(filepath.Dir(logFile), 0o755)
	f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, format+"\n", a...)
}

// parseArgs returns the runc subcommand and the --bundle value, if any.
func parseArgs(args []string) (sub, bundle string) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--bundle" || arg == "-b" {
			if i+1 < len(args) {
				bundle = args[i+1]
				i++
			}
			continue
		}
		if strings.HasPrefix(arg, "--bundle=") {
			bundle = strings.TrimPrefix(arg, "--bundle=")
			continue
		}
		if sub == "" && !strings.HasPrefix(arg, "-") && subcommands[arg] {
			sub = arg
		}
	}
	return sub, bundle
}

func main() {
	args := os.Args[1:]
	sub, bundle := parseArgs(args)

	// `run` only, not `create`: restore is tied to the wrapped process exiting,
	// but `runc create` returns before the process runs, so the CA would be gone
	// by `runc start`. BuildKit's runcexecutor uses `run`.
	var restore func() error
	if sub == "run" && bundle != "" {
		ca, err := os.ReadFile(caFile)
		if err != nil {
			// Without a CA there is nothing to trust and nothing to undo; the
			// step still runs, and its TLS failures will say so.
			logf("no CA at %s (%v); running without injection", caFile, err)
		} else if restore, err = inject(bundle, ca); err != nil {
			logf("injection failed for %s: %v", bundle, err)
			restore = nil
		}
	}

	cmd := exec.Command(realRunc, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr

	code := 0
	if err := cmd.Start(); err != nil {
		logf("cannot run %s: %v", realRunc, err)
		if restore != nil {
			restore()
		}
		os.Exit(1)
	}

	// Forward signals to runc. Named ones only: an unfiltered Notify also
	// catches SIGURG, which the Go runtime raises constantly. After cmd.Start,
	// so cmd.Process is set (reading it earlier would race cmd.Wait).
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT, syscall.SIGHUP)
	defer signal.Stop(signals)
	go func() {
		for s := range signals {
			_ = cmd.Process.Signal(s)
		}
	}()

	if err := cmd.Wait(); err != nil {
		var exitErr *exec.ExitError
		if ok := asExitError(err, &exitErr); ok {
			code = exitErr.ExitCode()
		} else {
			logf("cannot run %s: %v", realRunc, err)
			code = 1
		}
	}
	if restore != nil {
		if err := restore(); err != nil {
			logf("CA write-back failed, failing the build: %v", err)
			if code == 0 {
				code = 1
			}
		}
	}
	os.Exit(code)
}

func asExitError(err error, out **exec.ExitError) bool {
	exitErr, ok := err.(*exec.ExitError)
	if ok {
		*out = exitErr
	}
	return ok
}
