package main

import (
	"reflect"
	"testing"
)

func TestDotenv(t *testing.T) {
	s, err := parseDotenv("x", "# c\nexport A=1\nB = two words # note\nC=\"a\\nb \\\"q\\\"\"\nD='lit $X \\n'\nE=\"line1\nline2\"\nF=\nG=a#b\r\n")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"A": "1", "B": "two words", "C": "a\nb \"q\"", "D": `lit $X \n`, "E": "line1\nline2", "F": "", "G": "a#b"}
	if !reflect.DeepEqual(s.Vars, want) {
		t.Fatalf("got %q", s.Vars)
	}
	if !reflect.DeepEqual(s.Keys, []string{"A", "B", "C", "D", "E", "F", "G"}) {
		t.Fatalf("order %v", s.Keys)
	}
	if _, err := parseDotenv("x", "A=\"open\nB=1\n"); err == nil {
		t.Fatal("unterminated quote should fail")
	}
	if _, err := parseDotenv("x", "not a pair\n"); err == nil {
		t.Fatal("a line without = should fail")
	}
}

func TestKubernetes(t *testing.T) {
	text := "kind: ConfigMap\nmetadata: {name: cfg}\ndata:\n  A: \"1.10\"\n  B: |\n    pem\n---\nkind: Secret\nmetadata: {name: sec}\ndata:\n  A: aGk=\nstringData:\n  B: plain\n"
	srcs, err := parse("k.yaml", text)
	if err != nil || len(srcs) != 2 {
		t.Fatal(srcs, err)
	}
	if srcs[0].Name != "k.yaml#cfg" || srcs[0].Format != "configmap" || srcs[0].Vars["A"] != "1.10" || srcs[0].Vars["B"] != "pem\n" {
		t.Fatalf("configmap %+v", srcs[0])
	}
	if srcs[1].Format != "secret" || srcs[1].Vars["A"] != "hi" || srcs[1].Vars["B"] != "plain" {
		t.Fatalf("secret %+v", srcs[1])
	}
	one, err := parseNamed("k.yaml", "sec", text)
	if err != nil || len(one) != 1 || one[0].Sub != "sec" {
		t.Fatal(one, err)
	}
	if _, err := parseNamed("k.yaml", "nope", text); err == nil {
		t.Fatal("an unknown #name should fail")
	}
	if _, err := parse("k.yaml", "kind: Secret\nmetadata: {name: s}\ndata: {A: \"not base64!\"}\n"); err == nil {
		t.Fatal("bad base64 should fail")
	}
}

func TestCompose(t *testing.T) {
	srcs, err := load("testdata/demo/compose.yml")
	if err != nil || len(srcs) != 2 {
		t.Fatal(srcs, err)
	}
	api, worker := srcs[0], srcs[1]
	if api.Name != "testdata/demo/compose.yml#api" || api.Vars["LOG_LEVEL"] != `"info"` || api.Vars["REDIS_URL"] != "redis://cache.staging.internal:6379/0 " {
		t.Fatalf("api %q", api.Vars)
	}
	want := map[string]string{"PORT": "8080", "SMTP_HOST": "smtp.mailgun.org", "QUEUE": "default", "LOG_LEVEL": ""}
	if !reflect.DeepEqual(worker.Vars, want) {
		t.Fatalf("worker %q", worker.Vars)
	}
}
