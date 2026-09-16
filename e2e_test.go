package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type harness struct {
	t                     *testing.T
	bin, home, repo, addr string
	env                   []string
	server                *exec.Cmd
}

func newHarness(t *testing.T) *harness {
	dir := t.TempDir()
	h := &harness{t: t, bin: filepath.Join(dir, "kanban"), home: filepath.Join(dir, "home"), repo: filepath.Join(dir, "repo")}
	if out, err := exec.Command("go", "build", "-o", h.bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	os.MkdirAll(h.repo, 0o755)
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	h.addr = ln.Addr().String()
	ln.Close()
	tmux := fmt.Sprintf("kanban-test-%d-%d", os.Getpid(), time.Now().UnixNano())
	h.env = append(os.Environ(), "KANBAN_HOME="+h.home, "KANBAN_ADDR="+h.addr, "KANBAN_TMUX="+tmux)
	t.Cleanup(func() {
		h.stop()
		exec.Command("tmux", "-L", tmux, "kill-server").Run()
	})
	return h
}

func (h *harness) start() {
	h.t.Helper()
	os.Remove(filepath.Join(h.home, "kanban.sock"))
	h.server = exec.Command(h.bin)
	h.server.Env = h.env
	if err := h.server.Start(); err != nil {
		h.t.Fatal(err)
	}
	h.waitFor(func() bool {
		_, code := h.run("", "server")
		return code == 0
	}, "server start")
}

func (h *harness) stop() {
	if h.server != nil && h.server.Process != nil {
		h.server.Process.Kill()
		h.server.Wait()
		h.server = nil
	}
}

func (h *harness) run(stdin string, args ...string) (string, int) {
	h.t.Helper()
	cmd := exec.Command(h.bin, args...)
	cmd.Env, cmd.Dir = h.env, h.repo
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	out, _ := cmd.CombinedOutput()
	return string(out), cmd.ProcessState.ExitCode()
}

func (h *harness) expect(out string, code, wantCode int, wants ...string) {
	h.t.Helper()
	if code != wantCode {
		h.t.Fatalf("exit %d, want %d\n%s", code, wantCode, out)
	}
	for _, w := range wants {
		if !strings.Contains(out, w) {
			h.t.Fatalf("missing %q in\n%s", w, out)
		}
	}
}

func (h *harness) waitFor(ok func() bool, what string) {
	h.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			h.t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestEndToEnd(t *testing.T) {
	h := newHarness(t)
	run, expect := h.run, h.expect
	home, repo, addr := h.home, h.repo, h.addr

	out, code := run("", "item", "1")
	expect(out, code, 1, "server not running")

	h.start()

	out, code = run("")
	expect(out, code, 0, "already running", "pid:")

	out, code = run("", "project", "new", "demo")
	expect(out, code, 0, "project: demo", "repo: "+repo, "name: backlog")
	out, code = run("", "project", "new", "demo")
	expect(out, code, 1, "already exists")
	out, code = run("", "project", "new", "item")
	expect(out, code, 1, "invalid project name")

	out, code = run("Fix the login bug\n\nDetails here.\n", "item", "new")
	expect(out, code, 0, "id: 1", "column: backlog", "Fix the login bug")
	out, code = run("Second task\n", "project", "demo", "item", "new", "todo")
	expect(out, code, 0, "id: 2", "column: todo")
	out, code = run("", "item", "new")
	expect(out, code, 1, "content is empty")

	byItemFirst, _ := run("", "item", "1", "project", "demo")
	byProjectFirst, _ := run("", "project", "demo", "item", "1")
	if byItemFirst != byProjectFirst {
		t.Fatalf("argument order changed the result:\n%s\n---\n%s", byItemFirst, byProjectFirst)
	}

	out, code = run("", "item", "1", "move", "doing")
	expect(out, code, 0, "column: doing")
	out, code = run("", "item", "1", "move", "nope")
	expect(out, code, 1, "column \"nope\"")
	out, code = run("Fix the login bug, now with tests\n", "item", "1", "edit")
	expect(out, code, 0, "now with tests")

	os.WriteFile(filepath.Join(home, "projects", "demo", "items", "1.md"), []byte("Edited by hand\n"), 0o644)
	out, code = run("", "item", "1")
	expect(out, code, 0, "Edited by hand")

	out, code = run("", "project", "demo")
	expect(out, code, 0, "line: Edited by hand", "line: Second task")

	out, code = run("", "item", "2", "archive")
	expect(out, code, 0, "column: archive")
	out, code = run("", "project", "demo")
	if strings.Contains(out, "Second task") {
		t.Fatalf("archived item still on board:\n%s", out)
	}

	out, code = run("", "item", "1", "log")
	expect(out, code, 0, "event: created", "event: moved", "from: backlog", "to: doing", "event: edited")

	out, code = run("", "item", "1", "project", "other")
	expect(out, code, 1, "not found")
	out, code = run("", "bogus")
	expect(out, code, 2, "unexpected")

	token, _ := os.ReadFile(filepath.Join(home, "token"))
	get := func(auth string) int {
		req, _ := http.NewRequest("GET", "http://"+addr+"/item/1", nil)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if c := get(""); c != 401 {
		t.Fatalf("tcp without token: %d", c)
	}
	if c := get("Bearer " + strings.TrimSpace(string(token))); c != 200 {
		t.Fatalf("tcp with token: %d", c)
	}
}
