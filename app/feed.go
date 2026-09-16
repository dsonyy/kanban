package main

import (
	"bytes"
	"log"
	"mime"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

var (
	startsWait = map[string]bool{"attention": true, "failed": true}
	endsWait   = map[string]bool{"approved": true, "retried": true, "moved": true, "resumed": true, "finished": true, "done": true}
)

type openEntry struct {
	ID        int       `yaml:"id"`
	Project   string    `yaml:"project"`
	Line      string    `yaml:"line"`
	Column    string    `yaml:"column"`
	Status    string    `yaml:"status"`
	Attention string    `yaml:"attention"`
	Since     time.Time `yaml:"since"`
	Waiting   string    `yaml:"waiting"`
}

type recentEntry struct {
	At      time.Time `yaml:"at"`
	ID      int       `yaml:"id"`
	Project string    `yaml:"project"`
	Line    string    `yaml:"line"`
	Event   string    `yaml:"event"`
	Message string    `yaml:"message"`
	Open    bool      `yaml:"open"`
}

type feedView struct {
	Open      []openEntry   `yaml:"open"`
	Recent    []recentEntry `yaml:"recent"`
	Waited24h string        `yaml:"waited_24h"`
}

type wait struct {
	start, end time.Time
	index      int
}

func waits(evs []event) []wait {
	var ws []wait
	for i, e := range evs {
		open := len(ws) > 0 && ws[len(ws)-1].end.IsZero()
		switch {
		case startsWait[e.Event] && !open:
			ws = append(ws, wait{start: e.At, index: i})
		case endsWait[e.Event] && open:
			ws[len(ws)-1].end = e.At
		}
	}
	return ws
}

func waited(ws []wait, from, now time.Time) time.Duration {
	var total time.Duration
	for _, w := range ws {
		start, end := w.start, w.end
		if end.IsZero() {
			end = now
		}
		if start.Before(from) {
			start = from
		}
		if end.After(start) {
			total += end.Sub(start)
		}
	}
	return total
}

func firstLine(content string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(content), "\n")
	return line
}

func (s *store) feed(now time.Time) (feedView, error) {
	v := feedView{Open: []openEntry{}, Recent: []recentEntry{}}
	projs, err := s.projects()
	if err != nil {
		return v, err
	}
	var total time.Duration
	for _, proj := range projs {
		ids, err := s.itemIDs(proj)
		if err != nil {
			return v, err
		}
		for _, id := range ids {
			it, err := s.item(proj, id)
			if err != nil {
				return v, err
			}
			evs, err := s.events(proj, id)
			if err != nil {
				return v, err
			}
			ws := waits(evs)
			total += waited(ws, now.Add(-24*time.Hour), now)
			openIndex := -1
			if it.Attention != "" && it.Column != archive {
				since := it.Created
				if len(ws) > 0 && ws[len(ws)-1].end.IsZero() {
					since, openIndex = ws[len(ws)-1].start, ws[len(ws)-1].index
				}
				v.Open = append(v.Open, openEntry{ID: id, Project: proj, Line: firstLine(it.Content), Column: it.Column, Status: it.Status,
					Attention: it.Attention, Since: since, Waiting: now.Sub(since).Round(time.Second).String()})
			}
			for i, e := range evs {
				if startsWait[e.Event] {
					v.Recent = append(v.Recent, recentEntry{At: e.At, ID: id, Project: proj, Line: firstLine(it.Content), Event: e.Event, Message: e.Message, Open: i == openIndex})
				}
			}
		}
	}
	slices.SortStableFunc(v.Open, func(a, b openEntry) int { return a.Since.Compare(b.Since) })
	slices.SortStableFunc(v.Recent, func(a, b recentEntry) int { return b.At.Compare(a.At) })
	v.Recent = v.Recent[:min(len(v.Recent), 100)]
	v.Waited24h = total.Round(time.Second).String()
	return v, nil
}

func (s *store) push(proj string, id int, e event) {
	cfg := s.config()
	if cfg.Ntfy == "" {
		return
	}
	base := cfg.URL
	if base == "" {
		base = s.base
	}
	it, _ := s.item(proj, id)
	title := "#" + strconv.Itoa(id) + " " + s.projectName(proj) + ": " + firstLine(it.Content)
	go func() {
		req, err := http.NewRequest("POST", cfg.Ntfy, bytes.NewBufferString(e.Message))
		if err != nil {
			log.Print("ntfy: ", err)
			return
		}
		req.Header.Set("Title", mime.BEncoding.Encode("utf-8", title))
		req.Header.Set("Click", strings.TrimRight(base, "/")+"/ui/"+proj+"/item/"+strconv.Itoa(id))
		if e.Event == "failed" {
			req.Header.Set("Tags", "x")
		}
		resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
		if err != nil {
			log.Print("ntfy: ", err)
			return
		}
		resp.Body.Close()
		if resp.StatusCode >= 400 {
			log.Print("ntfy: ", resp.Status)
		}
	}()
}
