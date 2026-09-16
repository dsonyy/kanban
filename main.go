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
	home := os.Getenv("KANBAN_HOME")
	if home == "" {
		dir, err := os.UserHomeDir()
		if err != nil {
			fail(err)
		}
		home = filepath.Join(dir, "kanban")
	}
	if len(os.Args) == 1 {
		addr := os.Getenv("KANBAN_ADDR")
		if addr == "" {
			addr = "127.0.0.1:7420"
		}
		tmuxName := os.Getenv("KANBAN_TMUX")
		if tmuxName == "" {
			tmuxName = "kanban"
		}
		if err := serve(home, addr, tmuxName); err != nil {
			fail(err)
		}
		return
	}
	q, err := parse(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "kanban:", err)
		os.Exit(2)
	}
	var body io.Reader
	if q.takesBody() {
		// Only a pipe or a file: agent shells often hand over a socket that never closes.
		if st, err := os.Stdin.Stat(); err == nil && (st.Mode()&os.ModeNamedPipe != 0 || st.Mode().IsRegular()) {
			body = os.Stdin
		}
	}
	if err := call(home, os.Args[1:], body); err != nil {
		fail(err)
	}
}

func call(home string, args []string, body io.Reader) error {
	q, err := parse(args)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(q.method(), "http://kanban"+q.path(), body)
	if err != nil {
		return err
	}
	if cwd, err := os.Getwd(); err == nil {
		req.Header.Set("Kanban-Cwd", cwd)
	}
	sock := filepath.Join(home, "kanban.sock")
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
	if _, err := io.Copy(os.Stdout, resp.Body); err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		os.Exit(1)
	}
	return nil
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "kanban:", err)
	os.Exit(1)
}
