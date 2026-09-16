package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"go.yaml.in/yaml/v3"
)

func main() {
	home := os.Getenv("KK_HOME")
	if home == "" {
		dir, err := os.UserHomeDir()
		if err != nil {
			fail(err)
		}
		home = filepath.Join(dir, "kk")
	}
	if len(os.Args) == 1 {
		addr := os.Getenv("KK_ADDR")
		if addr == "" {
			addr = "127.0.0.1:7420"
		}
		tmuxName := os.Getenv("KK_TMUX")
		if tmuxName == "" {
			tmuxName = "kk"
		}
		if err := serve(home, addr, tmuxName); err != nil {
			fail(err)
		}
		return
	}
	args := os.Args[1:]
	if args[0] == "hook" && len(args) == 2 {
		args = append(args, os.Getenv("KK_TASK"))
	}
	q, err := parse(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "kk:", err)
		// Exit code 2 from a Claude Code hook blocks the agent.
		if args[0] == "hook" {
			os.Exit(1)
		}
		os.Exit(2)
	}
	var body io.Reader
	if q.takesBody() {
		// Only a pipe or a file: agent shells often hand over a socket that never closes.
		// Harnesses deliver hook payloads over a socket they do close, so hooks read anything but a terminal.
		st, err := os.Stdin.Stat()
		pipeOrFile := err == nil && (st.Mode()&os.ModeNamedPipe != 0 || st.Mode().IsRegular())
		if pipeOrFile || q.verb == "hook" && err == nil && st.Mode()&os.ModeCharDevice == 0 {
			body = os.Stdin
		}
	}
	if err := call(home, args, body); err != nil {
		fail(err)
	}
}

func call(home string, args []string, body io.Reader) error {
	q, err := parse(args)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(q.method(), "http://kk"+q.escapedPath(), body)
	if err != nil {
		return err
	}
	if cwd, err := os.Getwd(); err == nil {
		req.Header.Set("Kk-Cwd", cwd)
	}
	sock := filepath.Join(home, "kk.sock")
	client := http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", sock)
		},
	}}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("server not running (%s)", sock)
	}
	defer resp.Body.Close()
	if verbs[q.verb].exec && resp.StatusCode < 400 {
		var v struct{ Command []string }
		if err := yaml.NewDecoder(resp.Body).Decode(&v); err != nil || len(v.Command) == 0 {
			return fmt.Errorf("bad %s response: %v", q.verb, err)
		}
		bin, err := exec.LookPath(v.Command[0])
		if err != nil {
			return err
		}
		return syscall.Exec(bin, v.Command, os.Environ())
	}
	out := io.Writer(os.Stdout)
	// Claude Code feeds hook stdout into the model context, so a successful hook stays silent.
	if q.verb == "hook" && resp.StatusCode < 400 {
		out = io.Discard
	}
	if _, err := io.Copy(out, resp.Body); err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		os.Exit(1)
	}
	return nil
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "kk:", err)
	os.Exit(1)
}
