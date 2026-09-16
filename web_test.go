package main

import (
	"bufio"
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func (h *harness) browser() *http.Client {
	h.t.Helper()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	token, err := os.ReadFile(filepath.Join(h.home, "token"))
	if err != nil {
		h.t.Fatal(err)
	}
	resp, err := c.Get("http://" + h.addr + "/?token=" + strings.TrimSpace(string(token)))
	if err != nil {
		h.t.Fatal(err)
	}
	resp.Body.Close()
	return c
}

func (h *harness) get(c *http.Client, path string) (int, string) {
	h.t.Helper()
	resp, err := c.Get("http://" + h.addr + path)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	var b strings.Builder
	bufio.NewReader(resp.Body).WriteTo(&b)
	return resp.StatusCode, b.String()
}

func TestWebPagesAndAuth(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.project("demo", "columns:\n  - name: backlog\n    steps: []\n  - name: work\n    steps: []\n")
	id := h.newItem("backlog", "Render me on the board\n")

	if code, _ := h.get(http.DefaultClient, "/ui/demo"); code != 401 {
		t.Fatalf("board without token: %d", code)
	}
	c := h.browser()
	u, _ := url.Parse("http://" + h.addr)
	if len(c.Jar.Cookies(u)) != 1 {
		t.Fatal("token link did not set a cookie")
	}
	code, body := h.get(c, "/")
	if code != 200 || !strings.Contains(body, `data-column="work"`) || !strings.Contains(body, "Render me on the board") {
		t.Fatalf("home did not land on the board: %d\n%s", code, body)
	}
	for _, path := range []string{"/ui/demo/item/" + id, "/ui/demo/terminal", "/ui/demo/settings", "/static/app.js", "/static/ghostty-vt.wasm"} {
		if code, body := h.get(c, path); code != 200 {
			t.Fatalf("%s: %d\n%s", path, code, body)
		}
	}
	if code, _ := h.get(c, "/ui/demo/item/999"); code != 404 {
		t.Fatalf("missing item page: %d", code)
	}

	resp, err := c.Post("http://"+h.addr+"/item/"+id+"/move/work", "", nil)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("move with cookie: %v %v", err, resp.Status)
	}
	resp.Body.Close()
	h.waitItem(id, "column: work")

	boardPath := filepath.Join(h.home, "projects", "demo", "board.yaml")
	before, _ := os.ReadFile(boardPath)
	resp, _ = c.Post("http://"+h.addr+"/project/demo/board/edit", "", strings.NewReader("columns:\n  - name: a\n    steps:\n      - goto: next\n        shell: echo\n"))
	resp.Body.Close()
	after, _ := os.ReadFile(boardPath)
	if resp.StatusCode != 400 || string(before) != string(after) {
		t.Fatalf("invalid board: status %d, file changed: %v", resp.StatusCode, string(before) != string(after))
	}
}

func TestLiveUpdates(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.project("demo", "columns:\n  - name: backlog\n    steps: []\n  - name: work\n    steps:\n      - shell: echo started-by-hand-edit\n")
	c := h.browser()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://"+h.addr+"/sse", nil)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	events := make(chan string, 64)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			if name, ok := strings.CutPrefix(sc.Text(), "event: "); ok {
				events <- name
			}
		}
	}()
	expectEvent := func(what string) {
		t.Helper()
		select {
		case e := <-events:
			if e != "change-demo" {
				t.Fatalf("%s: got event %q", what, e)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: no SSE event", what)
		}
		for len(events) > 0 {
			<-events
		}
	}

	time.Sleep(200 * time.Millisecond)
	id := h.newItem("backlog", "Watch me\n")
	expectEvent("item created through the API")
	h.waitItem(id, "status: done")
	time.Sleep(300 * time.Millisecond)
	for len(events) > 0 {
		<-events
	}

	statePath := filepath.Join(h.home, "projects", "demo", "items", id+".yaml")
	state, _ := os.ReadFile(statePath)
	os.WriteFile(statePath, []byte(strings.Replace(string(state), "column: backlog", "column: work", 1)), 0o644)
	expectEvent("state edited by hand")
	h.waitItem(id, "column: work", "step: 1", "status: done")
	log, _ := h.run("", "item", id, "log")
	if !strings.Contains(log, "message: external") || !strings.Contains(log, "exit: 0") {
		t.Fatalf("hand edit did not run the new column:\n%s", log)
	}
}

func TestTerminalWebSocket(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.project("demo", "columns:\n  - name: backlog\n    steps: []\n")
	id := h.newItem("backlog", "Terminal target\n")
	c := h.browser()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, _, err := websocket.Dial(ctx, "ws://"+h.addr+"/term/item/"+id, nil); err == nil {
		t.Fatal("terminal without token accepted")
	}
	ws, _, err := websocket.Dial(ctx, "ws://"+h.addr+"/term/item/"+id, &websocket.DialOptions{HTTPClient: c})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	ws.Write(ctx, websocket.MessageText, []byte("resize 100 30"))
	time.Sleep(500 * time.Millisecond)
	ws.Write(ctx, websocket.MessageBinary, []byte("echo marker-$((6*7))\r"))
	var seen strings.Builder
	for !strings.Contains(seen.String(), "marker-42") {
		_, data, err := ws.Read(ctx)
		if err != nil {
			t.Fatalf("terminal output never showed the command result: %v\n%q", err, seen.String())
		}
		seen.Write(data)
	}
	size, err := h.tmuxOut("list-windows", "-t", "=kanban-"+id, "-F", "#{window_width}x#{window_height}")
	if err != nil || !strings.HasPrefix(size, "100x") {
		t.Fatalf("resize not applied: %q %v", size, err)
	}
}

func TestTasksInRemovedColumnStayVisible(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.project("demo", "columns:\n  - name: todo\n    steps: []\n  - name: doing\n    steps: []\n")
	id := h.newItem("doing", "Lives in doing\n")
	h.waitItem(id, "status: done")
	edit := h.cmd("project", "demo", "board", "edit")
	edit.Stdin = strings.NewReader("columns:\n  - name: todo\n    steps: []\n  - name: in-progress\n    steps: []\n")
	if b, err := edit.CombinedOutput(); err != nil {
		t.Fatalf("board edit: %v\n%s", err, b)
	}

	out, _ := h.run("", "project", "demo")
	if !strings.Contains(out, "name: doing") || !strings.Contains(out, "missing: true") || !strings.Contains(out, "line: Lives in doing") {
		t.Fatalf("task in a removed column vanished from the board:\n%s", out)
	}
	_, page := h.get(h.browser(), "/ui/demo")
	if !strings.Contains(page, "Lives in doing") || !strings.Contains(page, "not in board.yaml") {
		t.Fatal("task in a removed column is not on the web board")
	}
}

func TestTerminalAcceptsLargePaste(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.project("demo", "columns:\n  - name: backlog\n    steps: []\n")
	id := h.newItem("backlog", "Paste target\n")
	c := h.browser()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, "ws://"+h.addr+"/term/item/"+id, &websocket.DialOptions{HTTPClient: c})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	ws.Write(ctx, websocket.MessageText, []byte("resize 120 40"))
	time.Sleep(500 * time.Millisecond)

	target := filepath.Join(t.TempDir(), "paste.txt")
	ws.Write(ctx, websocket.MessageBinary, []byte("stty -echo; cat > "+target+"\r"))
	time.Sleep(time.Second)
	line := strings.Repeat("0123456789abcdef", 5) + "\n"
	paste := strings.Repeat(line, 1200)
	if err := ws.Write(ctx, websocket.MessageBinary, []byte(paste)); err != nil {
		t.Fatalf("writing a %d byte paste: %v", len(paste), err)
	}
	time.Sleep(2 * time.Second)
	ws.Write(ctx, websocket.MessageBinary, []byte("\x04"))
	go func() {
		for {
			if _, _, err := ws.Read(ctx); err != nil {
				return
			}
		}
	}()
	h.waitFor(func() bool {
		b, _ := os.ReadFile(target)
		return len(b) == len(paste)
	}, "the whole paste to reach the program in the terminal")
	if ws.Ping(ctx) != nil {
		t.Fatal("terminal connection closed after the paste")
	}
}
