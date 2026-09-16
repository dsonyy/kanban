package main

import (
	"fmt"
	"slices"
	"strings"
)

var resources = []string{"server", "project", "board", "item"}

var verbs = map[string]struct{ post, exec bool }{
	"new":     {post: true},
	"edit":    {post: true},
	"move":    {post: true},
	"archive": {post: true},
	"approve": {post: true},
	"retry":   {post: true},
	"log":     {},
	"runs":    {},
	"attach":  {exec: true},
}

type query struct {
	ids  map[string]string
	verb string
	args []string
}

func (q query) has(r string) bool {
	_, ok := q.ids[r]
	return ok
}

func parse(tokens []string) (query, error) {
	q := query{ids: map[string]string{}}
	for i := 0; i < len(tokens); i++ {
		t := tokens[i]
		if _, ok := verbs[t]; ok {
			q.verb, q.args = t, tokens[i+1:]
			break
		}
		if !slices.Contains(resources, t) {
			return q, fmt.Errorf("unexpected %q", t)
		}
		if q.has(t) {
			return q, fmt.Errorf("%s given twice", t)
		}
		q.ids[t] = ""
		if i+1 < len(tokens) && !reserved(tokens[i+1]) {
			i++
			q.ids[t] = tokens[i]
		}
	}
	if len(q.ids) == 0 {
		return q, fmt.Errorf("nothing to do")
	}
	return q, nil
}

func reserved(s string) bool {
	_, verb := verbs[s]
	return verb || slices.Contains(resources, s)
}

func (q query) path() string {
	var p []string
	for _, r := range resources {
		if id, ok := q.ids[r]; ok {
			p = append(p, r)
			if id != "" {
				p = append(p, id)
			}
		}
	}
	if q.verb != "" {
		p = append(append(p, q.verb), q.args...)
	}
	return "/" + strings.Join(p, "/")
}

func (q query) takesBody() bool {
	return q.verb == "edit" || q.verb == "new" && q.has("item")
}

func (q query) method() string {
	if verbs[q.verb].post {
		return "POST"
	}
	return "GET"
}
