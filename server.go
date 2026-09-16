package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"go.yaml.in/yaml/v3"
)

type boardView struct {
	Project string       `yaml:"project"`
	Repo    string       `yaml:"repo"`
	Columns []columnView `yaml:"columns"`
}

type columnView struct {
	Name  string     `yaml:"name"`
	Items []cardView `yaml:"items"`
}

type cardView struct {
	ID        int    `yaml:"id"`
	Line      string `yaml:"line"`
	Status    string `yaml:"status"`
	Attention string `yaml:"attention,omitempty"`
}

type httpError struct {
	code int
	err  error
}

func (e httpError) Error() string { return e.err.Error() }

func badRequest(format string, a ...any) error {
	return httpError{http.StatusBadRequest, fmt.Errorf(format, a...)}
}

func serve(home, addr, tmuxName string) error {
	if err := os.MkdirAll(home, 0o700); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(home, "kanban.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	if syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		fmt.Print("kanban server already running\n")
		return call(home, []string{"server"}, nil)
	}
	token, err := loadToken(home)
	if err != nil {
		return err
	}
	sock := filepath.Join(home, "kanban.sock")
	os.Remove(sock)
	unixLn, err := net.Listen("unix", sock)
	if err != nil {
		return err
	}
	if err := os.Chmod(sock, 0o600); err != nil {
		return err
	}
	tcpLn, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	s := &store{home: home, tmux: tmuxName}
	go s.run()
	h := handler(s)
	errc := make(chan error, 2)
	go func() { errc <- http.Serve(unixLn, h) }()
	go func() { errc <- http.Serve(tcpLn, withToken(token, h)) }()
	fmt.Printf("kanban server pid %d, socket %s, http %s\n", os.Getpid(), sock, addr)
	return <-errc
}

func loadToken(home string) (string, error) {
	path := filepath.Join(home, "token")
	b, err := os.ReadFile(path)
	if err == nil {
		return strings.TrimSpace(string(b)), nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	raw := make([]byte, 32)
	rand.Read(raw)
	token := hex.EncodeToString(raw)
	return token, os.WriteFile(path, []byte(token+"\n"), 0o600)
}

func withToken(token string, h http.Handler) http.Handler {
	want := []byte("Bearer " + token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), want) != 1 {
			reply(w, httpError{http.StatusUnauthorized, errors.New("missing or invalid token")}, nil)
			return
		}
		h.ServeHTTP(w, r)
	})
}

func handler(s *store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q, err := parse(strings.Split(strings.Trim(r.URL.Path, "/"), "/"))
		if err != nil {
			reply(w, httpError{http.StatusNotFound, err}, nil)
			return
		}
		if r.Method != q.method() {
			reply(w, httpError{http.StatusMethodNotAllowed, fmt.Errorf("%s needs %s", q.path(), q.method())}, nil)
			return
		}
		v, err := dispatch(s, q, r)
		reply(w, err, v)
	})
}

func dispatch(s *store, q query, r *http.Request) (any, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	switch {
	case q.has("server"):
		return map[string]int{"pid": os.Getpid()}, nil
	case q.has("item"):
		return dispatchItem(s, q, body)
	case q.ids["project"] == "" && q.verb == "":
		return s.projects()
	case q.ids["project"] == "" && q.verb == "new" && len(q.args) == 1:
		repo := r.Header.Get("Kanban-Cwd")
		if repo == "" {
			return nil, badRequest("project new needs the repo directory in the Kanban-Cwd header")
		}
		if err := s.createProject(q.args[0], repo); err != nil {
			return nil, err
		}
		return boardOf(s, q.args[0])
	case q.ids["project"] != "" && q.verb == "":
		name, err := s.resolveProject(q.ids["project"])
		if err != nil {
			return nil, err
		}
		if err := s.setLastProject(name); err != nil {
			return nil, err
		}
		return boardOf(s, name)
	}
	return nil, badRequest("unsupported: %s %s", q.method(), q.path())
}

func dispatchItem(s *store, q query, body []byte) (any, error) {
	if q.ids["item"] == "" {
		if q.verb != "new" || len(q.args) > 1 {
			return nil, badRequest("item needs an id")
		}
		proj, err := s.resolveProject(q.ids["project"])
		if err != nil {
			return nil, err
		}
		col := ""
		if len(q.args) == 1 {
			col = q.args[0]
		}
		return s.createItem(proj, col, body)
	}
	id, err := strconv.Atoi(q.ids["item"])
	if err != nil {
		return nil, badRequest("item id %q is not a number", q.ids["item"])
	}
	proj, err := s.findItem(id)
	if err != nil {
		return nil, err
	}
	if p := q.ids["project"]; p != "" && p != proj {
		return nil, fmt.Errorf("item %d in project %q: %w", id, p, errNotFound)
	}
	switch {
	case q.verb == "" && len(q.args) == 0:
		return s.item(proj, id)
	case q.verb == "log" && len(q.args) == 0:
		return s.events(proj, id)
	case q.verb == "edit" && len(q.args) == 0:
		return s.editItem(proj, id, body)
	case q.verb == "move" && len(q.args) == 1:
		return s.moveItem(proj, id, q.args[0])
	case q.verb == "archive" && len(q.args) == 0:
		return s.moveItem(proj, id, archive)
	case q.verb == "approve" && len(q.args) == 0:
		return s.approve(proj, id)
	case q.verb == "retry" && len(q.args) == 0:
		return s.retry(proj, id)
	case q.verb == "attach" && len(q.args) == 0:
		if err := s.ensureSession(proj, id); err != nil {
			return nil, err
		}
		return map[string][]string{"command": {"tmux", "-L", s.tmux, "attach", "-t", "=" + sessionName(id)}}, nil
	}
	return nil, badRequest("unsupported: %s %s", q.method(), q.path())
}

func boardOf(s *store, proj string) (boardView, error) {
	p, b, err := s.board(proj)
	if err != nil {
		return boardView{}, err
	}
	ids, err := s.itemIDs(proj)
	if err != nil {
		return boardView{}, err
	}
	v := boardView{Project: proj, Repo: p.Repo}
	byColumn := map[string][]cardView{}
	for _, id := range ids {
		it, err := s.item(proj, id)
		if err != nil {
			return boardView{}, err
		}
		line, _, _ := strings.Cut(strings.TrimSpace(it.Content), "\n")
		byColumn[it.Column] = append(byColumn[it.Column], cardView{ID: id, Line: line, Status: it.Status, Attention: it.Attention})
	}
	for _, c := range b.Columns {
		items := byColumn[c.Name]
		if items == nil {
			items = []cardView{}
		}
		v.Columns = append(v.Columns, columnView{Name: c.Name, Items: items})
	}
	return v, nil
}

func reply(w http.ResponseWriter, err error, v any) {
	w.Header().Set("Content-Type", "application/yaml")
	if err != nil {
		code := http.StatusInternalServerError
		var he httpError
		switch {
		case errors.As(err, &he):
			code = he.code
		case errors.Is(err, errNotFound):
			code = http.StatusNotFound
		}
		v = map[string]string{"error": err.Error()}
		w.WriteHeader(code)
	}
	yaml.NewEncoder(w).Encode(v)
}
