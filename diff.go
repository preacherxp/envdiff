package main

import (
	"fmt"
	"strings"
)

// Statuses, from worst to fine. The first source is the reference.
const (
	Missing = "missing" // in the reference, absent from another source
	Extra   = "extra"   // not in the reference
	Differs = "differs"
	Near    = "near" // differs only by whitespace, a trailing newline, quotes, case or double base64
	Same    = "same"
)

type Cell struct {
	Present bool   `json:"present"`
	Value   string `json:"value"`
	Note    string `json:"note,omitempty"` // the near-miss reason, or "differs"
}

type Row struct {
	Key    string `json:"key"`
	Status string `json:"status"`
	Detail string `json:"detail"`
	Cells  []Cell `json:"cells"`
}

type Result struct {
	Sources []*Source      `json:"sources"`
	Rows    []Row          `json:"rows"`
	Counts  map[string]int `json:"counts"`
}

func (r Result) Changed() bool { return r.Counts[Same] != len(r.Rows) }

// nearMiss names the one harmless-looking difference between two values, or returns "".
func nearMiss(a, b string) string {
	switch {
	case strings.TrimRight(a, "\r\n") == strings.TrimRight(b, "\r\n"):
		return "trailing newline"
	case strings.TrimSpace(a) == strings.TrimSpace(b):
		return "whitespace"
	case unquote(a) == unquote(b):
		return "quotes"
	case strings.EqualFold(a, b):
		return "case"
	}
	for _, p := range [][2]string{{a, b}, {b, a}} {
		if d, ok := b64(p[0]); ok && strings.TrimRight(d, "\n") == strings.TrimRight(p[1], "\n") {
			return "double base64"
		}
	}
	return ""
}

func unquote(s string) string { return strings.Trim(strings.TrimSpace(s), `"'`) }

// compare lines every key up across the sources. keysOnly skips the values, for files
// like .env.example whose values are placeholders.
func compare(srcs []*Source, keysOnly bool) Result {
	var keys []string
	seen := map[string]bool{}
	for _, s := range srcs {
		for _, k := range s.Keys {
			if !seen[k] {
				seen[k] = true
				keys = append(keys, k)
			}
		}
	}
	res := Result{Sources: srcs, Counts: map[string]int{}}
	for _, k := range keys {
		row := Row{Key: k, Status: Same}
		var absent, present, notes []string
		for i, s := range srcs {
			v, ok := s.Vars[k]
			row.Cells = append(row.Cells, Cell{Present: ok, Value: v})
			if !ok {
				absent = append(absent, s.Name)
				continue
			}
			present = append(present, s.Name)
			ref := row.Cells[0]
			if i == 0 || !ref.Present || keysOnly || v == ref.Value {
				continue
			}
			note := nearMiss(ref.Value, v)
			if note == "" {
				note, row.Status = Differs, Differs
			} else if row.Status == Same {
				row.Status = Near
			}
			row.Cells[i].Note = note
			notes = append(notes, note+" in "+s.Name)
		}
		switch {
		case !row.Cells[0].Present:
			row.Status, row.Detail = Extra, "only in "+strings.Join(present, ", ")
		case len(absent) > 0:
			row.Status, row.Detail = Missing, "missing in "+strings.Join(absent, ", ")
		default:
			row.Detail = strings.Join(notes, "; ")
		}
		res.Counts[row.Status]++
		res.Rows = append(res.Rows, row)
	}
	return res
}

// summary reads like "12 keys · 5 same · 2 missing · 3 near-misses".
func summary(r Result) string {
	parts := []string{fmt.Sprintf("%d keys", len(r.Rows))}
	for _, s := range []string{Same, Missing, Extra, Near, Differs} {
		if n := r.Counts[s]; n > 0 {
			label := s
			if s == Near {
				label = "near-miss"
			}
			parts = append(parts, fmt.Sprintf("%d %s", n, label))
		}
	}
	return strings.Join(parts, " · ")
}
