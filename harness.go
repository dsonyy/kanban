package main

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const rawHarness = "raw"

var harnesses = map[string]bool{"": true, rawHarness: true, "claude": true, "codex": true}

var hookEvents = []struct{ claude, codex, kanban string }{
	{"SessionStart", "SessionStart", "session-start"},
	{"Stop", "Stop", "stop"},
	{"Notification", "", "notification"},
	{"UserPromptSubmit", "UserPromptSubmit", "prompt"},
	{"PostToolUse", "PostToolUse", "tool-done"},
}

func harnessOf(c column, sp step) string {
	if sp.Harness != "" {
		return sp.Harness
	}
	if c.Harness != "" {
		return c.Harness
	}
	return rawHarness
}

func shq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// Double quotes keep $KANBAN_* expansion in prompts written in board.yaml.
func dq(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "`", "\\`").Replace(s) + `"`
}

func newUUID() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6], b[8] = b[6]&0x0f|0x40, b[8]&0x3f|0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func (s *store) writeHookSettings() error {
	bin, err := os.Executable()
	if err != nil {
		return err
	}
	hooks := map[string]any{}
	for _, h := range hookEvents {
		entry := map[string]any{"hooks": []map[string]string{{"type": "command", "command": shq(bin) + " hook " + h.kanban}}}
		if h.claude == "PostToolUse" {
			entry["matcher"] = "*"
		}
		hooks[h.claude] = []any{entry}
	}
	b, err := json.MarshalIndent(map[string]any{"hooks": hooks}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(s.home, "hooks"), 0o755); err != nil {
		return err
	}
	return writeFile(s.claudeSettings(), b)
}

func (s *store) claudeSettings() string { return filepath.Join(s.home, "hooks", "claude.json") }

func (s *store) agentCommand(harness string, sp step, resume string) (cmd, session string, err error) {
	var args []string
	for _, a := range sp.Args {
		args = append(args, shq(a))
	}
	switch harness {
	case "claude":
		parts := []string{"claude", "--settings", shq(s.claudeSettings())}
		if resume != "" {
			parts = append(parts, "--resume", shq(resume))
			session = resume
		} else {
			session = newUUID()
			parts = append(parts, "--session-id", session)
		}
		return strings.Join(append(append(parts, args...), dq(sp.Agent)), " "), session, nil
	case "codex":
		bin, err := os.Executable()
		if err != nil {
			return "", "", err
		}
		parts := []string{"codex", "--dangerously-bypass-hook-trust"}
		for _, h := range hookEvents {
			if h.codex != "" {
				toml := fmt.Sprintf(`hooks.%s=[{hooks=[{type="command",command=%q}]}]`, h.codex, shq(bin)+" hook "+h.kanban)
				parts = append(parts, "-c", shq(toml))
			}
		}
		if resume != "" {
			parts = append(parts, "resume", shq(resume))
		}
		return strings.Join(append(append(parts, args...), dq(sp.Agent)), " "), resume, nil
	}
	return "", "", fmt.Errorf("unknown harness %q", harness)
}

type toolCall struct {
	Name, Input, Output string
}

type turn struct {
	Role  string
	At    time.Time
	Text  string
	Tools []toolCall
}

type usage struct {
	Context, Output int
}

func eachLine(path string, fn func([]byte)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			fn(line)
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func textOf(v any) string {
	switch c := v.(type) {
	case string:
		return c
	case []any:
		var parts []string
		for _, b := range c {
			if m, ok := b.(map[string]any); ok {
				if t, ok := m["text"].(string); ok {
					parts = append(parts, t)
				}
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

func compact(v any) string {
	if m, ok := v.(map[string]any); ok {
		if c, ok := m["command"].(string); ok {
			return c
		}
	}
	if s, ok := v.(string); ok {
		return s
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func readTranscript(harness, path string) ([]turn, usage, error) {
	var turns []turn
	var u usage
	calls := map[string][2]int{}
	seen := map[string]bool{}
	add := func(role string, at time.Time, text string) {
		turns = append(turns, turn{Role: role, At: at, Text: text})
	}
	addTool := func(id string, at time.Time, c toolCall) {
		turns = append(turns, turn{Role: "assistant", At: at, Tools: []toolCall{c}})
		calls[id] = [2]int{len(turns) - 1, 0}
	}
	setOutput := func(id, out string) {
		if pos, ok := calls[id]; ok {
			turns[pos[0]].Tools[pos[1]].Output = out
		}
	}
	err := eachLine(path, func(line []byte) {
		var d map[string]any
		if json.Unmarshal(line, &d) != nil {
			return
		}
		at, _ := time.Parse(time.RFC3339Nano, fmt.Sprint(d["timestamp"]))
		switch harness {
		case "claude":
			msg, _ := d["message"].(map[string]any)
			if msg == nil || d["isMeta"] == true {
				return
			}
			switch d["type"] {
			case "user":
				if blocks, ok := msg["content"].([]any); ok {
					for _, b := range blocks {
						m, _ := b.(map[string]any)
						if m["type"] == "tool_result" {
							setOutput(fmt.Sprint(m["tool_use_id"]), textOf(m["content"]))
						}
					}
				}
				if text := textOf(msg["content"]); text != "" && !strings.HasPrefix(text, "<") {
					add("user", at, text)
				}
			case "assistant":
				if id := fmt.Sprint(msg["id"]); !seen[id] {
					seen[id] = true
					if us, ok := msg["usage"].(map[string]any); ok {
						num := func(k string) int { f, _ := us[k].(float64); return int(f) }
						u.Context = num("input_tokens") + num("cache_creation_input_tokens") + num("cache_read_input_tokens")
						u.Output += num("output_tokens")
					}
				}
				blocks, _ := msg["content"].([]any)
				for _, b := range blocks {
					m, _ := b.(map[string]any)
					switch m["type"] {
					case "text":
						add("assistant", at, fmt.Sprint(m["text"]))
					case "tool_use":
						addTool(fmt.Sprint(m["id"]), at, toolCall{Name: fmt.Sprint(m["name"]), Input: compact(m["input"])})
					}
				}
			}
		case "codex":
			p, _ := d["payload"].(map[string]any)
			if p == nil {
				return
			}
			switch p["type"] {
			case "message":
				role := fmt.Sprint(p["role"])
				text := textOf(p["content"])
				if (role == "user" || role == "assistant") && text != "" && !strings.HasPrefix(text, "<") {
					add(role, at, text)
				}
			case "custom_tool_call", "function_call":
				input := p["input"]
				if input == nil {
					input = p["arguments"]
				}
				addTool(fmt.Sprint(p["call_id"]), at, toolCall{Name: fmt.Sprint(p["name"]), Input: compact(input)})
			case "custom_tool_call_output", "function_call_output":
				setOutput(fmt.Sprint(p["call_id"]), textOf(p["output"]))
			case "token_count":
				info, _ := p["info"].(map[string]any)
				last, _ := info["last_token_usage"].(map[string]any)
				total, _ := info["total_token_usage"].(map[string]any)
				if f, ok := last["input_tokens"].(float64); ok {
					u.Context = int(f)
				}
				if f, ok := total["output_tokens"].(float64); ok {
					u.Output = int(f)
				}
			}
		}
	})
	// Claude splits one API message into a line per block, so consecutive assistant lines are merged.
	merged := turns[:0]
	for _, t := range turns {
		if n := len(merged); n > 0 && t.Role == "assistant" && merged[n-1].Role == "assistant" {
			last := &merged[n-1]
			if t.Text != "" {
				last.Text = strings.TrimSpace(last.Text + "\n\n" + t.Text)
			}
			last.Tools = append(last.Tools, t.Tools...)
			continue
		}
		merged = append(merged, t)
	}
	return merged, u, err
}

func transcriptMarkdown(turns []turn, since time.Time) string {
	var b strings.Builder
	for _, t := range turns {
		if t.At.Before(since) {
			continue
		}
		fmt.Fprintf(&b, "## %s\n\n", t.Role)
		if t.Text != "" {
			b.WriteString(t.Text + "\n\n")
		}
		for _, c := range t.Tools {
			out := c.Output
			if len(out) > 4000 {
				out = out[:4000] + "\n[truncated]"
			}
			fmt.Fprintf(&b, "```\n$ %s: %s\n%s\n```\n\n", c.Name, c.Input, strings.TrimRight(out, "\n"))
		}
	}
	return b.String()
}
