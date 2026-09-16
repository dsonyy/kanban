package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestDefaultBoardAndTaskFiles(t *testing.T) {
	h := newHarness(t)
	h.start()
	out, code := h.run("", "project", "new", "fresh")
	h.expect(out, code, 0, "name: planning", "name: plan-review", "name: implementation", "name: review", "name: merge")
	board, _ := os.ReadFile(filepath.Join(h.home, "projects", "fresh", "board.yaml"))
	var b struct {
		Setup   struct{ Generate string }
		Suggest struct{ To, Command string }
		Columns []column
	}
	if err := yaml.Unmarshal(board, &b); err != nil {
		t.Fatal(err)
	}
	commands := []string{b.Setup.Generate, b.Suggest.Command}
	for _, c := range b.Columns {
		for _, sp := range c.Steps {
			if sp.Shell != "" {
				commands = append(commands, sp.Shell)
			}
		}
	}
	if !strings.Contains(b.Setup.Generate, "#!/usr/bin/env bash.\" > \"$KANBAN_SETUP\"") || !strings.Contains(b.Suggest.Command, "content field") {
		t.Fatalf("default commands are cut short:\n%q\n%q", b.Setup.Generate, b.Suggest.Command)
	}
	for _, c := range commands {
		for _, shell := range []string{"sh", "zsh", "bash"} {
			if _, err := exec.LookPath(shell); err != nil {
				continue
			}
			if out, err := exec.Command(shell, "-n", "-c", c).CombinedOutput(); err != nil {
				t.Fatalf("default command is not valid %s: %v %s\n%s", shell, err, out, c)
			}
		}
	}
	check := h.cmd("project", "fresh", "board", "edit")
	check.Stdin = strings.NewReader(string(board))
	if b, err := check.CombinedOutput(); err != nil {
		t.Fatalf("default board does not pass validation: %v\n%s", err, b)
	}

	h.project("demo", "columns:\n  - name: work\n    steps:\n      - shell: printf '# Plan\\n\\nShip **it**\\n' > \"$KANBAN_TASK_DIR/PLAN.md\"\n")
	id := h.newItem("work", "Write a plan\n")
	h.waitItem(id, "status: done")
	_, page := h.get(h.browser(), "/ui/demo/item/"+id)
	if !strings.Contains(page, "PLAN.md") || !strings.Contains(page, "Ship <strong>it</strong>") {
		t.Fatal("task file not rendered on the item page")
	}
}

func TestBoardRejectsUnreachableColumnNames(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.project("demo", "columns:\n  - name: backlog\n    steps: []\n")
	for _, name := range []string{"a/b", "next", "archive", " padded"} {
		cmd := h.cmd("project", "demo", "board", "edit")
		cmd.Stdin = strings.NewReader("columns:\n  - name: \"" + name + "\"\n    steps: []\n")
		if out, err := cmd.CombinedOutput(); err == nil {
			t.Fatalf("column %q accepted:\n%s", name, out)
		}
	}
}

func TestSetupScriptGeneration(t *testing.T) {
	h := newHarness(t)
	h.start()
	board := `setup:
  generate: printf 'Here is the script:\n\nFENCEbash\necho ran > "$KANBAN_TASK_DIR/setup.txt"\nFENCE\n\n- **Note:** nothing is pinned.\n' > "$KANBAN_SETUP"
columns:
  - name: work
    steps:
      - setup: true
      - shell: test -f "$KANBAN_TASK_DIR/setup.txt"
`
	h.project("demo", strings.ReplaceAll(board, "FENCE", "```"))
	first := h.newItem("work", "Needs deps\n")
	h.waitItem(first, "status: waiting", "No setup script for demo")
	h.run("", "item", first, "approve")
	h.waitItem(first, "step: 2", "status: done")

	script := filepath.Join(h.home, "projects", "demo", "setup.sh")
	b, _ := os.ReadFile(script)
	fi, _ := os.Stat(script)
	if strings.Contains(string(b), "```") || strings.Contains(string(b), "Note") || fi.Mode()&0o100 == 0 {
		t.Fatalf("setup script not cleaned or not executable (%v):\n%s", fi.Mode(), b)
	}
	os.WriteFile(script, []byte("#!/bin/sh\necho ran > \"$KANBAN_TASK_DIR/setup.txt\"\n"), 0o644)
	os.Chmod(script, 0o644)
	second := h.newItem("work", "Reuses deps\n")
	h.waitItem(second, "step: 2", "status: done")
	if log, _ := h.run("", "item", second, "log"); strings.Contains(log, "attention") {
		t.Fatalf("second task asked for a setup script again:\n%s", log)
	}
}

func TestOnFailLoopsAndGotoLimit(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.project("demo", `columns:
  - name: impl
    steps:
      - shell: echo implementing
      - goto: next
  - name: test
    steps:
      - shell: exit 1
        on_fail: impl
        max_loops: 2
  - name: ping
    steps:
      - goto: pong
  - name: pong
    steps:
      - goto: ping
`)
	id := h.newItem("impl", "Flaky\n")
	h.waitItem(id, "column: test", "status: failed", "on_fail limit of 2 reached")
	log, _ := h.run("", "item", id, "log")
	if n := strings.Count(log, "message: 'on_fail: "); n != 2 {
		t.Fatalf("expected 2 on_fail loops, got %d:\n%s", n, log)
	}
	h.run("", "item", id, "move", "impl")
	h.waitFor(func() bool {
		log, _ := h.run("", "item", id, "log")
		return strings.Count(log, "message: 'on_fail: ") == 4
	}, "loop counter reset by a human move")

	loop := h.newItem("ping", "Ping pong\n")
	h.waitItem(loop, "status: failed", "goto loop")
}

func TestHistoryAndUndo(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.project("demo", "columns:\n  - name: backlog\n    steps: []\n  - name: work\n    steps: []\n")
	id := h.newItem("backlog", "Original text\n")

	var hist string
	h.waitFor(func() bool {
		hist, _ = h.run("", "history")
		return strings.Contains(hist, "projects/demo/items/"+id+".md")
	}, "first commit")
	tracked, _ := exec.Command("git", "-C", h.home, "ls-files").Output()
	for _, secret := range []string{"token", "kanban.sock", "hooks/"} {
		if strings.Contains(string(tracked), secret) {
			t.Fatalf("%s is tracked:\n%s", secret, tracked)
		}
	}

	latestWith := func(file, after string) string {
		var hash string
		h.waitFor(func() bool {
			hist, _ = h.run("", "history")
			first, _, _ := strings.Cut(strings.TrimPrefix(hist, "- hash: "), "\n")
			block := strings.SplitN(hist, "- hash: ", 3)[1]
			hash = first
			return first != after && strings.Contains(block, "- "+file)
		}, "commit touching "+file)
		return hash
	}
	created, _, _ := strings.Cut(strings.TrimPrefix(hist, "- hash: "), "\n")

	edit := h.cmd("item", id, "edit")
	edit.Stdin = strings.NewReader("Changed text\n")
	edit.Run()
	editHash := latestWith("projects/demo/items/"+id+".md", created)
	h.run("", "item", id, "move", "work")
	hash := latestWith("projects/demo/items/"+id+".yaml", editHash)
	if !strings.Contains(hist, "message: projects/demo/items/") {
		t.Fatalf("commit message lost the start of a path:\n%s", hist)
	}

	for _, undo := range []string{hash, editHash} {
		out, code := h.run("", "history", undo, "undo")
		h.expect(out, code, 0, "undone: "+undo)
	}
	h.waitItem(id, "Original text", "column: backlog")
	h.waitFor(func() bool {
		hist, _ := h.run("", "history")
		return strings.Contains(hist, "message: Undo "+hash[:12])
	}, "undo commit")
	log, _ := h.run("", "item", id, "log")
	if !strings.Contains(log, "event: edited") || !strings.Contains(log, "to: work") {
		t.Fatalf("undo rewrote the event log:\n%s", log)
	}
	out, code := h.run("", "history", "nothex", "undo")
	h.expect(out, code, 1, "invalid commit")
}

func TestGraphAndSuggestions(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.project("demo", `suggest:
  to: work
  command: |
    grep -q "Root task" "$KANBAN_FAMILY_FILE" && printf -- '- content: Write docs for %s\n- content: Add metrics\n' "$KANBAN_TASK" > "$KANBAN_SUGGESTIONS"
columns:
  - name: backlog
    steps: []
  - name: work
    steps:
      - shell: echo working
`)
	root := h.newItem("backlog", "Root task\n")
	mid := h.newItem("backlog", "Middle task\n")
	leaf := h.newItem("backlog", "Leaf task\n")
	h.run("", "item", mid, "link", root)
	h.run("", "item", leaf, "link", mid)
	out, code := h.run("", "item", root, "link", root)
	h.expect(out, code, 1, "own parent")
	out, code = h.run("", "item", root, "link", "999")
	h.expect(out, code, 1, "not found")

	h.waitItem(leaf, "status: done")
	graph, _ := h.run("", "project", "demo", "graph")
	if !strings.Contains(graph, "line: Leaf task\n      column: backlog\n      status: done\n      depth: 2") {
		t.Fatalf("leaf is not at depth 2:\n%s", graph)
	}
	for _, want := range []string{"line: Leaf task\n      column: backlog", "parent: " + mid + "\n      child: " + leaf} {
		if !strings.Contains(graph, want) {
			t.Fatalf("graph missing %q:\n%s", want, graph)
		}
	}
	h.run("", "item", root, "link", leaf)
	if graph, code := h.run("", "project", "demo", "graph"); code != 0 {
		t.Fatalf("graph with a cycle: %s", graph)
	}
	_, page := h.get(h.browser(), "/ui/demo/graph")
	if !strings.Contains(page, "<svg") || !strings.Contains(page, "#"+leaf+" Leaf task") {
		t.Fatal("graph page has no SVG")
	}
	h.run("", "item", root, "unlink", leaf)

	h.run("", "item", mid, "suggest")
	h.waitFor(func() bool {
		log, _ := h.run("", "item", mid, "log")
		return strings.Contains(log, "event: suggested")
	}, "suggestions")
	out, _ = h.run("", "item", mid, "suggestions")
	if !strings.Contains(out, "Write docs for "+mid) || !strings.Contains(out, "Add metrics") {
		t.Fatalf("suggestions:\n%s", out)
	}
	out, code = h.run("", "item", mid, "accept", "2")
	h.expect(out, code, 0, "column: work", "Add metrics")
	child, _, _ := strings.Cut(strings.TrimPrefix(out, "id: "), "\n")
	h.waitItem(child, "status: done", "parents:\n    - "+mid)
	if out, _ := h.run("", "item", mid, "suggestions"); strings.Contains(out, "Add metrics") {
		t.Fatalf("accepted suggestion still listed:\n%s", out)
	}
}

func TestFailedSetupGenerationLeavesNoScript(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.project("demo", "setup:\n  generate: |\n    : > \"$KANBAN_SETUP\"; exit 1\ncolumns:\n  - name: work\n    steps:\n      - setup: true\n")
	id := h.newItem("work", "Generator breaks\n")
	h.waitItem(id, "status: waiting", "No setup script")
	h.run("", "item", id, "approve")
	h.waitItem(id, "status: failed", "generate step 0 exited with 1")
	if _, err := os.Stat(filepath.Join(h.home, "projects", "demo", "setup.sh")); !os.IsNotExist(err) {
		t.Fatalf("failed generator left a script behind: %v", err)
	}
	h.run("", "item", id, "retry")
	h.waitItem(id, "status: waiting", "No setup script")
}
