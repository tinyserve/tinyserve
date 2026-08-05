package main

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestLaunchdPID(t *testing.T) {
	output := []byte(`gui/501/dev.tinyserve.daemon = {
	state = running
	pid = 4242
}`)

	if got := launchdPID(output); got != "4242" {
		t.Fatalf("launchdPID() = %q, want %q", got, "4242")
	}
}

func TestLaunchdPlistEscapesBinaryPath(t *testing.T) {
	plist, err := launchdPlist("/Applications/Tiny & Serve/tinyserved")
	if err != nil {
		t.Fatalf("launchdPlist() error = %v", err)
	}
	if !strings.Contains(plist, "/Applications/Tiny &amp; Serve/tinyserved") {
		t.Fatalf("launchdPlist() did not XML-escape binary path:\n%s", plist)
	}
}

func TestLaunchctlErrorExplainsMissingDesktopSession(t *testing.T) {
	err := launchctlError("bootstrap", errors.New("exit status 134"), nil)
	if !strings.Contains(err.Error(), "active macOS desktop login") {
		t.Fatalf("launchctlError() = %q, want desktop login hint", err)
	}
}

func TestBootstrapLaunchdAgentUsesExplicitGUIDomain(t *testing.T) {
	originalRunLaunchctl := runLaunchctl
	t.Cleanup(func() { runLaunchctl = originalRunLaunchctl })

	var calls [][]string
	runLaunchctl = func(args ...string) ([]byte, error) {
		calls = append(calls, append([]string(nil), args...))
		return nil, nil
	}

	plistPath := "/Users/test/Library/LaunchAgents/dev.tinyserve.daemon.plist"
	if err := bootstrapLaunchdAgent(plistPath); err != nil {
		t.Fatalf("bootstrapLaunchdAgent() error = %v", err)
	}

	want := [][]string{
		{"enable", launchdServiceTarget()},
		{"bootstrap", launchdDomain(), plistPath},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("launchctl calls = %#v, want %#v", calls, want)
	}
}
