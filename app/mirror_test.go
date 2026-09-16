package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newMirrorStore(t *testing.T) *store {
	home := t.TempDir()
	s := &store{home: home, m: mirror{files: map[string]*entry{}}}
	if err := s.createProject("demo", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	board := "columns:\n  - name: backlog\n    steps: []\n  - name: work\n    steps: []\n"
	if err := s.saveBoard("demo", []byte(board)); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestExternalEditWinsOverConcurrentChange(t *testing.T) {
	s := newMirrorStore(t)
	it, err := s.createItem("demo", "backlog", []byte("Task\n"))
	if err != nil {
		t.Fatal(err)
	}
	path := s.itemPath("demo", it.ID, ".yaml")
	onDisk, _ := os.ReadFile(path)

	// An editor saves before the watcher has told the mirror about it.
	external := strings.Replace(string(onDisk), "status: pending", "status: pending\nattention: set by hand", 1)
	os.WriteFile(path, []byte(external), 0o644)

	_, err = s.moveItem("demo", it.ID, "work")
	if !errors.Is(err, errConflict) {
		t.Fatalf("move over an unseen external edit: %v", err)
	}
	if b, _ := os.ReadFile(path); string(b) != external {
		t.Fatalf("external edit was overwritten:\n%s", b)
	}
	if st, _ := s.readState("demo", it.ID); st.Attention != "set by hand" || st.Column != "backlog" {
		t.Fatalf("mirror did not take the disk version: %+v", st)
	}

	if _, err := s.moveItem("demo", it.ID, "work"); err != nil {
		t.Fatalf("move after the mirror caught up: %v", err)
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), "column: work") {
		t.Fatalf("move not written:\n%s", b)
	}
}

func TestSyncAllCatchesMissedChanges(t *testing.T) {
	s := newMirrorStore(t)
	a, _ := s.createItem("demo", "backlog", []byte("First\n"))
	b, _ := s.createItem("demo", "backlog", []byte("Second\n"))

	os.WriteFile(s.itemPath("demo", a.ID, ".md"), []byte("Changed behind the watcher\n"), 0o644)
	os.Remove(s.itemPath("demo", b.ID, ".yaml"))
	os.Remove(s.itemPath("demo", b.ID, ".md"))
	os.MkdirAll(filepath.Join(s.home, "projects", "other", "items"), 0o755)
	os.WriteFile(filepath.Join(s.home, "projects", "other", "project.yaml"), []byte("repo: /tmp\n"), 0o644)

	changed := s.syncAll()
	if !changed["demo"] || !changed["other"] {
		t.Fatalf("changed projects: %v", changed)
	}
	if it, _ := s.item("demo", a.ID); it.Content != "Changed behind the watcher\n" {
		t.Fatalf("content not reloaded: %q", it.Content)
	}
	if _, err := s.findItem(b.ID); err == nil {
		t.Fatal("deleted item still in the mirror")
	}
	if projs, _ := s.projects(); len(projs) != 2 {
		t.Fatalf("projects: %v", projs)
	}
	if len(s.syncAll()) != 0 {
		t.Fatal("second rescan reported changes")
	}
}

func TestHalfWrittenFileKeepsLastGoodState(t *testing.T) {
	s := newMirrorStore(t)
	it, _ := s.createItem("demo", "backlog", []byte("Task\n"))
	path := s.itemPath("demo", it.ID, ".yaml")
	os.WriteFile(path, nil, 0o644)
	s.syncFile(s.rel(path))
	st, err := s.readState("demo", it.ID)
	if !errors.Is(err, errPartialState) || st.Column != "backlog" {
		t.Fatalf("half-written state: %+v %v", st, err)
	}
	if _, err := s.moveItem("demo", it.ID, "work"); err == nil {
		t.Fatal("change applied on top of a half-written file")
	}
	if b, _ := os.ReadFile(path); len(b) != 0 {
		t.Fatalf("half-written file was overwritten:\n%s", b)
	}
}
