package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.yaml.in/yaml/v3"
)

const archive = "archive"

var (
	errNotFound = errors.New("not found")
	validName   = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
)

type store struct {
	home   string
	tmux   string
	url    string
	base   string
	notify func(proj string, id int, e event)
	m      mirror
	sizes  map[string]int64
	gitMu  sync.Mutex

	suggestMu  sync.Mutex
	suggesting map[int]bool
	// ponytail: one lock for all writes, per-project locks if agents ever contend on it
	mu sync.Mutex
}

type project struct {
	Repo string `yaml:"repo"`
}

// personality is how a task's agent steps run: which harness, which model, and a prompt put in front of each step's prompt.
type personality struct {
	Name    string `yaml:"name"`
	Harness string `yaml:"harness"`
	Model   string `yaml:"model,omitempty"`
	Prompt  string `yaml:"prompt,omitempty"`
}

func (b board) personality(name string) (personality, bool) {
	for _, p := range b.Personalities {
		if p.Name == name {
			return p, true
		}
	}
	return personality{}, false
}

type column struct {
	Name    string `yaml:"name"`
	Harness string `yaml:"harness,omitempty"`
	Steps   []step `yaml:"steps"`
}

type step struct {
	Shell    string        `yaml:"shell,omitempty"`
	Agent    string        `yaml:"agent,omitempty"`
	Human    string        `yaml:"human,omitempty"`
	Goto     string        `yaml:"goto,omitempty"`
	Setup    bool          `yaml:"setup,omitempty"`
	OnFail   string        `yaml:"on_fail,omitempty"`
	MaxLoops int           `yaml:"max_loops,omitempty"`
	Timeout  time.Duration `yaml:"timeout,omitempty"`
	Idle     time.Duration `yaml:"idle,omitempty"`
	Harness  string        `yaml:"harness,omitempty"`
	Args     []string      `yaml:"args,omitempty"`
	Resume   bool          `yaml:"resume,omitempty"`
}

func (s step) kind() string {
	kinds := []string{}
	for kind, v := range map[string]string{"shell": s.Shell, "agent": s.Agent, "human": s.Human, "goto": s.Goto} {
		if v != "" {
			kinds = append(kinds, kind)
		}
	}
	if s.Setup {
		kinds = append(kinds, "setup")
	}
	if len(kinds) != 1 {
		return ""
	}
	return kinds[0]
}

type board struct {
	Setup struct {
		Generate string `yaml:"generate,omitempty"`
	} `yaml:"setup,omitempty"`
	Personalities []personality `yaml:"personalities,omitempty"`
	Suggest       struct {
		To      string `yaml:"to,omitempty"`
		Command string `yaml:"command,omitempty"`
	} `yaml:"suggest,omitempty"`
	Columns []column `yaml:"columns"`
}

type itemState struct {
	Column     string    `yaml:"column"`
	Created    time.Time `yaml:"created"`
	Step       int       `yaml:"step"`
	Status     string    `yaml:"status,omitempty"`
	Kind       string    `yaml:"kind,omitempty"`
	Pane       string    `yaml:"pane,omitempty"`
	Started    time.Time `yaml:"started,omitempty"`
	Attention  string    `yaml:"attention,omitempty"`
	Ran        string    `yaml:"ran,omitempty"`
	Harness    string    `yaml:"harness,omitempty"`
	Session    string    `yaml:"session,omitempty"`
	Transcript string    `yaml:"transcript,omitempty"`
	Context    int       `yaml:"context,omitempty"`
	Output     int       `yaml:"output,omitempty"`
	Recover    bool      `yaml:"recover,omitempty"`
	// Harness of the running step; Harness above is the harness of the last session, kept for resume.
	StepHarness string         `yaml:"step_harness,omitempty"`
	Generate    bool           `yaml:"generate,omitempty"`
	Loops       map[string]int `yaml:"loops,omitempty"`
	Parents     []int          `yaml:"parents,omitempty"`
	SessionSeen bool           `yaml:"session_seen,omitempty"`
	Personality string         `yaml:"personality,omitempty"`
	Tags        []string       `yaml:"tags,omitempty"`
}

func (st *itemState) enter(col string) {
	*st = itemState{Column: col, Created: st.Created, Status: "pending", Ran: col,
		Harness: st.Harness, Session: st.Session, Transcript: st.Transcript, Context: st.Context, Output: st.Output,
		Loops: st.Loops, Parents: st.Parents, Personality: st.Personality, Tags: st.Tags}
}

type event struct {
	At      time.Time `yaml:"at"`
	Event   string    `yaml:"event"`
	From    string    `yaml:"from,omitempty"`
	To      string    `yaml:"to,omitempty"`
	Column  string    `yaml:"column,omitempty"`
	Step    *int      `yaml:"step,omitempty"`
	Kind    string    `yaml:"kind,omitempty"`
	Exit    *int      `yaml:"exit,omitempty"`
	Took    string    `yaml:"took,omitempty"`
	Log     string    `yaml:"log,omitempty"`
	Message string    `yaml:"message,omitempty"`
}

type item struct {
	ID          int       `yaml:"id"`
	Project     string    `yaml:"project"`
	Column      string    `yaml:"column"`
	Step        int       `yaml:"step"`
	Status      string    `yaml:"status"`
	Attention   string    `yaml:"attention,omitempty"`
	Harness     string    `yaml:"harness,omitempty"`
	Session     string    `yaml:"session,omitempty"`
	Transcript  string    `yaml:"transcript,omitempty"`
	Context     int       `yaml:"context,omitempty"`
	Output      int       `yaml:"output,omitempty"`
	Live        string    `yaml:"step_harness,omitempty"`
	Parents     []int     `yaml:"parents,omitempty"`
	Personality string    `yaml:"personality,omitempty"`
	Tags        []string  `yaml:"tags,omitempty"`
	Created     time.Time `yaml:"created"`
	Content     string    `yaml:"content"`
}

type config struct {
	Agents int    `yaml:"agents"`
	Ntfy   string `yaml:"ntfy"`
	URL    string `yaml:"url"`
}

func now() time.Time { return time.Now().UTC().Truncate(time.Second) }

func (s *store) projectDir(name string) string { return filepath.Join(s.home, "projects", name) }

func (s *store) itemPath(proj string, id int, ext string) string {
	return filepath.Join(s.projectDir(proj), "items", strconv.Itoa(id)+ext)
}

func readYAML(path string, v any) error {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return errNotFound
	}
	if err != nil {
		return err
	}
	return yaml.Unmarshal(b, v)
}

func writeYAML(path string, v any) error {
	b, err := yaml.Marshal(v)
	if err != nil {
		return err
	}
	return writeFile(path, b)
}

// removeInterruptedWrites deletes temporary files of atomic writes cut off by a crash. Called with the instance flock held.
func removeInterruptedWrites(home string) {
	filepath.WalkDir(filepath.Join(home, "projects"), func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasPrefix(d.Name(), ".tmp-") {
			os.Remove(path)
		}
		return nil
	})
}

func writeFile(path string, b []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func (s *store) projects() ([]string, error) {
	names := []string{}
	for _, rel := range s.keys("projects/") {
		if strings.HasSuffix(rel, "/project.yaml") {
			names = append(names, strings.Split(rel, "/")[1])
		}
	}
	return sortedUnique(names), nil
}

const notGitRepo = "is not a git repository"

var nonSlug = regexp.MustCompile(`[^a-z0-9_-]+`)

// projectID names a project's data directory and URL. A project is identified by its repository path,
// so the id carries a hash of the path; the folder name in front only keeps the id readable.
func projectID(repo string) string {
	slug := strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(filepath.Base(repo)), "-"), "-_")
	if slug == "" {
		slug = "project"
	}
	sum := sha256.Sum256([]byte(repo))
	return slug + "-" + hex.EncodeToString(sum[:3])
}

// projectName is what people see: the repository folder name.
func (s *store) projectName(id string) string {
	if e := s.get(projectRel(id, "project.yaml")); e != nil && e.project.Repo != "" {
		return filepath.Base(e.project.Repo)
	}
	return id
}

// gitInit nil refuses a folder without git, true runs git init, false creates the project without git.
func (s *store) createProject(path string, gitInit *bool) (string, error) {
	repo, err := repoDir(path)
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(repo); err == nil {
		repo = real
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	projs, _ := s.projects()
	for _, id := range projs {
		if e := s.get(projectRel(id, "project.yaml")); e != nil && filepath.Clean(e.project.Repo) == repo {
			return "", badRequest("%s is already the project %q", repo, s.projectName(id))
		}
	}
	if gitInit == nil || *gitInit {
		if exec.Command("git", "-C", repo, "rev-parse", "--git-dir").Run() == nil {
			gitInit = ptr(false)
		} else if gitInit == nil {
			return "", httpError{http.StatusConflict, fmt.Errorf("%s %s", repo, notGitRepo)}
		}
	}
	if *gitInit {
		if out, err := exec.Command("git", "-C", repo, "init", "-q").CombinedOutput(); err != nil {
			return "", fmt.Errorf("git init in %s: %v: %s", repo, err, strings.TrimSpace(string(out)))
		}
	}
	id := projectID(repo)
	if err := os.MkdirAll(filepath.Join(s.projectDir(id), "items"), 0o755); err != nil {
		return "", err
	}
	p, err := yaml.Marshal(project{Repo: repo})
	if err != nil {
		return "", err
	}
	if err := s.write(projectRel(id, "project.yaml"), p, nil); err != nil {
		return "", err
	}
	if err := s.write(projectRel(id, "board.yaml"), []byte(defaultBoard), nil); err != nil {
		return "", err
	}
	return id, s.setLastProject(id)
}

func repoDir(path string) (string, error) {
	if path == "" {
		return "", badRequest("a project needs a repository: give its path, or run the CLI inside it")
	}
	if rest, ok := strings.CutPrefix(path, "~"); ok && (rest == "" || strings.HasPrefix(rest, "/")) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = home + rest
	}
	if !filepath.IsAbs(path) {
		return "", badRequest("repository path %q must be absolute", path)
	}
	if fi, err := os.Stat(path); err != nil || !fi.IsDir() {
		return "", badRequest("repository path %q is not a directory", path)
	}
	return filepath.Clean(path), nil
}

func (s *store) setLastProject(name string) error {
	return writeYAML(filepath.Join(s.home, "state.yaml"), map[string]string{"project": name})
}

// resolveProject accepts a project id or, when it is unambiguous, a project's display name.
func (s *store) resolveProject(name string) (string, error) {
	if name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return "", badRequest("invalid project %q", name)
	}
	if name == "" {
		var st map[string]string
		if err := readYAML(filepath.Join(s.home, "state.yaml"), &st); err != nil || st["project"] == "" {
			return "", badRequest("no project given and no last project")
		}
		name = st["project"]
	}
	if s.get(projectRel(name, "project.yaml")) != nil {
		return name, nil
	}
	projs, _ := s.projects()
	var matches []string
	for _, id := range projs {
		if s.projectName(id) == name {
			matches = append(matches, id)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return "", fmt.Errorf("project %q: %w", name, errNotFound)
	}
	return "", badRequest("%q matches several projects, use one of: %s", name, strings.Join(matches, ", "))
}

func (s *store) board(proj string) (project, board, error) {
	pe, be := s.get(projectRel(proj, "project.yaml")), s.get(projectRel(proj, "board.yaml"))
	switch {
	case pe == nil:
		return project{}, board{}, fmt.Errorf("project %q: %w", proj, errNotFound)
	case pe.err != nil:
		return pe.project, board{}, pe.err
	case be == nil:
		return pe.project, board{}, fmt.Errorf("board.yaml of %q: %w", proj, errNotFound)
	}
	return pe.project, be.board, be.err
}
func (s *store) itemIDs(proj string) ([]int, error) {
	ids := []int{}
	for _, rel := range s.keys("projects/" + proj + "/items/") {
		if name, ok := strings.CutSuffix(strings.TrimPrefix(rel, "projects/"+proj+"/items/"), ".yaml"); ok {
			if id, err := strconv.Atoi(name); err == nil {
				ids = append(ids, id)
			}
		}
	}
	slices.Sort(ids)
	return ids, nil
}
func (s *store) findItem(id int) (string, error) {
	suffix := "/items/" + strconv.Itoa(id) + ".yaml"
	for _, rel := range s.keys("projects/") {
		if strings.HasSuffix(rel, suffix) {
			return strings.Split(rel, "/")[1], nil
		}
	}
	return "", fmt.Errorf("item %d: %w", id, errNotFound)
}
func (s *store) item(proj string, id int) (item, error) {
	e := s.get(itemRel(proj, id, ".yaml"))
	if e == nil {
		return item{}, fmt.Errorf("item %d: %w", id, errNotFound)
	}
	st := e.state
	content := string(rawOf(s.get(itemRel(proj, id, ".md"))))
	return item{ID: id, Project: proj, Column: st.Column, Step: st.Step, Status: st.Status, Attention: st.Attention,
		Harness: st.Harness, Session: st.Session, Transcript: st.Transcript, Context: st.Context, Output: st.Output,
		Live: st.StepHarness, Parents: st.Parents, Personality: st.Personality, Tags: st.Tags, Created: st.Created, Content: content}, nil
}
func (s *store) createItem(proj, col, pers string, content []byte) (item, error) {
	_, b, err := s.board(proj)
	if err != nil {
		return item{}, err
	}
	if len(b.Columns) == 0 {
		return item{}, badRequest("project %q has no columns", proj)
	}
	if col == "" {
		col = b.Columns[0].Name
	}
	if !hasColumn(b, col) {
		return item{}, fmt.Errorf("column %q: %w", col, errNotFound)
	}
	if strings.TrimSpace(string(content)) == "" {
		return item{}, badRequest("item content is empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	id, err := s.nextID()
	if err != nil {
		return item{}, err
	}
	if _, ok := b.personality(pers); pers != "" && !ok {
		return item{}, fmt.Errorf("personality %q: %w", pers, errNotFound)
	}
	state, err := yaml.Marshal(itemState{Column: col, Created: now(), Status: "pending", Ran: col, Personality: pers})
	if err != nil {
		return item{}, err
	}
	if err := s.write(itemRel(proj, id, ".md"), content, nil); err != nil {
		return item{}, err
	}
	if err := s.write(itemRel(proj, id, ".yaml"), state, nil); err != nil {
		return item{}, err
	}
	if err := s.logEvent(proj, id, event{At: now(), Event: "created", To: col}); err != nil {
		return item{}, err
	}
	return s.item(proj, id)
}

func (s *store) nextID() (int, error) {
	// Any item file counts, not only state: a crash between writing N.md and N.yaml leaves content that must not be reused.
	max := 0
	for _, rel := range s.keys("projects/") {
		if _, file, ok := strings.Cut(rel, "/items/"); ok {
			name, _, _ := strings.Cut(file, ".")
			if id, err := strconv.Atoi(name); err == nil && id > max {
				max = id
			}
		}
	}
	return max + 1, nil
}

func hasColumn(b board, name string) bool {
	return slices.ContainsFunc(b.Columns, func(c column) bool { return c.Name == name })
}

func (s *store) editItem(proj string, id int, content []byte) (item, error) {
	if strings.TrimSpace(string(content)) == "" {
		return item{}, badRequest("item content is empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.write(itemRel(proj, id, ".md"), content, rawOf(s.get(itemRel(proj, id, ".md")))); err != nil {
		return item{}, err
	}
	if err := s.logEvent(proj, id, event{At: now(), Event: "edited"}); err != nil {
		return item{}, err
	}
	return s.item(proj, id)
}

func (s *store) moveItem(proj string, id int, to string) (item, error) {
	if to != archive {
		_, b, err := s.board(proj)
		if err != nil {
			return item{}, err
		}
		if !hasColumn(b, to) {
			return item{}, fmt.Errorf("column %q: %w", to, errNotFound)
		}
	}
	return s.transition(proj, id, func(st *itemState) (event, error) {
		e := event{Event: "moved", From: st.Column, To: to}
		st.enter(to)
		st.Loops = nil
		return e, nil
	})
}

func (s *store) approve(proj string, id int) (item, error) {
	return s.transition(proj, id, func(st *itemState) (event, error) {
		if st.Status != "waiting" {
			return event{}, badRequest("item %d is %s, not waiting for approval", id, st.Status)
		}
		e := event{Event: "approved", Column: st.Column, Step: ptr(st.Step)}
		st.Loops = nil
		if st.Kind == "setup" {
			st.Status, st.Attention, st.Generate = "pending", "", true
			return e, nil
		}
		st.Step, st.Status, st.Attention = st.Step+1, "pending", ""
		return e, nil
	})
}

func (s *store) retry(proj string, id int) (item, error) {
	return s.transition(proj, id, func(st *itemState) (event, error) {
		if st.Status != "failed" {
			return event{}, badRequest("item %d is %s, only failed items can be retried", id, st.Status)
		}
		e := event{Event: "retried", Column: st.Column, Step: ptr(st.Step)}
		st.Status, st.Kind, st.Pane, st.Attention, st.Loops = "pending", "", "", "", nil
		return e, nil
	})
}

func (s *store) transition(proj string, id int, fn func(*itemState) (event, error)) (item, error) {
	err := s.update(proj, id, func(st *itemState) ([]event, error) {
		e, err := fn(st)
		return []event{e}, err
	})
	if err != nil {
		return item{}, err
	}
	return s.item(proj, id)
}

func (s *store) update(proj string, id int, fn func(*itemState) ([]event, error)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rel := itemRel(proj, id, ".yaml")
	e := s.get(rel)
	if e == nil {
		return fmt.Errorf("item %d: %w", id, errNotFound)
	}
	if e.err != nil {
		return e.err
	}
	// Work on a deep copy: fn appends to slices and the mirror entry must stay what is on disk until the write succeeds.
	before, _ := yaml.Marshal(e.state)
	var st itemState
	if err := yaml.Unmarshal(before, &st); err != nil {
		return err
	}
	evs, err := fn(&st)
	if err != nil {
		return err
	}
	if after, _ := yaml.Marshal(st); !bytes.Equal(before, after) {
		if err := s.write(rel, after, e.raw); err != nil {
			return err
		}
	}
	for _, e := range evs {
		if e.At.IsZero() {
			e.At = now()
		}
		if err := s.logEvent(proj, id, e); err != nil {
			return err
		}
		if s.notify != nil && startsWait[e.Event] {
			s.notify(proj, id, e)
		}
	}
	return nil
}

func (s *store) reply(proj string, id int, text []byte) (item, error) {
	msg := strings.TrimRight(string(text), "\n")
	if msg == "" {
		return item{}, badRequest("reply is empty")
	}
	err := s.update(proj, id, func(st *itemState) ([]event, error) {
		if st.Status != "running" || st.Pane == "" {
			return nil, badRequest("item %d is %s, only a running step can take a reply", id, st.Status)
		}
		if _, err := s.tmuxCmd("send-keys", "-t", st.Pane, "-l", msg); err != nil {
			return nil, err
		}
		if _, err := s.tmuxCmd("send-keys", "-t", st.Pane, "Enter"); err != nil {
			return nil, err
		}
		return []event{{Event: "replied", Column: st.Column, Step: ptr(st.Step), Message: msg}}, nil
	})
	if err != nil {
		return item{}, err
	}
	return s.item(proj, id)
}

func ptr[T any](v T) *T { return &v }

var errPartialState = errors.New("state file is empty or incomplete, probably being written")

// An editor saving in place truncates the file first; writing state back at that moment would replace the file and lose the edit.
func (s *store) readState(proj string, id int) (itemState, error) {
	e := s.get(itemRel(proj, id, ".yaml"))
	if e == nil {
		return itemState{}, fmt.Errorf("item %d: %w", id, errNotFound)
	}
	return e.state, e.err
}
func (s *store) boardRaw(proj string) ([]byte, error) {
	e := s.get(projectRel(proj, "board.yaml"))
	if e == nil {
		return nil, fmt.Errorf("board.yaml of %q: %w", proj, errNotFound)
	}
	return e.raw, nil
}
func (s *store) saveBoard(proj string, raw []byte) error {
	var b board
	if err := yaml.Unmarshal(raw, &b); err != nil {
		return badRequest("board.yaml: %v", err)
	}
	seen := map[string]bool{}
	for _, c := range b.Columns {
		switch {
		case c.Name == "":
			return badRequest("board.yaml: column without a name")
		case c.Name == archive || c.Name == "next":
			return badRequest("board.yaml: column name %q is reserved", c.Name)
		case strings.ContainsAny(c.Name, "/\\") || strings.TrimSpace(c.Name) != c.Name:
			return badRequest("board.yaml: column name %q cannot contain slashes or surrounding spaces", c.Name)
		case seen[c.Name]:
			return badRequest("board.yaml: column %q defined twice", c.Name)
		}
		seen[c.Name] = true
		if !harnesses[c.Harness] {
			return badRequest("board.yaml: column %q has unknown harness %q", c.Name, c.Harness)
		}
		for i, sp := range c.Steps {
			if sp.kind() == "" {
				return badRequest("board.yaml: column %q step %d must have exactly one of shell, agent, human, goto, setup", c.Name, i)
			}
			if !harnesses[sp.Harness] {
				return badRequest("board.yaml: column %q step %d has unknown harness %q", c.Name, i, sp.Harness)
			}
		}
	}
	if len(b.Columns) == 0 {
		return badRequest("board.yaml: no columns")
	}
	names := map[string]bool{}
	for _, p := range b.Personalities {
		switch {
		case p.Name == "" || strings.ContainsAny(p.Name, "/\\"):
			return badRequest("board.yaml: personality %q needs a name without slashes", p.Name)
		case names[p.Name]:
			return badRequest("board.yaml: personality %q defined twice", p.Name)
		case p.Harness != "claude" && p.Harness != "codex":
			return badRequest("board.yaml: personality %q needs harness claude or codex", p.Name)
		}
		names[p.Name] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.write(projectRel(proj, "board.yaml"), raw, rawOf(s.get(projectRel(proj, "board.yaml"))))
}

func (s *store) projectRaw(proj string) ([]byte, error) {
	e := s.get(projectRel(proj, "project.yaml"))
	if e == nil {
		return nil, fmt.Errorf("project %q: %w", proj, errNotFound)
	}
	return e.raw, nil
}
func (s *store) saveProject(proj string, raw []byte) error {
	var p project
	if err := yaml.Unmarshal(raw, &p); err != nil {
		return badRequest("project.yaml: %v", err)
	}
	if p.Repo == "" {
		return badRequest("project.yaml: repo is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.write(projectRel(proj, "project.yaml"), raw, rawOf(s.get(projectRel(proj, "project.yaml"))))
}

func (s *store) runs(proj string, id int) ([]string, error) {
	entries, err := os.ReadDir(s.itemPath(proj, id, ".runs"))
	names := []string{}
	if errors.Is(err, os.ErrNotExist) {
		return names, nil
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), ".") {
			names = append(names, e.Name())
		}
	}
	return names, err
}

func (s *store) runLog(proj string, id int, name string) ([]byte, error) {
	if filepath.Base(name) != name || strings.HasPrefix(name, ".") {
		return nil, badRequest("invalid run name %q", name)
	}
	b, err := os.ReadFile(filepath.Join(s.itemPath(proj, id, ".runs"), name))
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("run %q: %w", name, errNotFound)
	}
	return b, err
}

func (s *store) config() config {
	c := config{Agents: 3}
	readYAML(filepath.Join(s.home, "config.yaml"), &c)
	return c
}

func (s *store) logEvent(proj string, id int, e event) error {
	b, err := yaml.Marshal([]event{e})
	if err != nil {
		return err
	}
	f, err := os.OpenFile(s.itemPath(proj, id, ".log.yaml"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func (s *store) events(proj string, id int) ([]event, error) {
	evs := []event{}
	err := readYAML(s.itemPath(proj, id, ".log.yaml"), &evs)
	if errors.Is(err, errNotFound) {
		return evs, nil
	}
	return evs, err
}

func (s *store) hook(name string, id int, body []byte) (map[string]string, error) {
	var payload struct {
		SessionID      string `json:"session_id"`
		TranscriptPath string `json:"transcript_path"`
		Message        string `json:"message"`
	}
	json.Unmarshal(body, &payload)
	proj, err := s.findItem(id)
	if err != nil {
		return nil, err
	}
	result := "ignored"
	err = s.update(proj, id, func(st *itemState) ([]event, error) {
		// Late hooks from a session that no longer runs this step must not touch the new state.
		if st.Status != "running" || st.StepHarness == "" || payload.SessionID != "" && st.Session != "" && payload.SessionID != st.Session && name != "session-start" {
			return nil, nil
		}
		result = "applied"
		switch name {
		case "session-start":
			if payload.SessionID == "" {
				result = "ignored"
				return nil, nil
			}
			st.Session, st.Transcript, st.SessionSeen = payload.SessionID, payload.TranscriptPath, true
			evs := []event{{Event: "session", Column: st.Column, Step: ptr(st.Step), Message: st.StepHarness + " " + payload.SessionID}}
			if st.Attention != "" {
				evs = append(evs, event{Event: "resumed", Column: st.Column, Step: ptr(st.Step), Message: st.Attention})
				st.Attention = ""
			}
			return evs, nil
		case "stop":
			if st.Transcript != "" {
				if _, u, err := readTranscript(st.StepHarness, st.Transcript); err == nil {
					st.Context, st.Output = u.Context, u.Output
				}
			}
			logPath := ""
			if p, err := s.panes(); err == nil {
				if pn, ok := p[st.Pane]; ok {
					logPath = s.saveRun(proj, id, st, pn)
				}
			}
			e := event{Event: "finished", Column: st.Column, Step: ptr(st.Step), Kind: st.Kind, Took: time.Since(st.Started).Round(time.Second).String(), Log: logPath}
			st.Step, st.Status, st.Kind, st.Pane, st.Attention, st.StepHarness = st.Step+1, "pending", "", "", "", ""
			return []event{e}, nil
		case "notification":
			msg := payload.Message
			if msg == "" {
				msg = st.StepHarness + " needs attention"
			}
			if st.Attention == msg {
				return nil, nil
			}
			st.Attention = msg
			return []event{{Event: "attention", Column: st.Column, Step: ptr(st.Step), Message: msg}}, nil
		case "prompt", "tool-done":
			if st.Attention == "" {
				return nil, nil
			}
			prev := st.Attention
			st.Attention = ""
			return []event{{Event: "resumed", Column: st.Column, Step: ptr(st.Step), Message: prev}}, nil
		}
		result = "ignored"
		return nil, nil
	})
	return map[string]string{"hook": name, "result": result}, err
}

func (s *store) tag(proj string, id int, name string, add bool) (item, error) {
	name = strings.TrimSpace(name)
	if name == "" || strings.ContainsAny(name, "/\\") {
		return item{}, badRequest("tag %q must be non-empty and without slashes", name)
	}
	return s.transition(proj, id, func(st *itemState) (event, error) {
		has := slices.Contains(st.Tags, name)
		switch {
		case add && !has:
			st.Tags = append(st.Tags, name)
		case !add && has:
			st.Tags = slices.DeleteFunc(st.Tags, func(t string) bool { return t == name })
		}
		verb := "tagged"
		if !add {
			verb = "untagged"
		}
		return event{Event: verb, Message: name}, nil
	})
}

// worktreeBranch reports the task's worktree and its checked-out branch by reading git's files, without running git.
func (s *store) worktreeBranch(id int) (worktree, branch string) {
	dir := s.worktree(id)
	dotgit, err := os.ReadFile(filepath.Join(dir, ".git"))
	if err != nil {
		return "", ""
	}
	gitdir, ok := strings.CutPrefix(strings.TrimSpace(string(dotgit)), "gitdir: ")
	if !ok {
		return dir, ""
	}
	head, _ := os.ReadFile(filepath.Join(gitdir, "HEAD"))
	branch, ok = strings.CutPrefix(strings.TrimSpace(string(head)), "ref: refs/heads/")
	if !ok {
		branch = "detached"
	}
	return dir, branch
}

// setPersonality changes the personality of a task's next agent steps; an empty name returns to the column default.
func (s *store) setPersonality(proj string, id int, name string) (item, error) {
	if name != "" {
		_, b, err := s.board(proj)
		if err != nil {
			return item{}, err
		}
		if _, ok := b.personality(name); !ok {
			return item{}, fmt.Errorf("personality %q: %w", name, errNotFound)
		}
	}
	return s.transition(proj, id, func(st *itemState) (event, error) {
		st.Personality = name
		if name == "" {
			return event{Event: "personality", Message: "column default"}, nil
		}
		return event{Event: "personality", Message: name}, nil
	})
}
