package main

import (
	"cmp"
	"context"
	"fmt"
	"html"
	"log"
	"maps"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

type node struct {
	ID       int    `yaml:"id"`
	Project  string `yaml:"project"`
	Name     string `yaml:"name"`
	Line     string `yaml:"line"`
	Column   string `yaml:"column"`
	Status   string `yaml:"status"`
	External bool   `yaml:"external,omitempty"`
	Depth    int    `yaml:"depth"`
}

type edge struct {
	Parent int `yaml:"parent"`
	Child  int `yaml:"child"`
}

type graphView struct {
	Nodes []node `yaml:"nodes"`
	Edges []edge `yaml:"edges"`
}

type suggestion struct {
	Content string `yaml:"content"`
}

func (s *store) link(proj string, id int, parentArg string, add bool) (item, error) {
	parent, err := strconv.Atoi(parentArg)
	if err != nil {
		return item{}, badRequest("parent id %q is not a number", parentArg)
	}
	if parent == id {
		return item{}, badRequest("item %d cannot be its own parent", id)
	}
	if _, err := s.findItem(parent); err != nil {
		return item{}, err
	}
	return s.transition(proj, id, func(st *itemState) (event, error) {
		has := slices.Contains(st.Parents, parent)
		switch {
		case add && !has:
			st.Parents = append(st.Parents, parent)
			slices.Sort(st.Parents)
		case !add && has:
			st.Parents = slices.DeleteFunc(st.Parents, func(p int) bool { return p == parent })
		}
		name := "linked"
		if !add {
			name = "unlinked"
		}
		return event{Event: name, Message: "parent #" + parentArg}, nil
	})
}

func (s *store) allItems() (map[int]item, error) {
	all := map[int]item{}
	projs, err := s.projects()
	if err != nil {
		return nil, err
	}
	for _, proj := range projs {
		ids, err := s.itemIDs(proj)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			it, err := s.item(proj, id)
			if err != nil {
				return nil, err
			}
			all[id] = it
		}
	}
	return all, nil
}

func (s *store) graph(proj string) (graphView, error) {
	all, err := s.allItems()
	if err != nil {
		return graphView{}, err
	}
	v := graphView{Nodes: []node{}, Edges: []edge{}}
	in := map[int]bool{}
	for id, it := range all {
		if it.Project == proj && it.Column != archive {
			in[id] = true
		}
	}
	for id := range in {
		for _, p := range all[id].Parents {
			if _, ok := all[p]; ok {
				v.Edges = append(v.Edges, edge{Parent: p, Child: id})
			}
		}
	}
	shown := maps.Clone(in)
	for _, e := range v.Edges {
		shown[e.Parent] = true
	}
	depth := map[int]int{}
	var walk func(id int, seen map[int]bool) int
	walk = func(id int, seen map[int]bool) int {
		if d, ok := depth[id]; ok {
			return d
		}
		if seen[id] || !in[id] {
			return 0
		}
		seen[id] = true
		d := 0
		for _, p := range all[id].Parents {
			if shown[p] {
				d = max(d, walk(p, seen)+1)
			}
		}
		delete(seen, id)
		depth[id] = d
		return d
	}
	for id := range shown {
		it := all[id]
		v.Nodes = append(v.Nodes, node{ID: id, Project: it.Project, Name: s.projectName(it.Project), Line: firstLine(it.Content), Column: it.Column, Status: it.Status, External: !in[id], Depth: walk(id, map[int]bool{})})
	}
	slices.SortFunc(v.Nodes, func(a, b node) int { return (a.Depth-b.Depth)*1_000_000 + a.ID - b.ID })
	slices.SortFunc(v.Edges, func(a, b edge) int { return (a.Child-b.Child)*1_000_000 + a.Parent - b.Parent })
	return v, nil
}

func graphSVG(g graphView) string {
	const w, h, gapX, gapY, pad = 240, 46, 70, 14, 10
	parents, linked := map[int][]int{}, map[int]bool{}
	for _, e := range g.Edges {
		parents[e.Child] = append(parents[e.Child], e.Parent)
		linked[e.Parent], linked[e.Child] = true, true
	}
	layers := map[int][]node{}
	for _, n := range g.Nodes {
		layers[n.Depth] = append(layers[n.Depth], n)
	}
	// Each layer follows the average row of its parents, which keeps edges from crossing in simple trees.
	row := map[int]float64{}
	pos := map[int][2]int{}
	width, height := 0, 0
	for d := 0; d < len(layers); d++ {
		key := func(n node) float64 {
			if !linked[n.ID] {
				return 1e9 + float64(n.ID)
			}
			if len(parents[n.ID]) == 0 {
				return float64(n.ID)
			}
			sum := 0.0
			for _, p := range parents[n.ID] {
				sum += row[p]
			}
			return sum/float64(len(parents[n.ID]))*1e6 + float64(n.ID)
		}
		slices.SortStableFunc(layers[d], func(a, b node) int { return cmp.Compare(key(a), key(b)) })
		for r, n := range layers[d] {
			row[n.ID] = float64(r)
			x, y := pad+d*(w+gapX), pad+r*(h+gapY)
			pos[n.ID] = [2]int{x, y}
			width, height = max(width, x+w+pad), max(height, y+h+pad)
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<svg class="graph" width="%d" height="%d" viewBox="0 0 %d %d" role="img" aria-label="Task graph">`, width, height, width, height)
	for _, e := range g.Edges {
		p, c := pos[e.Parent], pos[e.Child]
		fmt.Fprintf(&b, `<line class="edge" x1="%d" y1="%d" x2="%d" y2="%d"/>`, p[0]+w, p[1]+h/2, c[0], c[1]+h/2)
	}
	for _, n := range g.Nodes {
		p := pos[n.ID]
		line := n.Line
		if r := []rune(line); len(r) > 24 {
			line = string(r[:23]) + "…"
		}
		class := "node status-" + n.Status
		if n.External {
			class += " external"
		}
		fmt.Fprintf(&b, `<a href="/ui/%s/item/%d"><g class="%s"><rect x="%d" y="%d" width="%d" height="%d"/>`, html.EscapeString(n.Project), n.ID, class, p[0], p[1], w, h)
		fmt.Fprintf(&b, `<text x="%d" y="%d" class="title">#%d %s</text>`, p[0]+10, p[1]+19, n.ID, html.EscapeString(line))
		meta := n.Column + " · " + n.Status
		if n.External {
			meta = n.Name + " · " + meta
		}
		fmt.Fprintf(&b, `<text x="%d" y="%d" class="meta">%s</text></g></a>`, p[0]+10, p[1]+36, html.EscapeString(meta))
	}
	b.WriteString(`</svg>`)
	return b.String()
}

func (s *store) family(id int) ([]item, error) {
	all, err := s.allItems()
	if err != nil {
		return nil, err
	}
	children := map[int][]int{}
	for cid, it := range all {
		for _, p := range it.Parents {
			children[p] = append(children[p], cid)
		}
	}
	seen := map[int]bool{}
	var up, down func(int)
	up = func(i int) {
		if seen[i] {
			return
		}
		seen[i] = true
		for _, p := range all[i].Parents {
			up(p)
		}
	}
	down = func(i int) {
		for _, c := range children[i] {
			if !seen[c] {
				seen[c] = true
				down(c)
			}
		}
	}
	up(id)
	down(id)
	var fam []item
	for i := range seen {
		if it, ok := all[i]; ok {
			fam = append(fam, it)
		}
	}
	slices.SortFunc(fam, func(a, b item) int { return a.ID - b.ID })
	return fam, nil
}

func (s *store) suggest(proj string, id int) (map[string]string, error) {
	_, b, err := s.board(proj)
	if err != nil {
		return nil, err
	}
	if b.Suggest.Command == "" || b.Suggest.To == "" {
		return nil, badRequest("board.yaml needs suggest.command and suggest.to")
	}
	s.suggestMu.Lock()
	if s.suggesting[id] {
		s.suggestMu.Unlock()
		return nil, badRequest("item %d is already getting suggestions", id)
	}
	s.suggesting[id] = true
	s.suggestMu.Unlock()

	fam, err := s.family(id)
	if err != nil {
		return nil, err
	}
	var md strings.Builder
	md.WriteString("# Task family\n\n")
	for _, it := range fam {
		fmt.Fprintf(&md, "## #%d (%s, %s, %s)\n\n%s\n\n", it.ID, it.Project, it.Column, it.Status, strings.TrimSpace(it.Content))
	}
	familyFile, out := s.itemPath(proj, id, ".family.md"), s.itemPath(proj, id, ".suggestions.yaml")
	if err := writeFile(familyFile, []byte(md.String())); err != nil {
		return nil, err
	}
	s.update(proj, id, func(*itemState) ([]event, error) {
		return []event{{Event: "suggesting", Message: fmt.Sprintf("%d tasks in the family", len(fam))}}, nil
	})
	p, _, _ := s.board(proj)
	go func() {
		defer func() {
			s.suggestMu.Lock()
			delete(s.suggesting, id)
			s.suggestMu.Unlock()
		}()
		// ponytail: runs outside tmux and the agent limit; move it into a step if suggestions get long or interactive.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, "sh", "-c", b.Suggest.Command)
		cmd.Dir = p.Repo
		cmd.Env = append(os.Environ(), "KK_HOME="+s.home, "KK_TASK="+strconv.Itoa(id), "KK_PROJECT="+proj, "KK_REPO="+p.Repo,
			"KK_FAMILY_FILE="+familyFile, "KK_SUGGESTIONS="+out)
		output, runErr := cmd.CombinedOutput()
		e := event{Event: "suggested"}
		list, err := s.suggestions(proj, id)
		switch {
		case runErr != nil:
			e = event{Event: "suggest-failed", Message: strings.TrimSpace(runErr.Error() + " " + string(output))}
		case err != nil:
			e = event{Event: "suggest-failed", Message: err.Error()}
		default:
			e.Message = fmt.Sprintf("%d suggestions", len(list))
		}
		if err := s.update(proj, id, func(*itemState) ([]event, error) { return []event{e}, nil }); err != nil {
			log.Print("suggest: ", err)
		}
	}()
	return map[string]string{"suggesting": strconv.Itoa(id)}, nil
}

func (s *store) suggestions(proj string, id int) ([]suggestion, error) {
	b, err := os.ReadFile(s.itemPath(proj, id, ".suggestions.yaml"))
	if os.IsNotExist(err) {
		return []suggestion{}, nil
	}
	if err != nil {
		return nil, err
	}
	text := strings.TrimSpace(string(b))
	if lines := strings.Split(text, "\n"); len(lines) > 1 && strings.HasPrefix(lines[0], "```") {
		text = strings.Join(lines[1:len(lines)-1], "\n")
	}
	list := []suggestion{}
	if err := yaml.Unmarshal([]byte(text), &list); err != nil {
		return nil, fmt.Errorf("suggestions are not a YAML list of content: %w", err)
	}
	return slices.DeleteFunc(list, func(sg suggestion) bool { return strings.TrimSpace(sg.Content) == "" }), nil
}

func (s *store) accept(proj string, id int, nArg string) (item, error) {
	n, err := strconv.Atoi(nArg)
	if err != nil {
		return item{}, badRequest("suggestion number %q is not a number", nArg)
	}
	_, b, err := s.board(proj)
	if err != nil {
		return item{}, err
	}
	list, err := s.suggestions(proj, id)
	if err != nil {
		return item{}, err
	}
	if n < 1 || n > len(list) {
		return item{}, badRequest("item %d has %d suggestions, no number %d", id, len(list), n)
	}
	child, err := s.createItem(proj, b.Suggest.To, "", []byte(list[n-1].Content))
	if err != nil {
		return item{}, err
	}
	if _, err := s.link(proj, child.ID, strconv.Itoa(id), true); err != nil {
		return item{}, err
	}
	rest, _ := yaml.Marshal(slices.Delete(list, n-1, n))
	if err := writeFile(s.itemPath(proj, id, ".suggestions.yaml"), rest); err != nil {
		return item{}, err
	}
	s.update(proj, id, func(*itemState) ([]event, error) {
		return []event{{Event: "accepted", Message: fmt.Sprintf("suggestion %d as #%d", n, child.ID)}}, nil
	})
	return s.item(proj, child.ID)
}
