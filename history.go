package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const gitignore = "kanban.sock\nkanban.lock\ntoken\nhooks/\nworktrees/\n.tmp-*\n"

var commitHash = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

type commit struct {
	Hash    string    `yaml:"hash"`
	At      time.Time `yaml:"at"`
	Message string    `yaml:"message"`
	Files   []string  `yaml:"files"`
}

func (s *store) git(args ...string) (string, error) {
	base := []string{"-C", s.home, "-c", "user.name=kanban", "-c", "user.email=kanban@localhost", "-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null"}
	out, err := exec.Command("git", append(base, args...)...).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func (s *store) initHistory() error {
	if _, err := os.Stat(filepath.Join(s.home, ".git")); errors.Is(err, os.ErrNotExist) {
		if _, err := s.git("init", "-q"); err != nil {
			return err
		}
	}
	path := filepath.Join(s.home, ".gitignore")
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return os.WriteFile(path, []byte(gitignore), 0o644)
	}
	return nil
}

func (s *store) commitLoop() {
	for {
		time.Sleep(2 * time.Second)
		if err := s.commitChanges(); err != nil {
			log.Print("history: ", err)
		}
	}
}

func (s *store) commitChanges() error {
	s.gitMu.Lock()
	defer s.gitMu.Unlock()
	status, err := s.git("status", "--porcelain")
	if err != nil || strings.TrimSpace(status) == "" {
		return err
	}
	var paths []string
	for _, line := range strings.Split(strings.TrimRight(status, "\n"), "\n") {
		if len(line) > 3 {
			paths = append(paths, strings.Trim(line[3:], `"`))
		}
	}
	msg := strings.Join(paths[:min(len(paths), 3)], ", ")
	if len(paths) > 3 {
		msg += fmt.Sprintf(" and %d more", len(paths)-3)
	}
	if _, err := s.git("add", "-A"); err != nil {
		return err
	}
	_, err = s.git("commit", "-q", "-m", msg)
	return err
}

func (s *store) history() ([]commit, error) {
	s.gitMu.Lock()
	defer s.gitMu.Unlock()
	out, err := s.git("log", "-50", "--format=%x00%H%x09%cI%x09%s", "--name-only")
	if err != nil {
		if strings.Contains(out, "does not have any commits") {
			return []commit{}, nil
		}
		return nil, err
	}
	commits := []commit{}
	for _, block := range strings.Split(out, "\x00")[1:] {
		lines := strings.Split(strings.TrimSpace(block), "\n")
		head := strings.SplitN(lines[0], "\t", 3)
		if len(head) != 3 {
			continue
		}
		at, _ := time.Parse(time.RFC3339, head[1])
		c := commit{Hash: head[0], At: at, Message: head[2], Files: []string{}}
		for _, f := range lines[1:] {
			if f = strings.TrimSpace(f); f != "" {
				c.Files = append(c.Files, f)
			}
		}
		commits = append(commits, c)
	}
	return commits, nil
}

func (s *store) undo(hash string) (map[string]string, error) {
	if !commitHash.MatchString(hash) {
		return nil, badRequest("invalid commit %q", hash)
	}
	if err := s.commitChanges(); err != nil {
		return nil, err
	}
	s.gitMu.Lock()
	defer s.gitMu.Unlock()
	if _, err := s.git("rev-parse", "--verify", "-q", hash+"^"); err != nil {
		return nil, badRequest("cannot undo %s: it is the first commit", hash)
	}
	out, err := s.git("diff-tree", "--no-commit-id", "--name-only", "-r", hash)
	if err != nil {
		return nil, badRequest("cannot undo %s: %v", hash, err)
	}
	// Event logs and run records are append-only history, so undo never touches them.
	// Other files go back to their version before the commit; later edits to them are overwritten.
	var restored []string
	s.mu.Lock()
	for _, path := range strings.Split(strings.TrimSpace(out), "\n") {
		if path == "" || strings.HasSuffix(path, ".log.yaml") || strings.Contains(path, ".runs/") {
			continue
		}
		full := filepath.Join(s.home, path)
		if old, err := exec.Command("git", "-C", s.home, "show", hash+"^:"+path).Output(); err == nil {
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err == nil {
				err = writeFile(full, old)
			}
		} else {
			os.Remove(full)
		}
		restored = append(restored, path)
	}
	s.mu.Unlock()
	if len(restored) == 0 {
		return nil, badRequest("commit %s only changed event logs, nothing to undo", hash)
	}
	if _, err := s.git("add", "-A"); err != nil {
		return nil, err
	}
	if _, err := s.git("commit", "-q", "--allow-empty", "-m", "Undo "+hash[:min(len(hash), 12)]); err != nil {
		return nil, err
	}
	head, err := s.git("rev-parse", "HEAD")
	return map[string]string{"undone": hash, "commit": strings.TrimSpace(head)}, err
}
