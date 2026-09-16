package main

import (
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEndToEnd(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "kanban")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	home := filepath.Join(dir, "home")
	repo := filepath.Join(dir, "repo")
	os.MkdirAll(repo, 0o755)
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close()
	env := append(os.Environ(), "KANBAN_HOME="+home, "KANBAN_ADDR="+addr)

	run := func(stdin string, args ...string) (string, int) {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Env, cmd.Dir = env, repo
		if stdin != "" {
			cmd.Stdin = strings.NewReader(stdin)
		}
		out, _ := cmd.CombinedOutput()
		return string(out), cmd.ProcessState.ExitCode()
	}
	expect := func(out string, code, wantCode int, wants ...string) {
		t.Helper()
		if code != wantCode {
			t.Fatalf("exit %d, want %d\n%s", code, wantCode, out)
		}
		for _, w := range wants {
			if !strings.Contains(out, w) {
				t.Fatalf("missing %q in\n%s", w, out)
			}
		}
	}

	out, code := run("", "item", "1")
	expect(out, code, 1, "server not running")

	server := exec.Command(bin)
	server.Env = env
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Process.Kill(); server.Wait() })
	for i := 0; ; i++ {
		if _, err := os.Stat(filepath.Join(home, "kanban.sock")); err == nil {
			break
		}
		if i > 100 {
			t.Fatal("server did not start")
		}
		time.Sleep(20 * time.Millisecond)
	}

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
