package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const fakeClaude = `#!/bin/sh
settings= session= resume= prompt=
while [ $# -gt 0 ]; do
  case "$1" in
    --settings) settings=$2; shift ;;
    --session-id) session=$2; shift ;;
    --resume) resume=$2; session=$2; shift ;;
    *) prompt=$1 ;;
  esac
  shift
done
echo "argv session=$session resume=$resume prompt=$prompt" >> "$FAKE_DIR/claude.log"
hook() { cmd=$(sed -n "s/.*\"command\": \"\(.* hook $1\)\".*/\1/p" "$settings"); eval "$cmd"; }
transcript="$FAKE_DIR/$session.jsonl"
now() { date -u +%Y-%m-%dT%H:%M:%S.000Z; }
case "$prompt" in
  *WAIT-TRUST*) echo "Do you trust the files in this folder? (y/n)"; read answer; [ "$answer" = y ] || exit 1 ;;
esac
case "$prompt" in
  *SOCKET-STDIN*) while [ ! -f "$FAKE_DIR/session-sent" ]; do sleep 0.2; done ;;
  *) printf '{"session_id":"%s","transcript_path":"%s"}' "$session" "$transcript" | hook session-start ;;
esac
printf '{"type":"user","timestamp":"%s","message":{"role":"user","content":"%s"}}\n' "$(now)" "$prompt" >> "$transcript"
case "$prompt" in
  *QUIT-EARLY*) exit 0 ;;
  *ASK-FIRST*)
    printf '{"session_id":"%s","message":"Claude needs your permission to use Bash"}' "$session" | hook notification
    while [ ! -f "$FAKE_DIR/answered" ]; do sleep 0.2; done
    printf '{"session_id":"%s"}' "$session" | hook tool-done ;;
esac
printf '{"type":"assistant","timestamp":"%s","message":{"id":"m1","role":"assistant","usage":{"input_tokens":10,"cache_creation_input_tokens":1000,"cache_read_input_tokens":35000,"output_tokens":1200},"content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"go test ./..."}}]}}\n' "$(now)" >> "$transcript"
printf '{"type":"user","timestamp":"%s","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok  kanban  1.2s"}]}}\n' "$(now)" >> "$transcript"
printf '{"type":"assistant","timestamp":"%s","message":{"id":"m2","role":"assistant","usage":{"input_tokens":5,"cache_creation_input_tokens":0,"cache_read_input_tokens":36500,"output_tokens":300},"content":[{"type":"text","text":"All **tests pass**.\\n\\n| file | status |\\n|---|---|\\n| a.go | ok |"}]}}\n' "$(now)" >> "$transcript"
case "$prompt" in
  *HOLD*) while [ ! -f "$FAKE_DIR/release" ]; do sleep 0.2; done; sleep 60 ;;
esac
sleep 5
printf '{"session_id":"%s","last_assistant_message":"All tests pass."}' "$session" | hook stop
sleep 60
`

const fakeCodex = `#!/bin/sh
stop= start= resume= prompt=
while [ $# -gt 0 ]; do
  case "$1" in
    -c) case "$2" in
          hooks.Stop=*) stop=$(printf '%s' "$2" | sed 's/.*command="\(.*\)"}\]}\]/\1/' | sed 's/\\"/"/g') ;;
          hooks.SessionStart=*) start=$(printf '%s' "$2" | sed 's/.*command="\(.*\)"}\]}\]/\1/' | sed 's/\\"/"/g') ;;
        esac; shift ;;
    --dangerously-bypass-hook-trust) ;;
    resume) resume=$2; shift ;;
    *) prompt=$1 ;;
  esac
  shift
done
session=${resume:-0199aaaa-bbbb-7ccc-8ddd-eeeeffff0000}
transcript="$FAKE_DIR/rollout-$session.jsonl"
echo "argv session=$session resume=$resume prompt=$prompt" >> "$FAKE_DIR/codex.log"
printf '{"session_id":"%s","transcript_path":"%s"}' "$session" "$transcript" | eval "$start"
cat >> "$transcript" <<JSONL
{"timestamp":"$(date -u +%Y-%m-%dT%H:%M:%S.000Z)","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"$prompt"}]}}
{"timestamp":"$(date -u +%Y-%m-%dT%H:%M:%S.000Z)","type":"response_item","payload":{"type":"custom_tool_call","call_id":"c1","name":"exec","input":"ls"}}
{"timestamp":"$(date -u +%Y-%m-%dT%H:%M:%S.000Z)","type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"c1","output":[{"type":"input_text","text":"main.go"}]}}
{"timestamp":"$(date -u +%Y-%m-%dT%H:%M:%S.000Z)","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"output_tokens":420},"last_token_usage":{"input_tokens":19474}}}}
{"timestamp":"$(date -u +%Y-%m-%dT%H:%M:%S.000Z)","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Codex finished."}]}}
JSONL
printf '{"session_id":"%s"}' "$session" | eval "$stop"
sleep 60
`

func (h *harness) fakeAgents() string {
	h.t.Helper()
	dir := h.t.TempDir()
	bin := filepath.Join(dir, "bin")
	os.MkdirAll(bin, 0o755)
	os.WriteFile(filepath.Join(bin, "claude"), []byte(fakeClaude), 0o755)
	os.WriteFile(filepath.Join(bin, "codex"), []byte(fakeCodex), 0o755)
	for i, e := range h.env {
		if strings.HasPrefix(e, "PATH=") {
			h.env[i] = "PATH=" + bin + ":" + strings.TrimPrefix(e, "PATH=")
		}
	}
	h.env = append(h.env, "FAKE_DIR="+dir)
	return dir
}

func TestClaudeIntegration(t *testing.T) {
	h := newHarness(t)
	fake := h.fakeAgents()
	h.start()
	h.project("demo", `columns:
  - name: plan
    harness: claude
    steps:
      - agent: Plan task $KK_TASK
      - goto: next
  - name: build
    harness: claude
    steps:
      - agent: Build it
        resume: true
  - name: idle
    steps: []
`)
	settings, err := os.ReadFile(filepath.Join(h.home, "hooks", "claude.json"))
	if err != nil || !strings.Contains(string(settings), h.bin+"' hook stop") {
		t.Fatalf("hook settings: %v\n%s", err, settings)
	}

	id := h.newItem("plan", "Integrate\n")
	h.waitItem(id, "status: running", "context: 36505", "output: 1500")
	board, _ := h.run("", "project", "demo")
	if !strings.Contains(board, "live: true") || !strings.Contains(board, "tokens: ctx 36.5k · out 1.5k") {
		t.Fatalf("board does not show live tokens:\n%s", board)
	}
	out := h.waitItem(id, "column: build", "status: done")

	calls, _ := os.ReadFile(filepath.Join(fake, "claude.log"))
	lines := strings.Split(strings.TrimSpace(string(calls)), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], "prompt=Plan task "+id) || strings.Contains(lines[0], "resume=0") {
		t.Fatalf("claude calls:\n%s", calls)
	}
	first := strings.TrimPrefix(strings.Fields(lines[0])[1], "session=")
	if !strings.Contains(lines[1], "resume="+first) || !strings.Contains(out, "session: "+first) {
		t.Fatalf("second step did not resume %s:\n%s\n%s", first, calls, out)
	}

	c := h.browser()
	_, page := h.get(c, "/ui/demo/item/"+id)
	for _, want := range []string{"<strong>tests pass</strong>", "<table>", "go test ./...", "ok  kanban  1.2s", "ctx 36.5k"} {
		if !strings.Contains(page, want) {
			t.Fatalf("item page missing %q", want)
		}
	}
	runs, _ := filepath.Glob(filepath.Join(h.home, "projects", "demo", "items", id+".runs", "*-plan-0.md"))
	if len(runs) != 1 {
		t.Fatalf("no transcript run record: %v", runs)
	}
	record, _ := os.ReadFile(runs[0])
	if !strings.Contains(string(record), "$ Bash: go test ./...") || !strings.Contains(string(record), "All **tests pass**") {
		t.Fatalf("run record:\n%s", record)
	}

	h.run("", "item", id, "move", "idle")
	h.waitItem(id, "column: idle", "status: done")
	late := h.cmd("hook", "stop", id)
	late.Stdin = strings.NewReader(`{"session_id":"` + first + `"}`)
	if b, err := late.CombinedOutput(); err != nil || len(b) != 0 {
		t.Fatalf("hook must succeed silently: %v %q", err, b)
	}
	time.Sleep(700 * time.Millisecond)
	if after := h.waitItem(id, "column: idle", "status: done"); strings.Contains(after, "step: 1") {
		t.Fatalf("late stop hook advanced the step:\n%s", after)
	}
}

func TestClaudeAttentionAndEarlyExit(t *testing.T) {
	h := newHarness(t)
	fake := h.fakeAgents()
	h.start()
	h.project("demo", `columns:
  - name: ask
    harness: claude
    steps:
      - agent: ASK-FIRST
  - name: quit
    harness: claude
    steps:
      - agent: QUIT-EARLY
`)
	id := h.newItem("ask", "Needs permission\n")
	h.waitItem(id, "status: running", "attention: Claude needs your permission to use Bash")
	os.WriteFile(filepath.Join(fake, "answered"), nil, 0o644)
	h.waitItem(id, "status: done")
	log, _ := h.run("", "item", id, "log")
	if !strings.Contains(log, "event: resumed") {
		t.Fatalf("no resumed event:\n%s", log)
	}

	quitter := h.newItem("quit", "Leaves early\n")
	h.waitItem(quitter, "status: failed", "claude exited before finishing its turn")
}

func TestCodexIntegration(t *testing.T) {
	h := newHarness(t)
	h.fakeAgents()
	h.start()
	h.project("demo", "columns:\n  - name: work\n    harness: codex\n    steps:\n      - agent: Refactor\n")
	id := h.newItem("work", "Codex task\n")
	out := h.waitItem(id, "status: done", "session: 0199aaaa-bbbb-7ccc-8ddd-eeeeffff0000", "harness: codex")
	if !strings.Contains(out, "output: 420") {
		t.Fatalf("codex tokens missing:\n%s", out)
	}
	c := h.browser()
	_, page := h.get(c, "/ui/demo/item/"+id)
	if !strings.Contains(page, "Codex finished.") || !strings.Contains(page, "main.go") {
		t.Fatal("codex transcript not rendered")
	}
}

func TestHookWithoutServerExitsOne(t *testing.T) {
	h := newHarness(t)
	cmd := h.cmd("hook", "stop")
	cmd.Env = append(cmd.Env, "KK_TASK=1")
	cmd.Stdin = strings.NewReader("{}")
	cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 1 {
		t.Fatalf("hook exit code %d", code)
	}
	bad := h.cmd("hook")
	bad.Run()
	if code := bad.ProcessState.ExitCode(); code != 1 {
		t.Fatalf("malformed hook exit code %d", code)
	}
}

func TestHookPayloadOverSocket(t *testing.T) {
	h := newHarness(t)
	fake := h.fakeAgents()
	h.start()
	h.project("demo", "columns:\n  - name: work\n    harness: claude\n    steps:\n      - agent: SOCKET-STDIN\n")
	id := h.newItem("work", "Payload over a socket\n")
	out := h.waitItem(id, "status: running", "session: ")
	session := strings.TrimSpace(strings.SplitN(strings.SplitN(out, "session: ", 2)[1], "\n", 2)[0])

	stdin, peer, err := socketPair()
	if err != nil {
		t.Fatal(err)
	}
	cmd := h.cmd("hook", "session-start", id)
	cmd.Stdin = stdin
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	stdin.Close()
	transcript := filepath.Join(fake, session+".jsonl")
	peer.Write([]byte(`{"session_id":"` + session + `","transcript_path":"` + transcript + `"}`))
	peer.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatalf("hook over socket: %v", err)
	}
	h.waitItem(id, "transcript: "+transcript)
	os.WriteFile(filepath.Join(fake, "session-sent"), nil, 0o644)
	h.waitItem(id, "status: done", "output: 1500")
}

func TestAgentWaitingBeforeSession(t *testing.T) {
	h := newHarness(t)
	h.fakeAgents()
	h.start()
	h.project("demo", "columns:\n  - name: work\n    harness: claude\n    steps:\n      - agent: WAIT-TRUST\n        idle: 2s\n")
	id := h.newItem("work", "Blocked on a prompt\n")
	h.waitItem(id, "status: running", "may be waiting for input", "Do you trust the files in this folder? (y/n)")
	cmd := h.cmd("item", id, "reply")
	cmd.Stdin = strings.NewReader("y\n")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("reply: %v\n%s", err, b)
	}
	h.waitItem(id, "status: done")
	log, _ := h.run("", "item", id, "log")
	if !strings.Contains(log, "event: resumed") || strings.Contains(log, "event: failed") {
		t.Fatalf("expected attention to resolve on session start:\n%s", log)
	}
}

func TestTmuxServerKilledMidStep(t *testing.T) {
	h := newHarness(t)
	fake := h.fakeAgents()
	h.start()
	h.project("demo", `columns:
  - name: raw
    steps:
      - agent: sleep 60
  - name: claude
    harness: claude
    steps:
      - agent: Long task
`)
	rawID := h.newItem("raw", "Raw agent\n")
	claudeID := h.newItem("claude", "Claude agent\n")
	out := h.waitItem(claudeID, "status: running", "transcript: ")
	h.waitItem(rawID, "status: running")
	session := strings.TrimSpace(strings.SplitN(strings.SplitN(out, "session: ", 2)[1], "\n", 2)[0])

	if b, err := exec.Command("tmux", "-L", h.tmux(), "kill-server").CombinedOutput(); err != nil {
		t.Fatalf("kill-server: %v %s", err, b)
	}
	h.waitItem(rawID, "status: failed", "step session was lost")
	h.waitItem(claudeID, "status: done")
	calls, _ := os.ReadFile(filepath.Join(fake, "claude.log"))
	if !strings.Contains(string(calls), "resume="+session) {
		t.Fatalf("claude step was not resumed after tmux died:\n%s", calls)
	}
	log, _ := h.run("", "item", claudeID, "log")
	if !strings.Contains(log, "event: recovering") || strings.Contains(log, "event: failed") {
		t.Fatalf("unexpected recovery log:\n%s", log)
	}
}

func TestHooksOutOfOrderAndForged(t *testing.T) {
	h := newHarness(t)
	h.fakeAgents()
	h.start()
	h.project("demo", "columns:\n  - name: work\n    harness: claude\n    steps:\n      - agent: HOLD\n      - shell: sleep 60\n")
	id := h.newItem("work", "Hook abuse\n")
	out := h.waitItem(id, "status: running", "transcript: ")
	session := strings.TrimSpace(strings.SplitN(strings.SplitN(out, "session: ", 2)[1], "\n", 2)[0])

	hook := func(name, payload string) {
		cmd := h.cmd("hook", name, id)
		cmd.Stdin = strings.NewReader(payload)
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("hook %s: %v %s", name, err, b)
		}
	}
	hook("notification", `{"session_id":"someone-else","message":"forged"}`)
	hook("stop", `{"session_id":"someone-else"}`)
	time.Sleep(time.Second)
	if out, _ := h.run("", "item", id); strings.Contains(out, "forged") || !strings.Contains(out, "step: 0") {
		t.Fatalf("hooks from another session changed the step:\n%s", out)
	}

	done := make(chan struct{})
	for i := 0; i < 5; i++ {
		go func() {
			hook("stop", `{"session_id":"`+session+`"}`)
			done <- struct{}{}
		}()
	}
	for i := 0; i < 5; i++ {
		<-done
	}
	hook("notification", `{"session_id":"`+session+`","message":"late notification"}`)
	out = h.waitItem(id, "step: 1", "status: running")
	if strings.Contains(out, "late notification") {
		t.Fatalf("notification after stop was applied:\n%s", out)
	}
	log, _ := h.run("", "item", id, "log")
	if n := strings.Count(log, "event: finished"); n != 1 {
		t.Fatalf("five concurrent stops finished the step %d times:\n%s", n, log)
	}
}
