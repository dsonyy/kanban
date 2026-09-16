package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
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
		if err := serve(home, addr); err != nil {
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
	if verbs[q.verb].body {
		if st, err := os.Stdin.Stat(); err == nil && st.Mode()&os.ModeCharDevice == 0 {
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
