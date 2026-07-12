package logging

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestNewRunIDReturnsRandomHex(t *testing.T) {
	first, err := NewRunID()
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewRunID()
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != rootRunIDBytes*2 {
		t.Fatalf("run ID length = %d, want %d", len(first), rootRunIDBytes*2)
	}
	if _, err := hex.DecodeString(first); err != nil {
		t.Fatalf("run ID is not hex: %q: %v", first, err)
	}
	if first == second {
		t.Fatalf("two generated run IDs are equal: %q", first)
	}
}

func TestCrashArtifactPersistsRedactedRunAndCompletion(t *testing.T) {
	logDir := t.TempDir()
	metadata := RootRunMetadata{
		RunID:         "0123456789abcdef",
		SupervisorPID: 42,
		StartedAt:     time.Date(2026, 7, 12, 13, 14, 15, 0, time.UTC),
		Version:       "v1.2.3",
		GoVersion:     "go1.26.3",
		OS:            "darwin",
		Arch:          "arm64",
		ConfigDir:     "/tmp/ainn config",
		ManagerPort:   19090,
	}
	artifact, previous, err := openCrashArtifact(logDir, metadata, 1024*1024, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if previous != nil {
		t.Fatalf("unexpected previous run: %#v", previous)
	}
	if _, err := artifact.Writer().Write([]byte("Authorization: Bearer sk-live\npartial url?token=tok-live")); err != nil {
		t.Fatal(err)
	}
	exit := RootRunExit{
		ChildPID:             43,
		ExitCode:             23,
		Reason:               RootRunExitReasonExitCode,
		Error:                "exit status 23",
		Signal:               "",
		ForwardedSignal:      "terminated",
		DurationMilliseconds: 9876,
		CompletedAt:          time.Date(2026, 7, 12, 13, 14, 24, 876000000, time.UTC),
	}
	if err := artifact.Complete(exit); err != nil {
		t.Fatal(err)
	}
	if err := artifact.Close(); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(artifact.Path())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("crash artifact mode = %o, want 600", info.Mode().Perm())
	}
	data, err := os.ReadFile(artifact.Path())
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	for _, secret := range []string{"sk-live", "tok-live"} {
		if strings.Contains(got, secret) {
			t.Fatalf("crash artifact leaked %q:\n%s", secret, got)
		}
	}
	for _, want := range []string{
		"root.supervisor.start",
		"run=0123456789abcdef",
		"pid=42",
		"version=v1.2.3",
		`config_dir="/tmp/ainn config"`,
		"Authorization: Bearer ***REDACTED***",
		"partial url?token=***REDACTED***",
		"root.supervisor.exit",
		"child_pid=43",
		"exit_code=23",
		"reason=exit_code",
		`error="exit status 23"`,
		"forwarded_signal=terminated",
		"duration_ms=9876",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("crash artifact missing %q:\n%s", want, got)
		}
	}
	if _, err := os.Stat(filepath.Join(logDir, crashDirectoryName, activeRootMarkerName)); !os.IsNotExist(err) {
		t.Fatalf("active marker remains after completion: %v", err)
	}
}

func TestCrashArtifactReportsPreviousUnfinishedRun(t *testing.T) {
	logDir := t.TempDir()
	firstMetadata := RootRunMetadata{
		RunID:         "1111111111111111",
		SupervisorPID: 101,
		StartedAt:     time.Date(2026, 7, 12, 13, 0, 0, 0, time.UTC),
		Version:       "v1",
		GoVersion:     "go1.26.3",
		OS:            "darwin",
		Arch:          "arm64",
		ConfigDir:     "/tmp/first",
		ManagerPort:   19090,
	}
	first, previous, err := openCrashArtifact(logDir, firstMetadata, 1024*1024, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if previous != nil {
		t.Fatalf("unexpected previous run: %#v", previous)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	firstMetadata.ArtifactPath = first.Path()

	secondMetadata := RootRunMetadata{
		RunID:         "2222222222222222",
		SupervisorPID: 202,
		StartedAt:     time.Date(2026, 7, 12, 14, 0, 0, 0, time.UTC),
		Version:       "v2",
		GoVersion:     "go1.26.3",
		OS:            "darwin",
		Arch:          "arm64",
		ConfigDir:     "/tmp/second",
		ManagerPort:   19091,
	}
	second, previous, err := openCrashArtifact(logDir, secondMetadata, 1024*1024, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(previous, &firstMetadata) {
		t.Fatalf("previous run mismatch:\n got %#v\nwant %#v", previous, &firstMetadata)
	}
	if err := second.Complete(RootRunExit{ChildPID: 203, ExitCode: 0, CompletedAt: secondMetadata.StartedAt.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(second.Path())
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	for _, want := range []string{
		"root.previous_unclean",
		"previous_run=1111111111111111",
		"previous_pid=101",
		"previous_artifact=" + first.Path(),
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("second artifact missing %q:\n%s", want, got)
		}
	}
}

func TestCrashArtifactRetainsNewestRunGroups(t *testing.T) {
	logDir := t.TempDir()
	for i := 0; i < 5; i++ {
		metadata := RootRunMetadata{
			RunID:         strings.Repeat(string(rune('a'+i)), rootRunIDBytes*2),
			SupervisorPID: 100 + i,
			StartedAt:     time.Date(2026, 7, 12, 13, i, 0, 0, time.UTC),
			Version:       "v1",
			GoVersion:     "go1.26.3",
			OS:            "darwin",
			Arch:          "arm64",
			ConfigDir:     "/tmp/config",
			ManagerPort:   19090 + i,
		}
		artifact, _, err := openCrashArtifact(logDir, metadata, 64, 1, 3)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := artifact.Writer().Write([]byte(strings.Repeat("stack line\n", 20))); err != nil {
			t.Fatal(err)
		}
		if err := artifact.Complete(RootRunExit{ChildPID: 200 + i, ExitCode: 0, CompletedAt: metadata.StartedAt.Add(time.Second)}); err != nil {
			t.Fatal(err)
		}
		if err := artifact.Close(); err != nil {
			t.Fatal(err)
		}
	}

	entries, err := os.ReadDir(filepath.Join(logDir, crashDirectoryName))
	if err != nil {
		t.Fatal(err)
	}
	groups := map[string]struct{}{}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, crashFilePrefix) {
			continue
		}
		base, _, _ := strings.Cut(name, ".log")
		groups[base] = struct{}{}
	}
	if len(groups) != 3 {
		t.Fatalf("retained crash groups = %d, want 3: %#v", len(groups), groups)
	}
	for _, removedRun := range []string{strings.Repeat("a", rootRunIDBytes*2), strings.Repeat("b", rootRunIDBytes*2)} {
		for group := range groups {
			if strings.Contains(group, removedRun) {
				t.Fatalf("old crash group %q was retained: %#v", removedRun, groups)
			}
		}
	}
}
