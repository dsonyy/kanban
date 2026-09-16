package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type ntfyRequest struct {
	title, click, body string
}

func fakeNtfy(t *testing.T) (*httptest.Server, func() []ntfyRequest) {
	var mu sync.Mutex
	var got []ntfyRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		got = append(got, ntfyRequest{r.Header.Get("Title"), r.Header.Get("Click"), string(b)})
		mu.Unlock()
	}))
	t.Cleanup(srv.Close)
	return srv, func() []ntfyRequest {
		mu.Lock()
		defer mu.Unlock()
		return append([]ntfyRequest(nil), got...)
	}
}

func TestFeedAndPush(t *testing.T) {
	h := newHarness(t)
	ntfy, pushed := fakeNtfy(t)
	os.MkdirAll(h.home, 0o700)
	os.WriteFile(filepath.Join(h.home, "config.yaml"), []byte("ntfy: "+ntfy.URL+"/topic\nurl: https://box.example.ts.net\n"), 0o644)
	h.start()

	h.project("alpha", "columns:\n  - name: review\n    steps:\n      - human: Check the migration\n  - name: shipped\n    steps: []\n")
	first := h.newItem("review", "Rename the users table\n")
	h.waitItem(first, "status: waiting")
	time.Sleep(1100 * time.Millisecond)
	h.project("beta", "columns:\n  - name: build\n    steps:\n      - shell: exit 2\n")
	second := h.newItem("build", "Compile release\n")
	h.waitItem(second, "status: failed")

	out, code := h.run("", "feed")
	h.expect(out, code, 0, "Check the migration", "exited with 2")
	if strings.Index(out, "Rename the users table") > strings.Index(out, "Compile release") {
		t.Fatalf("longest waiting item is not first:\n%s", out)
	}

	h.waitFor(func() bool { return len(pushed()) == 2 }, "two ntfy pushes")
	for _, p := range pushed() {
		if strings.Contains(p.title, "Rename the users table") {
			if p.body != "Check the migration" || p.click != "https://box.example.ts.net/ui/alpha/item/"+first {
				t.Fatalf("bad push for human step: %+v", p)
			}
		} else if !strings.Contains(p.title, "#"+second+" beta: Compile release") || !strings.Contains(p.body, "exited with 2") {
			t.Fatalf("bad push for failed step: %+v", p)
		}
	}

	time.Sleep(1100 * time.Millisecond)
	h.run("", "item", first, "approve")
	h.waitItem(first, "status: done")
	out, _ = h.run("", "feed")
	open, recent, _ := strings.Cut(out, "recent:")
	if strings.Contains(open, "Rename the users table") || !strings.Contains(recent, "Rename the users table") {
		t.Fatalf("approved item still open or missing from history:\n%s", out)
	}
	if strings.Contains(out, "waited_24h: 0s") {
		t.Fatalf("waiting time not counted:\n%s", out)
	}

	c := h.browser()
	code, body := h.get(c, "/ui/alpha/item/"+first)
	if code != 200 || strings.Contains(body, "<dd>0s</dd>") || !strings.Contains(body, "waited on you") {
		t.Fatalf("item page does not show waiting time:\n%s", body)
	}
	if code, body := h.get(c, "/ui/alpha/feed"); code != 200 || !strings.Contains(body, "Compile release") {
		t.Fatalf("feed page: %d\n%s", code, body)
	}
}

func TestNoPushWithoutConfig(t *testing.T) {
	h := newHarness(t)
	_, pushed := fakeNtfy(t)
	h.start()
	h.project("demo", "columns:\n  - name: review\n    steps:\n      - human: Look\n")
	id := h.newItem("review", "Quiet\n")
	h.waitItem(id, "status: waiting")
	time.Sleep(500 * time.Millisecond)
	if len(pushed()) != 0 {
		t.Fatal("pushed without ntfy configured")
	}
}

func TestReplyAndResumed(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.project("demo", `columns:
  - name: ask
    steps:
      - agent: echo "question?"; read answer; echo "got:$answer"; sleep 2; echo done-talking
        idle: 1s
`)
	id := h.newItem("ask", "Needs an answer\n")
	h.waitItem(id, "status: running", "attention: no output for")

	out, code := h.run("", "item", "99", "reply")
	h.expect(out, code, 1)

	cmd := h.cmd("item", id, "reply")
	cmd.Stdin = strings.NewReader("forty two\n")
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("reply: %v\n%s", err, b)
	}
	h.waitItem(id, "status: done")

	log, _ := h.run("", "item", id, "log")
	for _, want := range []string{"event: replied", "message: forty two", "event: resumed"} {
		if !strings.Contains(log, want) {
			t.Fatalf("log missing %q:\n%s", want, log)
		}
	}
	runs, _ := filepath.Glob(filepath.Join(h.home, "projects", "demo", "items", id+".runs", "*.log"))
	if len(runs) != 1 {
		t.Fatalf("runs: %v", runs)
	}
	run, _ := os.ReadFile(runs[0])
	if !strings.Contains(string(run), "got:forty two") {
		t.Fatalf("reply did not reach the step:\n%s", run)
	}
}
