package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func (h *harness) project(name, boardYAML string) {
	h.t.Helper()
	out, code := h.run("", "project", "new", name)
	h.expect(out, code, 0)
	if err := os.WriteFile(filepath.Join(h.home, "projects", name, "board.yaml"), []byte(boardYAML), 0o644); err != nil {
		h.t.Fatal(err)
	}
	h.waitFor(func() bool {
		out, _ := h.run("", "project", name, "board")
		return out == boardYAML
	}, "board.yaml of "+name+" to reach the server")
}

func (h *harness) newItem(column, content string) string {
	h.t.Helper()
	out, code := h.run(content, "item", "new", column)
	h.expect(out, code, 0)
	id, _, _ := strings.Cut(strings.TrimPrefix(out, "id: "), "\n")
	return id
}

func (h *harness) waitItem(id string, wants ...string) string {
	h.t.Helper()
	var out string
	h.waitFor(func() bool {
		out, _ = h.run("", "item", id)
		for _, w := range wants {
			if !strings.Contains(out, w) {
				return false
			}
		}
		return true
	}, "item "+id+" to have "+strings.Join(wants, ", "))
	return out
}

func (h *harness) tmuxPanes() string {
	out, _ := exec.Command("tmux", "-L", h.tmux(), "list-panes", "-a", "-F", "#{pane_id} #{window_name}").CombinedOutput()
	return string(out)
}

func (h *harness) tmuxOut(args ...string) (string, error) {
	out, err := exec.Command("tmux", append([]string{"-L", h.tmux()}, args...)...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func (h *harness) tmux() string {
	for _, e := range h.env {
		if v, ok := strings.CutPrefix(e, "KK_TMUX="); ok {
			return v
		}
	}
	return ""
}

func TestWorkflow(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.project("demo", `columns:
  - name: backlog
    steps: []
  - name: todo
    steps:
      - goto: next
  - name: planning
    steps:
      - shell: mkdir -p "$KK_WORKTREE" && echo setup-ok
      - agent: printf 'plan for %s port %s project %s\n' "$KK_TASK" "$KK_PORT" "$KK_PROJECT" > PLAN.md && cat "$KK_TASK_FILE" && pwd
      - goto: next
  - name: plan-review
    steps:
      - human: Review PLAN.md
      - goto: implementation
  - name: implementation
    steps:
      - agent: test -f fixed
      - goto: next
  - name: review
    steps:
      - agent: sleep 30
        timeout: 1s
  - name: merge
    steps:
      - human: Merge it
      - shell: rm -rf "$KK_WORKTREE"
`)
	id := h.newItem("backlog", "Add dark mode\n")
	h.waitItem(id, "column: backlog", "status: done")

	h.run("", "item", id, "move", "todo")
	h.waitItem(id, "column: plan-review", "status: waiting", "attention: Review PLAN.md")

	worktree := filepath.Join(h.home, "worktrees", id)
	plan, err := os.ReadFile(filepath.Join(worktree, "PLAN.md"))
	if err != nil || string(plan) != "plan for "+id+" port 2000"+id+" project demo\n" {
		t.Fatalf("PLAN.md = %q, %v", plan, err)
	}

	out, code := h.run("", "item", id, "retry")
	h.expect(out, code, 1, "only failed items can be retried")
	h.run("", "item", id, "approve")
	h.waitItem(id, "column: implementation", "status: failed", "agent step 0 exited with 1")

	os.WriteFile(filepath.Join(worktree, "fixed"), nil, 0o644)
	h.run("", "item", id, "retry")
	h.waitItem(id, "column: review", "status: failed", "timed out after 1s")

	h.run("", "item", id, "move", "merge")
	h.waitItem(id, "column: merge", "status: waiting", "attention: Merge it")
	h.run("", "item", id, "approve")
	h.waitItem(id, "column: merge", "status: done")
	if _, err := os.Stat(worktree); !os.IsNotExist(err) {
		t.Fatalf("worktree not cleaned up: %v", err)
	}

	log, _ := h.run("", "item", id, "log")
	for _, want := range []string{"event: started", "event: finished", "exit: 0", "took:", "exit: 1", "event: approved", "event: retried", "timed out", "message: goto"} {
		if !strings.Contains(log, want) {
			t.Fatalf("log missing %q:\n%s", want, log)
		}
	}
	runs, _ := filepath.Glob(filepath.Join(h.home, "projects", "demo", "items", id+".runs", "*-planning-*.log"))
	var all string
	for _, r := range runs {
		b, _ := os.ReadFile(r)
		all += string(b)
	}
	for _, want := range []string{"setup-ok", "Add dark mode", worktree} {
		if !strings.Contains(all, want) {
			t.Fatalf("planning run logs missing %q:\n%s", want, all)
		}
	}
	if strings.Contains(h.tmuxPanes(), " step") {
		t.Fatalf("step panes left behind:\n%s", h.tmuxPanes())
	}
}

func TestAgentLimitQueue(t *testing.T) {
	h := newHarness(t)
	h.start()
	os.WriteFile(filepath.Join(h.home, "config.yaml"), []byte("agents: 1\n"), 0o644)
	h.project("demo", `columns:
  - name: work
    steps:
      - agent: sleep 6
`)
	a := h.newItem("work", "first\n")
	h.waitItem(a, "status: running")
	b := h.newItem("work", "second\n")
	h.waitItem(b, "status: queued")
	h.waitItem(a, "status: done")
	h.waitItem(b, "status: done")
}

func TestIdleAttention(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.project("demo", `columns:
  - name: work
    steps:
      - agent: sleep 6
        idle: 1s
`)
	id := h.newItem("work", "quiet\n")
	h.waitItem(id, "status: running", "attention: no output for")
	h.waitItem(id, "status: done")
}

func TestRestartKeepsRunningStep(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.project("demo", `columns:
  - name: work
    steps:
      - agent: sleep 6; echo survived
      - shell: echo next-step
`)
	id := h.newItem("work", "long\n")
	h.waitItem(id, "status: running")
	h.stop()
	h.start()
	h.waitItem(id, "step: 2", "status: done")
	log, _ := h.run("", "item", id, "log")
	if strings.Count(log, "event: started") != 2 || strings.Contains(log, "failed") {
		t.Fatalf("expected two clean steps after restart:\n%s", log)
	}
}

func TestMoveKillsRunningStep(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.project("demo", `columns:
  - name: idle
    steps: []
  - name: work
    steps:
      - agent: sleep 60
`)
	id := h.newItem("work", "to be moved\n")
	h.waitItem(id, "status: running")
	if !strings.Contains(h.tmuxPanes(), " step") {
		t.Fatalf("no step pane while running:\n%s", h.tmuxPanes())
	}
	h.run("", "item", id, "move", "idle")
	h.waitItem(id, "column: idle", "status: done")
	h.waitFor(func() bool { return !strings.Contains(h.tmuxPanes(), " step") }, "step pane to be killed")
}

func TestBrokenBoardDoesNotKillRunningStep(t *testing.T) {
	h := newHarness(t)
	h.start()
	board := `columns:
  - name: work
    steps:
      - agent: sleep 8
`
	h.project("demo", board)
	id := h.newItem("work", "survive a bad edit\n")
	h.waitItem(id, "status: running")
	boardPath := filepath.Join(h.home, "projects", "demo", "board.yaml")
	os.WriteFile(boardPath, []byte("columns: [unclosed\n"), 0o644)
	h.waitItem(id, "step: 1", "status: pending", "attention: 'board.yaml: ")
	log, _ := h.run("", "item", id, "log")
	if !strings.Contains(log, "event: finished") || strings.Contains(log, "event: failed") {
		t.Fatalf("running step did not finish cleanly:\n%s", log)
	}
	os.WriteFile(boardPath, []byte(board), 0o644)
	out := h.waitItem(id, "status: done")
	if strings.Contains(out, "attention") {
		t.Fatalf("attention not cleared after fixing board:\n%s", out)
	}
}

func TestHalfWrittenStateIsLeftAlone(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.project("demo", "columns:\n  - name: work\n    steps:\n      - agent: sleep 8\n")
	id := h.newItem("work", "Edited by hand mid-run\n")
	h.waitItem(id, "status: running")
	statePath := filepath.Join(h.home, "projects", "demo", "items", id+".yaml")
	state, _ := os.ReadFile(statePath)

	os.WriteFile(statePath, nil, 0o644)
	time.Sleep(2 * time.Second)
	if b, _ := os.ReadFile(statePath); len(b) != 0 {
		t.Fatalf("server wrote over a file that was being saved:\n%s", b)
	}
	if !strings.Contains(h.tmuxPanes(), " step") {
		t.Fatal("running step was killed while its state file was being saved")
	}

	os.WriteFile(statePath, []byte("column: [unclosed\n"), 0o644)
	time.Sleep(time.Second)
	if !strings.Contains(h.tmuxPanes(), " step") {
		t.Fatal("running step was killed while its state file was unreadable")
	}

	os.WriteFile(statePath, state, 0o644)
	h.waitItem(id, "status: done")
}
