package main

import (
	"bytes"
	"context"
	"crypto/subtle"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/creack/pty"
	"github.com/fsnotify/fsnotify"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

//go:embed web
var webFS embed.FS

type web struct {
	s     *store
	token string
	pages map[string]*template.Template
	hub   hub
}

type hub struct {
	mu   sync.Mutex
	subs map[chan string]bool
}

func (h *hub) subscribe() chan string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.subs == nil {
		h.subs = map[chan string]bool{}
	}
	ch := make(chan string, 16)
	h.subs[ch] = true
	return ch
}

func (h *hub) unsubscribe(ch chan string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.subs, ch)
}

func (h *hub) publish(project string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- project:
		default:
		}
	}
}

type page struct {
	Projects    []string
	Project     string
	View        string
	Board       boardView
	Item        item
	Events      []event
	Runs        []string
	BoardRaw    string
	ProjectRaw  string
	Term        string
	Feed        feedView
	Waited      string
	Turns       []turnView
	Tokens      string
	Graph       template.HTML
	Parents     []item
	Children    []item
	Files       []fileView
	Suggestions []suggestion
	Suggesting  bool
	CanSuggest  bool
	History     []commit
}

type fileView struct {
	Name string
	HTML template.HTML
	Text string
}

type turnView struct {
	Role  string
	HTML  template.HTML
	Tools []toolCall
}

var markdown = goldmark.New(goldmark.WithExtensions(extension.GFM))

func renderTurns(turns []turn) []turnView {
	views := make([]turnView, 0, len(turns))
	for _, t := range turns {
		var b bytes.Buffer
		markdown.Convert([]byte(t.Text), &b)
		views = append(views, turnView{Role: t.Role, HTML: template.HTML(b.String()), Tools: t.Tools})
	}
	return views
}

func newWeb(s *store, token string) (*web, error) {
	wb := &web{s: s, token: token, pages: map[string]*template.Template{}}
	funcs := template.FuncMap{
		"ago": func(t time.Time) string {
			if t.IsZero() {
				return ""
			}
			return t.Local().Format("2006-01-02 15:04:05")
		},
		"add":       func(a, b int) int { return a + b },
		"firstline": firstLine,
		"deref": func(p *int) string {
			if p == nil {
				return ""
			}
			return strconv.Itoa(*p)
		},
	}
	for _, name := range []string{"board", "item", "feed", "graph", "terminal", "settings", "empty"} {
		t, err := template.New("").Funcs(funcs).ParseFS(webFS, "web/templates/layout.html", "web/templates/"+name+".html")
		if err != nil {
			return nil, err
		}
		wb.pages[name] = t
	}
	return wb, nil
}

func (wb *web) handler(api http.Handler) http.Handler {
	static, _ := fs.Sub(webFS, "web/static")
	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(static))))
	mux.HandleFunc("GET /{$}", wb.home)
	mux.HandleFunc("GET /ui/{project}", wb.page("board"))
	mux.HandleFunc("GET /ui/{project}/item/{id}", wb.page("item"))
	mux.HandleFunc("GET /ui/{project}/feed", wb.page("feed"))
	mux.HandleFunc("GET /ui/{project}/graph", wb.page("graph"))
	mux.HandleFunc("GET /ui/{project}/terminal", wb.page("terminal"))
	mux.HandleFunc("GET /ui/{project}/settings", wb.page("settings"))
	mux.HandleFunc("GET /sse", wb.sse)
	mux.HandleFunc("GET /term/item/{id}", wb.terminal)
	mux.HandleFunc("GET /term/project/{project}", wb.terminal)
	mux.Handle("/", api)
	return wb.auth(mux)
}

func (wb *web) auth(h http.Handler) http.Handler {
	valid := func(t string) bool { return subtle.ConstantTimeCompare([]byte(t), []byte(wb.token)) == 1 }
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if t := r.URL.Query().Get("token"); t != "" && valid(t) {
			http.SetCookie(w, &http.Cookie{Name: "kanban_token", Value: t, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: 400 * 24 * 3600})
			q := r.URL.Query()
			q.Del("token")
			r.URL.RawQuery = q.Encode()
			http.Redirect(w, r, r.URL.RequestURI(), http.StatusSeeOther)
			return
		}
		c, err := r.Cookie("kanban_token")
		if valid(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")) || err == nil && valid(c.Value) {
			h.ServeHTTP(w, r)
			return
		}
		reply(w, httpError{http.StatusUnauthorized, errors.New("missing or invalid token")}, nil)
	})
}

func (wb *web) home(w http.ResponseWriter, r *http.Request) {
	name, err := wb.s.resolveProject("")
	if err != nil {
		projs, _ := wb.s.projects()
		if len(projs) == 0 {
			wb.render(w, "empty", page{Projects: projs})
			return
		}
		name = projs[0]
	}
	http.Redirect(w, r, "/ui/"+name, http.StatusSeeOther)
}

func (wb *web) page(view string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name, err := wb.s.resolveProject(r.PathValue("project"))
		if err != nil {
			reply(w, err, nil)
			return
		}
		projs, err := wb.s.projects()
		if err != nil {
			reply(w, err, nil)
			return
		}
		p := page{Projects: projs, Project: name, View: view}
		switch view {
		case "board":
			if err := wb.s.setLastProject(name); err != nil {
				reply(w, err, nil)
				return
			}
			p.Board, err = boardOf(wb.s, name)
		case "item":
			var id int
			id, err = strconv.Atoi(r.PathValue("id"))
			if err != nil {
				reply(w, badRequest("item id %q is not a number", r.PathValue("id")), nil)
				return
			}
			if owner, ferr := wb.s.findItem(id); ferr != nil || owner != name {
				reply(w, fmt.Errorf("item %d in project %q: %w", id, name, errNotFound), nil)
				return
			}
			p.Board, err = boardOf(wb.s, name)
			if err == nil {
				p.Item, err = wb.s.item(name, id)
			}
			if err == nil {
				p.Events, err = wb.s.events(name, id)
				p.Waited = waited(waits(p.Events), time.Time{}, time.Now()).Round(time.Second).String()
				slices.Reverse(p.Events)
			}
			if err == nil {
				p.Runs, err = wb.s.runs(name, id)
				slices.Reverse(p.Runs)
			}
			p.Term = "/term/item/" + strconv.Itoa(id)
			if err == nil && p.Item.Transcript != "" {
				if turns, _, terr := readTranscript(p.Item.Harness, p.Item.Transcript); terr == nil {
					p.Turns = renderTurns(turns)
				}
			}
			p.Tokens = tokens(p.Item.Context, p.Item.Output)
			if err == nil {
				err = wb.itemExtras(&p, name, id)
			}
		case "feed":
			p.Feed, err = wb.s.feed(time.Now())
		case "graph":
			var g graphView
			g, err = wb.s.graph(name)
			p.Graph = template.HTML(graphSVG(g))
		case "terminal":
			p.Term = "/term/project/" + name
		case "settings":
			var b, pr []byte
			b, err = wb.s.boardRaw(name)
			if err == nil {
				pr, err = wb.s.projectRaw(name)
			}
			p.BoardRaw, p.ProjectRaw = string(b), string(pr)
			if err == nil {
				p.History, err = wb.s.history()
			}
		}
		if err != nil {
			reply(w, err, nil)
			return
		}
		wb.render(w, view, p)
	}
}

func (wb *web) itemExtras(p *page, proj string, id int) error {
	all, err := wb.s.allItems()
	if err != nil {
		return err
	}
	for _, parent := range p.Item.Parents {
		if it, ok := all[parent]; ok {
			p.Parents = append(p.Parents, it)
		}
	}
	for _, it := range all {
		if slices.Contains(it.Parents, id) {
			p.Children = append(p.Children, it)
		}
	}
	slices.SortFunc(p.Children, func(a, b item) int { return a.ID - b.ID })
	entries, _ := os.ReadDir(wb.s.itemPath(proj, id, ".files"))
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(wb.s.itemPath(proj, id, ".files"), e.Name()))
		if err != nil {
			continue
		}
		f := fileView{Name: e.Name()}
		if strings.HasSuffix(e.Name(), ".md") {
			var out bytes.Buffer
			markdown.Convert(b, &out)
			f.HTML = template.HTML(out.String())
		} else {
			f.Text = string(b[:min(len(b), 200_000)])
		}
		p.Files = append(p.Files, f)
	}
	if _, b, err := wb.s.board(proj); err == nil {
		p.CanSuggest = b.Suggest.Command != "" && b.Suggest.To != ""
	}
	p.Suggestions, _ = wb.s.suggestions(proj, id)
	wb.s.suggestMu.Lock()
	p.Suggesting = wb.s.suggesting[id]
	wb.s.suggestMu.Unlock()
	return nil
}

func (wb *web) render(w http.ResponseWriter, name string, p page) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := wb.pages[name].ExecuteTemplate(w, "layout", p); err != nil {
		log.Print("web: ", err)
	}
}

func (wb *web) sse(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	ch := wb.hub.subscribe()
	defer wb.hub.unsubscribe(ch)
	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case p := <-ch:
			fmt.Fprintf(w, "event: change-%s\ndata: %s\n\nevent: change\ndata: %s\n\n", p, p, p)
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
		case <-r.Context().Done():
			return
		}
		flusher.Flush()
	}
}

func (wb *web) terminal(w http.ResponseWriter, r *http.Request) {
	var session string
	if idStr := r.PathValue("id"); idStr != "" {
		id, err := strconv.Atoi(idStr)
		if err != nil {
			reply(w, badRequest("item id %q is not a number", idStr), nil)
			return
		}
		proj, err := wb.s.findItem(id)
		if err == nil {
			err = wb.s.ensureSession(proj, id)
		}
		if err != nil {
			reply(w, err, nil)
			return
		}
		session = sessionName(id)
	} else {
		name, err := wb.s.resolveProject(r.PathValue("project"))
		if err == nil {
			p, _, berr := wb.s.board(name)
			err = berr
			if err == nil {
				session = "project-" + name
				err = wb.s.ensureTmux(session, p.Repo)
			}
		}
		if err != nil {
			reply(w, err, nil)
			return
		}
	}

	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer c.CloseNow()
	// The library default of 32 KiB closes the terminal as soon as someone pastes a long prompt.
	c.SetReadLimit(maxBody)
	cmd := exec.Command("tmux", "-L", wb.s.tmux, "attach", "-t", "="+session)
	cmd.Env = append(slices.DeleteFunc(os.Environ(), func(e string) bool { return strings.HasPrefix(e, "TMUX=") }), "TERM=xterm-256color")
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: 80, Rows: 24})
	if err != nil {
		c.Close(websocket.StatusInternalError, err.Error())
		return
	}
	defer func() {
		f.Close()
		cmd.Process.Kill()
		cmd.Wait()
	}()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go func() {
		defer cancel()
		buf := make([]byte, 32*1024)
		for {
			n, err := f.Read(buf)
			if err != nil {
				c.Close(websocket.StatusNormalClosure, "session ended")
				return
			}
			if c.Write(ctx, websocket.MessageBinary, buf[:n]) != nil {
				return
			}
		}
	}()
	for {
		typ, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		if typ == websocket.MessageText {
			var cols, rows uint16
			if _, err := fmt.Sscanf(string(data), "resize %d %d", &cols, &rows); err == nil && cols > 0 && rows > 0 {
				pty.Setsize(f, &pty.Winsize{Cols: cols, Rows: rows})
			}
			continue
		}
		f.Write(data)
	}
}

func (s *store) watch(h *hub) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	root := filepath.Join(s.home, "projects")
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	addTree := func(dir string) {
		filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err == nil && d.IsDir() {
				if err := w.Add(path); err != nil {
					log.Print("watch: ", err)
				}
			}
			return nil
		})
	}
	addTree(root)
	go func() {
		for {
			time.Sleep(30 * time.Second)
			for p := range s.syncAll() {
				h.publish(p)
			}
		}
	}()
	go func() {
		pending := map[string]bool{}
		var flush <-chan time.Time
		for {
			select {
			case e, ok := <-w.Events:
				if !ok {
					return
				}
				base := filepath.Base(e.Name)
				if strings.HasPrefix(base, ".") || strings.HasSuffix(base, "~") || strings.HasSuffix(base, ".swp") || strings.HasSuffix(base, ".swx") {
					continue
				}
				if e.Has(fsnotify.Create) {
					// fsnotify is not recursive and files may land before the new directory is watched.
					if fi, err := os.Stat(e.Name); err == nil && fi.IsDir() {
						addTree(e.Name)
						for p := range s.syncAll() {
							pending[p] = true
						}
					}
				}
				s.syncFile(s.rel(e.Name))
				rel, err := filepath.Rel(root, e.Name)
				if err != nil || rel == "." {
					continue
				}
				pending[strings.Split(rel, string(filepath.Separator))[0]] = true
				if flush == nil {
					flush = time.After(50 * time.Millisecond)
				}
			case <-flush:
				for p := range pending {
					h.publish(p)
				}
				pending, flush = map[string]bool{}, nil
			case err, ok := <-w.Errors:
				if !ok {
					return
				}
				log.Print("watch: ", err)
			}
		}
	}()
	return nil
}
