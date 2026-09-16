package main

import (
	"errors"
	"fmt"
	"os"
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
	home string
	// ponytail: one lock for all writes, per-project locks if agents ever contend on it
	mu sync.Mutex
}

type project struct {
	Repo string `yaml:"repo"`
}

type column struct {
	Name  string `yaml:"name"`
	Steps []any  `yaml:"steps"`
}

type board struct {
	Columns []column `yaml:"columns"`
}

type itemState struct {
	Column  string    `yaml:"column"`
	Created time.Time `yaml:"created"`
}

type event struct {
	At    time.Time `yaml:"at"`
	Event string    `yaml:"event"`
	From  string    `yaml:"from,omitempty"`
	To    string    `yaml:"to,omitempty"`
}

type item struct {
	ID      int       `yaml:"id"`
	Project string    `yaml:"project"`
	Column  string    `yaml:"column"`
	Created time.Time `yaml:"created"`
	Content string    `yaml:"content"`
}

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
	entries, err := os.ReadDir(filepath.Join(s.home, "projects"))
	if errors.Is(err, os.ErrNotExist) {
		return []string{}, nil
	}
	names := []string{}
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names, err
}

func (s *store) createProject(name, repo string) error {
	if !validName.MatchString(name) || reserved(name) {
		return badRequest("invalid project name %q", name)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := s.projectDir(name)
	if _, err := os.Stat(dir); err == nil {
		return badRequest("project %q already exists", name)
	}
	if err := os.MkdirAll(filepath.Join(dir, "items"), 0o755); err != nil {
		return err
	}
	b := board{}
	for _, c := range []string{"backlog", "todo", "doing", "done"} {
		b.Columns = append(b.Columns, column{Name: c, Steps: []any{}})
	}
	if err := writeYAML(filepath.Join(dir, "project.yaml"), project{Repo: repo}); err != nil {
		return err
	}
	if err := writeYAML(filepath.Join(dir, "board.yaml"), b); err != nil {
		return err
	}
	return s.setLastProject(name)
}

func (s *store) setLastProject(name string) error {
	return writeYAML(filepath.Join(s.home, "state.yaml"), map[string]string{"project": name})
}

func (s *store) resolveProject(name string) (string, error) {
	if name == "" {
		var st map[string]string
		if err := readYAML(filepath.Join(s.home, "state.yaml"), &st); err != nil || st["project"] == "" {
			return "", badRequest("no project given and no last project")
		}
		name = st["project"]
	}
	if _, err := os.Stat(s.projectDir(name)); err != nil {
		return "", fmt.Errorf("project %q: %w", name, errNotFound)
	}
	return name, nil
}

func (s *store) board(proj string) (project, board, error) {
	var p project
	var b board
	if err := readYAML(filepath.Join(s.projectDir(proj), "project.yaml"), &p); err != nil {
		return p, b, err
	}
	return p, b, readYAML(filepath.Join(s.projectDir(proj), "board.yaml"), &b)
}

func (s *store) itemIDs(proj string) ([]int, error) {
	paths, err := filepath.Glob(filepath.Join(s.projectDir(proj), "items", "*.yaml"))
	ids := []int{}
	for _, p := range paths {
		if id, err := strconv.Atoi(strings.TrimSuffix(filepath.Base(p), ".yaml")); err == nil {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return ids, err
}

func (s *store) findItem(id int) (string, error) {
	paths, err := filepath.Glob(filepath.Join(s.home, "projects", "*", "items", strconv.Itoa(id)+".yaml"))
	if err != nil {
		return "", err
	}
	if len(paths) == 0 {
		return "", fmt.Errorf("item %d: %w", id, errNotFound)
	}
	return filepath.Base(filepath.Dir(filepath.Dir(paths[0]))), nil
}

func (s *store) item(proj string, id int) (item, error) {
	var st itemState
	if err := readYAML(s.itemPath(proj, id, ".yaml"), &st); err != nil {
		return item{}, err
	}
	content, err := os.ReadFile(s.itemPath(proj, id, ".md"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return item{}, err
	}
	return item{ID: id, Project: proj, Column: st.Column, Created: st.Created, Content: string(content)}, nil
}

func (s *store) createItem(proj, col string, content []byte) (item, error) {
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
	now := time.Now().UTC().Truncate(time.Second)
	if err := writeFile(s.itemPath(proj, id, ".md"), content); err != nil {
		return item{}, err
	}
	if err := writeYAML(s.itemPath(proj, id, ".yaml"), itemState{Column: col, Created: now}); err != nil {
		return item{}, err
	}
	if err := s.logEvent(proj, id, event{At: now, Event: "created", To: col}); err != nil {
		return item{}, err
	}
	return s.item(proj, id)
}

func (s *store) nextID() (int, error) {
	projs, err := s.projects()
	if err != nil {
		return 0, err
	}
	max := 0
	for _, p := range projs {
		ids, err := s.itemIDs(p)
		if err != nil {
			return 0, err
		}
		if len(ids) > 0 && ids[len(ids)-1] > max {
			max = ids[len(ids)-1]
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
	if err := writeFile(s.itemPath(proj, id, ".md"), content); err != nil {
		return item{}, err
	}
	if err := s.logEvent(proj, id, event{At: time.Now().UTC().Truncate(time.Second), Event: "edited"}); err != nil {
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
	s.mu.Lock()
	defer s.mu.Unlock()
	var st itemState
	if err := readYAML(s.itemPath(proj, id, ".yaml"), &st); err != nil {
		return item{}, err
	}
	from := st.Column
	st.Column = to
	if err := writeYAML(s.itemPath(proj, id, ".yaml"), st); err != nil {
		return item{}, err
	}
	if err := s.logEvent(proj, id, event{At: time.Now().UTC().Truncate(time.Second), Event: "moved", From: from, To: to}); err != nil {
		return item{}, err
	}
	return s.item(proj, id)
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
