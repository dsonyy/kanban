package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	tick        = 500 * time.Millisecond
	defaultIdle = 10 * time.Minute
	stepWindow  = "step"
)

type pane struct {
	id, session, window string
	dead                bool
	exit                int
	activity            time.Time
}

type ref struct {
	proj string
	id   int
	st   itemState
}

func (s *store) run() {
	for {
		if err := s.reconcile(); err != nil {
			log.Print("runner: ", err)
		}
		time.Sleep(tick)
	}
}

func (s *store) tmuxCmd(args ...string) (string, error) {
	out, err := exec.Command("tmux", append([]string{"-L", s.tmux}, args...)...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("tmux %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimRight(string(out), "\n"), nil
}

func (s *store) panes() (map[string]pane, error) {
	out, err := s.tmuxCmd("list-panes", "-a", "-F", "#{pane_id}\t#{session_name}\t#{window_name}\t#{pane_dead}\t#{pane_dead_status}\t#{window_activity}")
	if err != nil {
		if strings.Contains(err.Error(), "no server running") || strings.Contains(err.Error(), "error connecting") {
			return map[string]pane{}, nil
		}
		return nil, err
	}
	panes := map[string]pane{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(line, "\t")
		if len(f) != 6 {
			continue
		}
		exit, _ := strconv.Atoi(f[4])
		act, _ := strconv.ParseInt(f[5], 10, 64)
		panes[f[0]] = pane{id: f[0], session: f[1], window: f[2], dead: f[3] == "1", exit: exit, activity: time.Unix(act, 0)}
	}
	return panes, nil
}

func sessionName(id int) string { return "kanban-" + strconv.Itoa(id) }

func (s *store) worktree(id int) string {
	return filepath.Join(s.home, "worktrees", strconv.Itoa(id))
}

func (s *store) workdir(proj string, id int) string {
	if fi, err := os.Stat(s.worktree(id)); err == nil && fi.IsDir() {
		return s.worktree(id)
	}
	p, _, _ := s.board(proj)
	return p.Repo
}

func (s *store) ensureSession(proj string, id int) error {
	if _, err := s.tmuxCmd("has-session", "-t", "="+sessionName(id)); err == nil {
		return nil
	}
	_, err := s.tmuxCmd("new-session", "-d", "-s", sessionName(id), "-x", "200", "-y", "50", "-c", s.workdir(proj, id),
		";", "set", "-g", "remain-on-exit", "on",
		";", "set", "-g", "mouse", "on",
		";", "set", "-g", "focus-events", "on",
		";", "set", "-g", "history-limit", "50000")
	return err
}

func (s *store) reconcile() error {
	panes, err := s.panes()
	if err != nil {
		return err
	}
	projs, err := s.projects()
	if err != nil {
		return err
	}
	var refs []ref
	agents := 0
	for _, proj := range projs {
		ids, err := s.itemIDs(proj)
		if err != nil {
			return err
		}
		for _, id := range ids {
			var st itemState
			if err := readYAML(s.itemPath(proj, id, ".yaml"), &st); err != nil {
				log.Printf("runner: item %d: %v", id, err)
				continue
			}
			if st.Status == "running" && st.Kind == "agent" {
				agents++
			}
			refs = append(refs, ref{proj, id, st})
		}
	}
	slices.SortStableFunc(refs, func(a, b ref) int {
		if (a.st.Status == "queued") != (b.st.Status == "queued") {
			if a.st.Status == "queued" {
				return 1
			}
			return -1
		}
		if a.st.Status == "queued" {
			return a.st.Started.Compare(b.st.Started)
		}
		return 0
	})

	limit := s.config().Agents
	keepPanes, keepSessions := map[string]bool{}, map[string]bool{}
	for _, r := range refs {
		err := s.update(r.proj, r.id, func(st *itemState) ([]event, error) {
			return s.advance(r.proj, r.id, st, panes, &agents, limit), nil
		})
		if err != nil {
			log.Printf("runner: item %d: %v", r.id, err)
		}
		st := r.st
		if readYAML(s.itemPath(r.proj, r.id, ".yaml"), &st) != nil || st.Column != archive {
			keepPanes[st.Pane] = true
			keepSessions[sessionName(r.id)] = true
		}
	}

	killedSessions := map[string]bool{}
	for _, p := range panes {
		if !strings.HasPrefix(p.session, "kanban-") {
			continue
		}
		if !keepSessions[p.session] && !killedSessions[p.session] {
			killedSessions[p.session] = true
			s.tmuxCmd("kill-session", "-t", "="+p.session)
			continue
		}
		if p.window == stepWindow && !keepPanes[p.id] {
			s.tmuxCmd("kill-pane", "-t", p.id)
		}
	}
	return nil
}

func (s *store) advance(proj string, id int, st *itemState, panes map[string]pane, agents *int, limit int) []event {
	if st.Column == archive {
		return nil
	}
	fail := func(msg string, e event) []event {
		if st.Status == "running" && st.Kind == "agent" {
			*agents--
		}
		st.Status, st.Pane, st.Attention = "failed", "", msg
		e.Event, e.Column, e.Step, e.Message = "failed", st.Column, ptr(st.Step), msg
		return []event{e}
	}
	var (
		b      board
		i      = -1
		steps  []step
		broken string
	)
	if _, bd, err := s.board(proj); err != nil {
		broken = "board.yaml: " + err.Error()
	} else if i = slices.IndexFunc(bd.Columns, func(c column) bool { return c.Name == st.Column }); i < 0 {
		broken = fmt.Sprintf("column %q is not in board.yaml", st.Column)
	} else {
		b, steps = bd, bd.Columns[i].Steps
	}
	// A running step outlives a broken board.yaml, which is often just a half-saved edit.
	if broken != "" && (st.Status == "" || st.Status == "pending" || st.Status == "queued") {
		if st.Attention == broken {
			return nil
		}
		st.Attention = broken
		return []event{{Event: "attention", Column: st.Column, Message: broken}}
	}
	if broken == "" && (strings.HasPrefix(st.Attention, "board.yaml: ") || strings.HasSuffix(st.Attention, "is not in board.yaml")) {
		st.Attention = ""
	}

	switch st.Status {
	case "", "pending":
		if st.Step >= len(steps) {
			st.Status, st.Kind = "done", ""
			return []event{{Event: "done", Column: st.Column}}
		}
		sp := steps[st.Step]
		switch sp.kind() {
		case "human":
			st.Status, st.Kind, st.Attention = "waiting", "human", sp.Human
			return []event{{Event: "attention", Column: st.Column, Step: ptr(st.Step), Message: sp.Human}}
		case "goto":
			to := sp.Goto
			if to == "next" {
				if i+1 >= len(b.Columns) {
					return fail("goto next from the last column", event{})
				}
				to = b.Columns[i+1].Name
			}
			if !hasColumn(b, to) {
				return fail(fmt.Sprintf("goto %q: no such column", to), event{})
			}
			e := event{Event: "moved", From: st.Column, To: to, Message: "goto"}
			*st = itemState{Column: to, Created: st.Created, Status: "pending"}
			return []event{e}
		case "shell", "agent":
			if sp.kind() == "agent" && *agents >= limit {
				st.Status, st.Kind, st.Started = "queued", "agent", now()
				return []event{{Event: "queued", Column: st.Column, Step: ptr(st.Step)}}
			}
			return s.start(proj, id, st, sp, agents)
		}
		return fail(fmt.Sprintf("step %d must have exactly one of shell, agent, human, goto", st.Step), event{})

	case "queued":
		if *agents >= limit {
			return nil
		}
		if st.Step >= len(steps) || steps[st.Step].kind() != "agent" {
			st.Status = "pending"
			return nil
		}
		return s.start(proj, id, st, steps[st.Step], agents)

	case "running":
		p, ok := panes[st.Pane]
		if !ok {
			return fail("step session was lost", event{Kind: st.Kind})
		}
		var sp step
		if st.Step < len(steps) {
			sp = steps[st.Step]
		}
		took := time.Since(st.Started).Round(time.Second)
		if p.dead {
			logPath := s.saveRun(proj, id, st, p)
			e := event{Kind: st.Kind, Exit: ptr(p.exit), Took: took.String(), Log: logPath}
			if p.exit != 0 {
				return fail(fmt.Sprintf("%s step %d exited with %d", st.Kind, st.Step, p.exit), e)
			}
			if st.Kind == "agent" {
				*agents--
			}
			e.Event, e.Column, e.Step = "finished", st.Column, ptr(st.Step)
			st.Step, st.Status, st.Kind, st.Pane, st.Attention = st.Step+1, "pending", "", "", ""
			return []event{e}
		}
		if sp.Timeout > 0 && took > sp.Timeout {
			logPath := s.saveRun(proj, id, st, p)
			return fail(fmt.Sprintf("%s step %d timed out after %s", st.Kind, st.Step, sp.Timeout), event{Kind: st.Kind, Took: took.String(), Log: logPath})
		}
		idle := sp.Idle
		if idle == 0 && st.Kind == "agent" {
			idle = defaultIdle
		}
		quiet := time.Since(p.activity).Round(time.Second)
		if idle > 0 && quiet > idle && st.Attention == "" {
			st.Attention = fmt.Sprintf("no output for %s", quiet)
			return []event{{Event: "attention", Column: st.Column, Step: ptr(st.Step), Message: st.Attention}}
		}
		if idle > 0 && quiet <= idle && st.Attention != "" {
			st.Attention = ""
		}
	}
	return nil
}

func (s *store) start(proj string, id int, st *itemState, sp step, agents *int) []event {
	cmd := sp.Shell
	if sp.kind() == "agent" {
		cmd = sp.Agent
	}
	p, _, _ := s.board(proj)
	err := s.ensureSession(proj, id)
	var paneID string
	if err == nil {
		paneID, err = s.tmuxCmd("new-window", "-d", "-t", "="+sessionName(id), "-n", stepWindow, "-c", s.workdir(proj, id), "-P", "-F", "#{pane_id}",
			"-e", "KANBAN_TASK="+strconv.Itoa(id),
			"-e", "KANBAN_TASK_FILE="+s.itemPath(proj, id, ".md"),
			"-e", "KANBAN_PROJECT="+proj,
			"-e", "KANBAN_REPO="+p.Repo,
			"-e", "KANBAN_WORKTREE="+s.worktree(id),
			"-e", "KANBAN_PORT="+strconv.Itoa(20000+id),
			cmd)
	}
	if err != nil {
		st.Status, st.Attention = "failed", "cannot start step: "+err.Error()
		return []event{{Event: "failed", Column: st.Column, Step: ptr(st.Step), Message: st.Attention}}
	}
	if sp.kind() == "agent" {
		*agents++
	}
	st.Status, st.Kind, st.Pane, st.Started, st.Attention = "running", sp.kind(), paneID, now(), ""
	return []event{{Event: "started", Column: st.Column, Step: ptr(st.Step), Kind: st.Kind}}
}

func (s *store) saveRun(proj string, id int, st *itemState, p pane) string {
	out, err := s.tmuxCmd("capture-pane", "-p", "-J", "-S", "-", "-E", "-", "-t", p.id)
	if err != nil {
		out = err.Error()
	}
	lines := strings.Split(out, "\n")
	for len(lines) > 0 && (strings.TrimSpace(lines[len(lines)-1]) == "" || strings.HasPrefix(lines[len(lines)-1], "Pane is dead")) {
		lines = lines[:len(lines)-1]
	}
	name := fmt.Sprintf("%d.runs/%s-%s-%d.log", id, time.Now().UTC().Format("20060102T150405"), st.Column, st.Step)
	path := filepath.Join(s.projectDir(proj), "items", name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		log.Print("runner: ", err)
		return ""
	}
	if err := writeFile(path, []byte(strings.Join(lines, "\n")+"\n")); err != nil {
		log.Print("runner: ", err)
		return ""
	}
	return name
}
