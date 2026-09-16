package main

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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

	assets "kk/web"
)

type web struct {
	s     *store
	token string
	pages map[string]*template.Template
	files fs.FS
	dev   bool
	funcs template.FuncMap
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

// Tags lists every tag used on the board, for the tag input suggestions.
func (b boardView) Tags() []string {
	var tags []string
	for _, c := range b.Columns {
		for _, it := range c.Items {
			for _, t := range it.Tags {
				if !slices.Contains(tags, t) {
					tags = append(tags, t)
				}
			}
		}
	}
	slices.Sort(tags)
	return tags
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
	Title       template.HTML
	TitleText   string
	Description template.HTML
	Content     template.HTML
	Branch      string
	Worktree    string
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

type dirEntry struct {
	Name, Path, Modified string
	Git                  bool
}

type dirListing struct {
	Path, Parent string
	Git          bool
	Dirs         []dirEntry
}

func isGitRepo(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

// dirs lists the folders of one directory on the server for the repository picker, hidden ones included.
func (wb *web) dirs(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if rest, ok := strings.CutPrefix(path, "~"); path == "" || ok && (rest == "" || strings.HasPrefix(rest, "/")) {
		home, _ := os.UserHomeDir()
		path = home + rest
	}
	path = filepath.Clean(path)
	entries, err := os.ReadDir(path)
	if err != nil || !filepath.IsAbs(path) {
		reply(w, badRequest("cannot open folder %q", path), nil)
		return
	}
	v := dirListing{Path: path, Git: isGitRepo(path), Dirs: []dirEntry{}}
	if parent := filepath.Dir(path); parent != path {
		v.Parent = parent
	}
	for _, e := range entries {
		full := filepath.Join(path, e.Name())
		if fi, err := os.Stat(full); err == nil && fi.IsDir() {
			v.Dirs = append(v.Dirs, dirEntry{Name: e.Name(), Path: full, Git: isGitRepo(full), Modified: fi.ModTime().Format("2006-01-02 15:04")})
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	t, err := wb.tmpl("dirs")
	if err == nil {
		err = t.ExecuteTemplate(w, "dirs", v)
	}
	if err != nil {
		log.Print("web: ", err)
	}
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
	wb := &web{s: s, token: token, files: assets.FS}
	// KK_DEV=1 reads the web client from ./web on every request, so front-end edits show up on reload.
	if os.Getenv("KK_DEV") == "1" {
		dir, err := filepath.Abs("web")
		if err != nil {
			return nil, err
		}
		if _, err := os.Stat(filepath.Join(dir, "templates")); err != nil {
			return nil, fmt.Errorf("KK_DEV=1 needs the web client sources in %s: run the server from the repository root", dir)
		}
		wb.files, wb.dev = os.DirFS(dir), true
		if err := wb.watchWeb(filepath.Join(dir, "templates")); err != nil {
			return nil, err
		}
		log.Printf("dev mode: serving the web client from %s, reloading pages on changes", dir)
	}
	wb.funcs = template.FuncMap{
		"ago": func(t time.Time) string {
			if t.IsZero() {
				return ""
			}
			return t.Local().Format("2006-01-02 15:04:05")
		},
		"add":       func(a, b int) int { return a + b },
		"dev":       func() bool { return wb.dev },
		"assets":    wb.assetTags,
		"pname":     s.projectName,
		"firstline": firstLine,
		"join":      strings.Join,
		"deref": func(p *int) string {
			if p == nil {
				return ""
			}
			return strconv.Itoa(*p)
		},
	}
	wb.pages = map[string]*template.Template{}
	for _, name := range append(pageNames, "dirs", "panel") {
		t, err := wb.parse(name)
		if err != nil {
			return nil, err
		}
		wb.pages[name] = t
	}
	return wb, nil
}

var pageNames = []string{"board", "item", "feed", "graph", "stats", "terminal", "settings", "new"}

func (wb *web) parse(name string) (*template.Template, error) {
	if name == "dirs" || name == "panel" {
		return template.New("").Funcs(wb.funcs).ParseFS(wb.files, "templates/"+name+".html")
	}
	return template.New("").Funcs(wb.funcs).ParseFS(wb.files, "templates/layout.html", "templates/"+name+".html")
}

// tmpl returns a parsed template, parsed again on every call in dev mode.
func (wb *web) tmpl(name string) (*template.Template, error) {
	if wb.dev {
		return wb.parse(name)
	}
	return wb.pages[name], nil
}

func (wb *web) handler(api http.Handler) http.Handler {
	// Production serves the Vite build; dev serves only public/ (icons), because Vite serves scripts and styles itself.
	static, _ := fs.Sub(wb.files, "dist")
	if wb.dev {
		static, _ = fs.Sub(wb.files, "public")
	}
	mux := http.NewServeMux()
	files := http.StripPrefix("/static/", http.FileServer(http.FS(static)))
	mux.HandleFunc("GET /static/", func(w http.ResponseWriter, r *http.Request) {
		if wb.dev {
			w.Header().Set("Cache-Control", "no-store")
		}
		files.ServeHTTP(w, r)
	})
	mux.HandleFunc("GET /{$}", wb.home)
	mux.HandleFunc("GET /ui/dirs", wb.dirs)
	mux.HandleFunc("GET /ui/new", func(w http.ResponseWriter, r *http.Request) {
		projs, _ := wb.s.projects()
		wb.render(w, "new", page{Projects: projs, View: "new"})
	})
	mux.HandleFunc("GET /ui/{project}", wb.page("board"))
	mux.HandleFunc("GET /ui/{project}/item/{id}", wb.page("item"))
	mux.HandleFunc("GET /ui/{project}/item/{id}/panel", wb.page("panel"))
	mux.HandleFunc("GET /ui/{project}/feed", wb.page("feed"))
	mux.HandleFunc("GET /ui/{project}/graph", wb.page("graph"))
	mux.HandleFunc("GET /ui/{project}/stats", wb.page("stats"))
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
			http.SetCookie(w, &http.Cookie{Name: "kk_token", Value: t, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: 400 * 24 * 3600})
			q := r.URL.Query()
			q.Del("token")
			r.URL.RawQuery = q.Encode()
			http.Redirect(w, r, r.URL.RequestURI(), http.StatusSeeOther)
			return
		}
		if valid(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")) {
			h.ServeHTTP(w, r)
			return
		}
		if c, err := r.Cookie("kk_token"); err == nil && valid(c.Value) {
			// SameSite ignores ports, so a page on another port of this host still gets the cookie attached.
			// Browsers send Origin on every POST; a write authorized by the cookie must come from this server's own pages.
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				// Behind tailscale serve the Host may be the local address; a cross-site page cannot set
				// X-Forwarded-Host without a CORS preflight, which this server never grants.
				u, err := url.Parse(r.Header.Get("Origin"))
				if err != nil || u.Host == "" || u.Host != r.Host && u.Host != r.Header.Get("X-Forwarded-Host") {
					reply(w, httpError{http.StatusForbidden, errors.New("cross-origin request refused")}, nil)
					return
				}
			}
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
			wb.render(w, "new", page{Projects: projs, View: "new"})
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
		case "item", "panel":
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
			p.Title, p.TitleText, p.Description = taskText(p.Item.Content)
			var full bytes.Buffer
			markdown.Convert([]byte(p.Item.Content), &full)
			p.Content = template.HTML(full.String())
			p.Branch, p.Worktree = wb.s.worktreeBranch(id)
			p.Worktree = tildePath(p.Worktree)
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
		if view == "panel" {
			wb.renderFragment(w, "panel", "task-panel", p)
			return
		}
		wb.render(w, view, p)
	}
}

func (wb *web) renderFragment(w http.ResponseWriter, file, name string, data any) {
	var buf bytes.Buffer
	t, err := wb.tmpl(file)
	if err == nil {
		err = t.ExecuteTemplate(&buf, name, data)
	}
	if err != nil {
		log.Print("web: ", err)
		http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	buf.WriteTo(w)
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
	// Render into memory first: a template error half way through would otherwise ship a silently truncated page.
	var buf bytes.Buffer
	t, err := wb.tmpl(name)
	if err == nil {
		err = t.ExecuteTemplate(&buf, "layout", p)
	}
	if err != nil {
		log.Print("web: ", err)
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "<!doctype html><pre>template error:\n%s</pre>", template.HTMLEscapeString(err.Error()))
		return
	}
	buf.WriteTo(w)
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
			if p == reloadEvent {
				fmt.Fprint(w, "event: reload\ndata: web\n\n")
				break
			}
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

// reloadEvent travels through the project change hub; project ids never contain a NUL byte.
const reloadEvent = "\x00reload"

// watchWeb tells open pages to reload when anything under the dev web directory changes.
func (wb *web) watchWeb(dir string) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			w.Add(path)
		}
		return nil
	})
	go func() {
		var fire <-chan time.Time
		for {
			select {
			case e := <-w.Events:
				if fi, err := os.Stat(e.Name); err == nil && fi.IsDir() && e.Has(fsnotify.Create) {
					w.Add(e.Name)
				}
				if fire == nil {
					fire = time.After(100 * time.Millisecond)
				}
			case <-fire:
				fire = nil
				wb.hub.publish(reloadEvent)
			case err := <-w.Errors:
				log.Print("web watch: ", err)
			}
		}
	}()
	return nil
}

const viteDevServer = "http://127.0.0.1:5173"

// assetTags links the client's scripts and styles: from the Vite dev server in dev mode, from the build manifest otherwise.
func (wb *web) assetTags() template.HTML {
	if wb.dev {
		return template.HTML(`<script type="module" src="` + viteDevServer + `/static/@vite/client"></script>` +
			`<script type="module" src="` + viteDevServer + `/static/src/main.ts"></script>`)
	}
	raw, err := fs.ReadFile(wb.files, "dist/.vite/manifest.json")
	var manifest map[string]struct {
		File string   `json:"file"`
		CSS  []string `json:"css"`
	}
	if err == nil {
		err = json.Unmarshal(raw, &manifest)
	}
	entry, ok := manifest["src/main.ts"]
	if err != nil || !ok {
		log.Print("web: no Vite build found, run just build: ", err)
		return ""
	}
	var b strings.Builder
	for _, css := range entry.CSS {
		fmt.Fprintf(&b, `<link rel="stylesheet" href="/static/%s">`, css)
	}
	fmt.Fprintf(&b, `<script type="module" src="/static/%s"></script>`, entry.File)
	return template.HTML(b.String())
}

var htmlTag = regexp.MustCompile(`<[^>]*>`)

// taskText splits a task into its title (the first line, inline markdown, heading marks dropped) and a markdown description.
func taskText(content string) (title template.HTML, plain string, description template.HTML) {
	first, rest, _ := strings.Cut(strings.TrimSpace(content), "\n")
	var b bytes.Buffer
	markdown.Convert([]byte(strings.TrimLeft(first, "# ")), &b)
	inline := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(b.String()), "<p>"), "</p>")
	b.Reset()
	markdown.Convert([]byte(strings.TrimSpace(rest)), &b)
	return template.HTML(inline), html.UnescapeString(htmlTag.ReplaceAllString(inline, "")), template.HTML(b.String())
}
