package main

import (
	"bytes"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	"go.yaml.in/yaml/v3"
)

var errConflict = httpError{http.StatusConflict, errors.New("the file changed on disk while this change was being made; the version on disk was kept, try again")}

// entry is the last known content of one state file on disk, parsed.
type entry struct {
	raw     []byte
	err     error
	project project
	board   board
	state   itemState
}

type mirror struct {
	mu    sync.RWMutex
	files map[string]*entry
}

// Mirrored paths, relative to KK_HOME: projects/P/project.yaml, projects/P/board.yaml, projects/P/items/N.yaml, projects/P/items/N.md.
func mirrored(rel string) bool {
	parts := strings.Split(rel, "/")
	if len(parts) == 3 && parts[0] == "projects" {
		return parts[2] == "project.yaml" || parts[2] == "board.yaml"
	}
	if len(parts) == 4 && parts[0] == "projects" && parts[2] == "items" {
		name, ext, _ := strings.Cut(parts[3], ".")
		_, err := strconv.Atoi(name)
		return err == nil && (ext == "yaml" || ext == "md")
	}
	return false
}

func parseEntry(rel string, raw []byte, prev *entry) *entry {
	e := &entry{raw: raw}
	switch {
	case strings.HasSuffix(rel, "/project.yaml"):
		e.err = yaml.Unmarshal(raw, &e.project)
	case strings.HasSuffix(rel, "/board.yaml"):
		e.err = yaml.Unmarshal(raw, &e.board)
	case strings.HasSuffix(rel, ".yaml"):
		e.err = yaml.Unmarshal(raw, &e.state)
		if e.err == nil && e.state.Column == "" {
			e.err = errPartialState
		}
		// A half-saved file must not blank out what readers see; they get the last good state and the error.
		if e.err != nil && prev != nil {
			e.state = prev.state
		}
	}
	return e
}

func (s *store) abs(rel string) string { return filepath.Join(s.home, filepath.FromSlash(rel)) }

func (s *store) rel(path string) string {
	r, err := filepath.Rel(s.home, path)
	if err != nil {
		return ""
	}
	return filepath.ToSlash(r)
}

func (s *store) get(rel string) *entry {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	return s.m.files[rel]
}

func (s *store) keys(prefix string) []string {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	var keys []string
	for k := range s.m.files {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	return keys
}

// syncFile loads one file from disk into the mirror and reports whether the mirror changed.
func (s *store) syncFile(rel string) bool {
	if !mirrored(rel) {
		return false
	}
	raw, err := os.ReadFile(s.abs(rel))
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	prev, known := s.m.files[rel]
	if err != nil {
		if known && errors.Is(err, fs.ErrNotExist) {
			delete(s.m.files, rel)
			return true
		}
		return false
	}
	if known && bytes.Equal(prev.raw, raw) {
		return false
	}
	s.m.files[rel] = parseEntry(rel, raw, prev)
	return true
}

// syncAll rescans every state file, which catches anything the watcher missed, and returns the projects that changed.
func (s *store) syncAll() map[string]bool {
	changed := map[string]bool{}
	seen := map[string]bool{}
	filepath.WalkDir(filepath.Join(s.home, "projects"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel := s.rel(path)
		if !mirrored(rel) {
			return nil
		}
		seen[rel] = true
		if s.syncFile(rel) {
			changed[strings.Split(rel, "/")[1]] = true
		}
		return nil
	})
	for _, rel := range s.keys("projects/") {
		if !seen[rel] && s.syncFile(rel) {
			changed[strings.Split(rel, "/")[1]] = true
		}
	}
	return changed
}

// write is the only way state files change. The caller holds s.mu and passes the content its change was computed from;
// if the disk no longer matches it, an external edit happened in between, the disk wins and the change is dropped.
func (s *store) write(rel string, raw, expected []byte) error {
	disk, err := os.ReadFile(s.abs(rel))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	// ponytail: an edit landing between this read and the rename below still loses; closing that needs file locks editors ignore.
	if !bytes.Equal(disk, expected) {
		s.syncFile(rel)
		return errConflict
	}
	if err := os.MkdirAll(filepath.Dir(s.abs(rel)), 0o755); err != nil {
		return err
	}
	if err := writeFile(s.abs(rel), raw); err != nil {
		return err
	}
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	s.m.files[rel] = parseEntry(rel, raw, s.m.files[rel])
	return nil
}

func rawOf(e *entry) []byte {
	if e == nil {
		return nil
	}
	return e.raw
}

func projectRel(proj, file string) string { return "projects/" + proj + "/" + file }

func itemRel(proj string, id int, ext string) string {
	return "projects/" + proj + "/items/" + strconv.Itoa(id) + ext
}

func sortedUnique(names []string) []string {
	slices.Sort(names)
	return slices.Compact(names)
}
