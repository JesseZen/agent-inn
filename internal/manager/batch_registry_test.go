package manager

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestBatchRegistryCreatesListsSelectsAndDeletesRun(t *testing.T) {
	registry := NewBatchRegistry(filepath.Join(t.TempDir(), "batch-runs.json"))

	created, err := registry.Create(BatchCreateInput{
		Title:           "fix scroll",
		Prompt:          "Fix scroll state",
		WorkerName:      "codex-app",
		WorkerPort:      6767,
		Model:           "gpt-5.5",
		SourceDirectory: "/repo",
		Variants: []BatchVariant{
			{Index: 1, HostedSessionID: "hs_1", SessionLabel: "fix scroll #1", WorktreeDir: "/tmp/b1/1"},
			{Index: 2, HostedSessionID: "hs_2", SessionLabel: "fix scroll #2", WorktreeDir: "/tmp/b1/2"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.ID != "batch_1" {
		t.Fatalf("unexpected id %q", created.ID)
	}
	if created.CreatedAt.IsZero() {
		t.Fatalf("missing created_at")
	}
	if created.Variants[0].ID != "variant_1" || created.Variants[1].ID != "variant_2" {
		t.Fatalf("unexpected variant ids: %#v", created.Variants)
	}

	reserved, err := registry.Create(BatchCreateInput{
		Title:           "reserved",
		WorkerName:      "codex-app",
		WorkerPort:      6767,
		SourceDirectory: "/repo",
	})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := registry.SetVariants(reserved.ID, []BatchVariant{
		{Index: 1, HostedSessionID: "hs_3", SessionLabel: "reserved #1", WorktreeDir: "/tmp/b2/1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Variants[0].ID != "variant_1" {
		t.Fatalf("unexpected reserved variant id: %#v", updated.Variants)
	}

	selected, err := registry.SelectWinner(created.ID, "variant_2")
	if err != nil {
		t.Fatal(err)
	}
	created.WinnerVariantID = "variant_2"
	created.CreatedAt = selected.CreatedAt
	if !reflect.DeepEqual(selected, created) {
		t.Fatalf("selected mismatch:\n got %#v\nwant %#v", selected, created)
	}
	updated.CreatedAt = selected.CreatedAt.Add(time.Second)
	err = registry.withLockedFile(func(file *batchRunsFile) error {
		file.Runs[updated.ID] = updated
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	listed, err := registry.List()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(listed, []BatchRun{updated, selected}) {
		t.Fatalf("list mismatch:\n got %#v\nwant %#v", listed, []BatchRun{updated, selected})
	}

	deleted, err := registry.Delete(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(deleted, selected) {
		t.Fatalf("deleted mismatch:\n got %#v\nwant %#v", deleted, selected)
	}
	listed, err = registry.List()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(listed, []BatchRun{updated}) {
		t.Fatalf("list after delete mismatch:\n got %#v\nwant %#v", listed, []BatchRun{updated})
	}
	deleted, err = registry.Delete(updated.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(deleted, updated) {
		t.Fatalf("deleted reserved mismatch:\n got %#v\nwant %#v", deleted, updated)
	}
	listed, err = registry.List()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(listed, []BatchRun{}) {
		t.Fatalf("list after final delete mismatch:\n got %#v\nwant %#v", listed, []BatchRun{})
	}
}

func TestBatchRegistrySortsNewestFirst(t *testing.T) {
	registry := NewBatchRegistry(filepath.Join(t.TempDir(), "batch-runs.json"))
	older, err := registry.Create(BatchCreateInput{Title: "older", WorkerName: "w", WorkerPort: 1, SourceDirectory: "/repo"})
	if err != nil {
		t.Fatal(err)
	}
	newer, err := registry.Create(BatchCreateInput{Title: "newer", WorkerName: "w", WorkerPort: 1, SourceDirectory: "/repo"})
	if err != nil {
		t.Fatal(err)
	}
	newer.CreatedAt = older.CreatedAt.Add(time.Second)

	err = registry.withLockedFile(func(file *batchRunsFile) error {
		file.Runs[newer.ID] = newer
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	listed, err := registry.List()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(listed, []BatchRun{newer, older}) {
		t.Fatalf("list mismatch:\n got %#v\nwant %#v", listed, []BatchRun{newer, older})
	}
}
