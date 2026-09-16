package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"go.yaml.in/yaml/v3"
)

type boardView struct {
	Project string       `yaml:"project"`
	Repo    string       `yaml:"repo"`
	Columns []columnView `yaml:"columns"`
}

type columnView struct {
	Name    string     `yaml:"name"`
	Missing bool       `yaml:"missing,omitempty"`
	Items   []cardView `yaml:"items"`
}

type cardView struct {
	ID        int    `yaml:"id"`
	Line      string `yaml:"line"`
	Status    string `yaml:"status"`
	Attention string `yaml:"attention,omitempty"`
	Live      bool   `yaml:"live,omitempty"`
	Tokens    string `yaml:"tokens,omitempty"`
}

func tokens(context, output int) string {
	if context == 0 && output == 0 {
		return ""
	}
	short := func(n int) string {
		switch {
		case n >= 1_000_000:
			return fmt.Sprintf("%.1fM", float64(n)/1e6)
		case n >= 1000:
			return fmt.Sprintf("%.1fk", float64(n)/1e3)
		}
		return strconv.Itoa(n)
	}
	return "ctx " + short(context) + " · out " + short(output)
}

const maxBody = 64 << 20

type raw []byte

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
	removeInterruptedWrites(home)
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
	s := &store{home: home, tmux: tmuxName, base: "http://" + addr, sizes: map[string]int64{}, suggesting: map[int]bool{}, m: mirror{files: map[string]*entry{}}}
	if err := s.writeHookSettings(); err != nil {
		return err
	}
	if err := s.initHistory(); err != nil {
		return err
	}
	go s.commitLoop()
	s.url = s.base + "/?token=" + token
	s.notify = s.push
	wb, err := newWeb(s, token)
	if err != nil {
		return err
	}
	s.syncAll()
	if err := s.watch(&wb.hub); err != nil {
		return err
	}
	go s.run()
	h := handler(s)
	errc := make(chan error, 2)
	go func() { errc <- http.Serve(unixLn, h) }()
	go func() { errc <- http.Serve(tcpLn, wb.handler(h)) }()
	fmt.Printf("kanban server pid %d, socket %s\nweb: %s\n", os.Getpid(), sock, s.url)
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
	// Read one byte past the limit so an oversized body is rejected instead of silently cut.
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxBody {
		return nil, httpError{http.StatusRequestEntityTooLarge, fmt.Errorf("request body too large, the limit is %d MiB", maxBody>>20)}
	}
	switch {
	case q.verb == "hook" && len(q.ids) == 0 && len(q.args) == 2:
		id, err := strconv.Atoi(q.args[1])
		if err != nil {
			return nil, badRequest("hook item id %q is not a number", q.args[1])
		}
		return s.hook(q.args[0], id, body)
	case q.has("history") && q.ids["history"] == "" && q.verb == "":
		return s.history()
	case q.has("history") && q.ids["history"] != "" && q.verb == "undo" && len(q.args) == 0:
		return s.undo(q.ids["history"])
	case q.has("feed") && q.verb == "":
		return s.feed(time.Now())
	case q.has("server"):
		return map[string]any{"pid": os.Getpid(), "url": s.url}, nil
	case q.has("item"):
		return dispatchItem(s, q, body)
	case q.has("board"):
		name, err := s.resolveProject(q.ids["project"])
		if err != nil {
			return nil, err
		}
		switch {
		case q.verb == "" && q.ids["board"] == "":
			b, err := s.boardRaw(name)
			return raw(b), err
		case q.verb == "edit" && q.ids["board"] == "":
			if err := s.saveBoard(name, body); err != nil {
				return nil, err
			}
			b, err := s.boardRaw(name)
			return raw(b), err
		}
	case q.ids["project"] != "" && q.verb == "graph" && len(q.args) == 0:
		name, err := s.resolveProject(q.ids["project"])
		if err != nil {
			return nil, err
		}
		return s.graph(name)
	case q.ids["project"] != "" && q.verb == "edit":
		name, err := s.resolveProject(q.ids["project"])
		if err != nil {
			return nil, err
		}
		if err := s.saveProject(name, body); err != nil {
			return nil, err
		}
		b, err := s.projectRaw(name)
		return raw(b), err
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
	case q.verb == "runs" && len(q.args) == 0:
		return s.runs(proj, id)
	case q.verb == "runs" && len(q.args) == 1:
		b, err := s.runLog(proj, id, q.args[0])
		return raw(b), err
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
	case q.verb == "reply" && len(q.args) == 0:
		return s.reply(proj, id, body)
	case q.verb == "link" && len(q.args) == 1:
		return s.link(proj, id, q.args[0], true)
	case q.verb == "unlink" && len(q.args) == 1:
		return s.link(proj, id, q.args[0], false)
	case q.verb == "suggest" && len(q.args) == 0:
		return s.suggest(proj, id)
	case q.verb == "suggestions" && len(q.args) == 0:
		return s.suggestions(proj, id)
	case q.verb == "accept" && len(q.args) == 1:
		return s.accept(proj, id, q.args[0])
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
		byColumn[it.Column] = append(byColumn[it.Column], cardView{ID: id, Line: line, Status: it.Status, Attention: it.Attention,
			Live: it.Status == "running" && it.Live != "", Tokens: tokens(it.Context, it.Output)})
	}
	for _, c := range b.Columns {
		items := byColumn[c.Name]
		if items == nil {
			items = []cardView{}
		}
		v.Columns = append(v.Columns, columnView{Name: c.Name, Items: items})
		delete(byColumn, c.Name)
	}
	// Tasks whose column was removed or renamed in board.yaml would otherwise disappear from every view.
	delete(byColumn, archive)
	var missing []string
	for name := range byColumn {
		missing = append(missing, name)
	}
	slices.Sort(missing)
	for _, name := range missing {
		v.Columns = append(v.Columns, columnView{Name: name, Missing: true, Items: byColumn[name]})
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
	if b, ok := v.(raw); ok {
		w.Write(b)
		return
	}
	yaml.NewEncoder(w).Encode(v)
}
