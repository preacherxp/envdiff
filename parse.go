package main

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Source is one set of variables: a dotenv file, one ConfigMap or Secret, or one Compose service.
type Source struct {
	Name   string            `json:"name"`
	Format string            `json:"format"` // dotenv, configmap, secret, compose
	Sub    string            `json:"-"`      // object or service name, matched by file#sub
	Keys   []string          `json:"keys"`   // in file order
	Vars   map[string]string `json:"-"`
}

func (s *Source) set(k, v string) {
	if _, ok := s.Vars[k]; !ok {
		s.Keys = append(s.Keys, k)
	}
	s.Vars[k] = v
}

func newSource(name, format, sub string) *Source {
	return &Source{Name: name, Format: format, Sub: sub, Vars: map[string]string{}}
}

// load reads one argument: a path, "-" for stdin, either optionally followed by #name.
func load(arg string) ([]*Source, error) {
	path, sel := arg, ""
	if i := strings.LastIndex(arg, "#"); i > 0 {
		path, sel = arg[:i], arg[i+1:]
	}
	var data []byte
	var err error
	if path == "-" {
		data, err = io.ReadAll(os.Stdin)
		path = "stdin"
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, err
	}
	return parseNamed(path, sel, string(data))
}

// parseNamed parses text and keeps only the source called sel, when one is given.
func parseNamed(name, sel, text string) ([]*Source, error) {
	srcs, err := parse(name, text)
	if err != nil || sel == "" {
		return srcs, err
	}
	var names []string
	for _, s := range srcs {
		if s.Sub == sel {
			return []*Source{s}, nil
		}
		names = append(names, s.Sub)
	}
	return nil, fmt.Errorf("%s: no %q in it (found: %s)", name, sel, strings.Join(names, ", "))
}

// parse detects the format: Kubernetes ConfigMaps and Secrets, then Compose, then dotenv.
func parse(name, text string) ([]*Source, error) {
	srcs, err := parseYAML(name, text)
	if err != nil || len(srcs) > 0 {
		return srcs, err
	}
	s, err := parseDotenv(name, text)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return []*Source{s}, nil
}

// parseYAML returns no sources and no error when the text is not a manifest or Compose file.
func parseYAML(name, text string) ([]*Source, error) {
	dec := yaml.NewDecoder(strings.NewReader(text))
	var out []*Source
	for {
		var doc yaml.Node
		if err := dec.Decode(&doc); err != nil {
			if errors.Is(err, io.EOF) {
				return out, nil
			}
			return nil, nil // not YAML, so let dotenv have a go
		}
		if len(doc.Content) == 0 {
			continue
		}
		root := doc.Content[0]
		switch kind := scalar(get(root, "kind")); {
		case kind == "ConfigMap" || kind == "Secret":
			obj := scalar(get(get(root, "metadata"), "name"))
			s := newSource(name+"#"+obj, strings.ToLower(kind), obj)
			for _, p := range pairs(get(root, "data")) {
				v := scalar(p[1])
				if kind == "Secret" {
					b, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(v), ""))
					if err != nil {
						return nil, fmt.Errorf("%s: Secret %s: data.%s is not valid base64", name, obj, p[0].Value)
					}
					v = string(b)
				}
				s.set(p[0].Value, v)
			}
			for _, p := range pairs(get(root, "stringData")) {
				s.set(p[0].Value, scalar(p[1]))
			}
			out = append(out, s)
		case get(root, "services") != nil:
			for _, svc := range pairs(get(root, "services")) {
				env := deref(get(svc[1], "environment"))
				if env == nil {
					continue
				}
				s := newSource(name+"#"+svc[0].Value, "compose", svc[0].Value)
				if env.Kind == yaml.SequenceNode { // - KEY=value, or - KEY to pass it through from the shell
					for _, item := range env.Content {
						k, v, _ := strings.Cut(scalar(item), "=")
						s.set(k, v)
					}
				}
				for _, p := range pairs(env) {
					s.set(p[0].Value, scalar(p[1]))
				}
				out = append(out, s)
			}
		}
	}
}

func deref(n *yaml.Node) *yaml.Node {
	if n != nil && n.Kind == yaml.AliasNode {
		return n.Alias
	}
	return n
}

// pairs lists a mapping's key and value nodes, expanding <<: merge keys.
// ponytail: merged keys are applied in file order, so a merge listed after an explicit key wins; YAML says it should not.
func pairs(n *yaml.Node) [][2]*yaml.Node {
	n = deref(n)
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	var out [][2]*yaml.Node
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], deref(n.Content[i+1])
		if k.Value == "<<" {
			if v.Kind == yaml.SequenceNode {
				for _, m := range v.Content {
					out = append(out, pairs(m)...)
				}
			} else {
				out = append(out, pairs(v)...)
			}
			continue
		}
		out = append(out, [2]*yaml.Node{k, v})
	}
	return out
}

func get(n *yaml.Node, key string) *yaml.Node {
	for _, p := range pairs(n) {
		if p[0].Value == key {
			return p[1]
		}
	}
	return nil
}

// scalar keeps the value exactly as written, so 1.10 stays 1.10 and null becomes empty.
func scalar(n *yaml.Node) string {
	n = deref(n)
	if n == nil || n.Kind != yaml.ScalarNode || n.Tag == "!!null" {
		return ""
	}
	return n.Value
}

var unescape = strings.NewReplacer(`\\`, `\`, `\n`, "\n", `\r`, "\r", `\t`, "\t", `\"`, `"`, `\$`, `$`)

// parseDotenv reads KEY=value lines: comments, export, single and double quotes,
// escapes in double quotes and quoted values that run over several lines.
func parseDotenv(name, text string) (*Source, error) {
	s := newSource(name, "dotenv", "")
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" || line[0] == '#' {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		k = strings.TrimSpace(k)
		if !ok || k == "" || strings.ContainsAny(k, " \t") {
			return nil, fmt.Errorf("line %d: expected KEY=value", i+1)
		}
		v = strings.TrimLeft(v, " \t")
		if v == "" || (v[0] != '"' && v[0] != '\'') {
			if j := strings.Index(v, " #"); j >= 0 {
				v = v[:j]
			}
			s.set(k, strings.TrimSpace(v))
			continue
		}
		q, body, start := v[0], v[1:], i+1
		end := closing(body, q)
		for end < 0 && i+1 < len(lines) {
			i++
			body += "\n" + lines[i]
			end = closing(body, q)
		}
		if end < 0 {
			return nil, fmt.Errorf("line %d: %s has no closing %c", start, k, q)
		}
		v = body[:end]
		if q == '"' {
			v = unescape.Replace(v)
		}
		s.set(k, v)
	}
	return s, nil
}

// closing finds the quote that ends a value, skipping backslash escapes inside double quotes.
func closing(s string, q byte) int {
	for i := 0; i < len(s); i++ {
		if q == '"' && s[i] == '\\' {
			i++
			continue
		}
		if s[i] == q {
			return i
		}
	}
	return -1
}

// b64 decodes a value that looks base64 encoded, ignoring the newline base64(1) adds.
func b64(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if len(s) < 4 {
		return "", false
	}
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil || bytes.ContainsRune(b, 0) {
		return "", false
	}
	return string(b), true
}
