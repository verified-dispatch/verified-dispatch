package config

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestCheckStructureAcceptsOrdinaryFiles(t *testing.T) {
	good := []string{
		homeOK,
		repoOK,
		"",
		"# only a comment with [brackets] and {braces} and 'quotes'\n",
		"[gate]\ncommand = \"make a && make b\" # trailing [comment]\n",
		// Brackets, braces, and dots inside strings are values, not structure.
		"[redaction]\npatterns = [\"[[[[[[[[[[\", \"{{{{{{{{{{\", \"a.b.c.d.e.f.g.h.i.j\", '\\[', \"\\\\[\"]\n",
		// Multi-line strings may hold anything.
		"[gate]\ncommand = \"\"\"\nmake [[[[[[[[[[ {{{{{{{{{{\n\"\"\"\nprepare = '''\n[[[[[[[[[[\n'''\n",
		// Quoted keys with dots are one segment each.
		"[presets.\"a.b.c.d.e.f.g.h.i\"]\ncommand = [\"x\"]\nprompt = \"stdin\"\n",
		// Up to eight levels is fine.
		"a = {b = {c = {d = {e = {f = {g = [[1]]}}}}}}\n",
		// A multi-line array of inline tables.
		"x = [\n  {a = 1, b = \"[\"},\n  {a = 2, b = \"{\"},\n]\n",
		// Floats carry one dot in value position.
		"x = 1.5\ny = [1.5, 2.5, 3.5]\n",
		// Array-of-tables headers.
		"[[presets.list]]\ncommand = [\"x\"]\n",
	}
	for _, src := range good {
		if err := checkStructure([]byte(src)); err != nil {
			t.Errorf("checkStructure(%q) = %v, want nil", src, err)
		}
	}
}

func TestCheckStructureRefusesDeepNesting(t *testing.T) {
	deep := "a = " + strings.Repeat("{b = ", MaxNesting+1) + "1" + strings.Repeat("}", MaxNesting+1) + "\n"
	if err := checkStructure([]byte(deep)); !errors.Is(err, errTooDeep) {
		t.Fatalf("inline tables: err = %v, want errTooDeep", err)
	}
	arrays := "a = " + strings.Repeat("[", MaxNesting+1) + "1" + strings.Repeat("]", MaxNesting+1) + "\n"
	if err := checkStructure([]byte(arrays)); !errors.Is(err, errTooDeep) {
		t.Fatalf("arrays: err = %v, want errTooDeep", err)
	}
	// Exactly MaxNesting levels is allowed.
	edge := "a = " + strings.Repeat("[", MaxNesting) + "1" + strings.Repeat("]", MaxNesting) + "\n"
	if err := checkStructure([]byte(edge)); err != nil {
		t.Fatalf("exactly %d levels: err = %v, want nil", MaxNesting, err)
	}
}

func TestCheckStructureRefusesLongDottedKeys(t *testing.T) {
	header := "[" + strings.TrimSuffix(strings.Repeat("a.", MaxKeySegments+1), ".") + "]\n"
	if err := checkStructure([]byte(header)); !errors.Is(err, errKeySegments) {
		t.Fatalf("header: err = %v, want errKeySegments", err)
	}
	dotted := strings.TrimSuffix(strings.Repeat("a.", MaxKeySegments+1), ".") + " = 1\n"
	if err := checkStructure([]byte(dotted)); !errors.Is(err, errKeySegments) {
		t.Fatalf("dotted key: err = %v, want errKeySegments", err)
	}
	inline := "t = {" + strings.TrimSuffix(strings.Repeat("a.", MaxKeySegments+1), ".") + " = 1}\n"
	if err := checkStructure([]byte(inline)); !errors.Is(err, errKeySegments) {
		t.Fatalf("inline table key: err = %v, want errKeySegments", err)
	}
	edge := "[" + strings.TrimSuffix(strings.Repeat("a.", MaxKeySegments), ".") + "]\n"
	if err := checkStructure([]byte(edge)); err != nil {
		t.Fatalf("exactly %d segments: err = %v, want nil", MaxKeySegments, err)
	}
}

func TestCheckStructureRefusesLongKeys(t *testing.T) {
	long := strings.Repeat("k", MaxKeyBytes+1) + " = 1\n"
	if err := checkStructure([]byte(long)); !errors.Is(err, errKeyTooLong) {
		t.Fatalf("bare key: err = %v, want errKeyTooLong", err)
	}
	quoted := "\"" + strings.Repeat("k", MaxKeyBytes+1) + "\" = 1\n"
	if err := checkStructure([]byte(quoted)); !errors.Is(err, errKeyTooLong) {
		t.Fatalf("quoted key: err = %v, want errKeyTooLong", err)
	}
	// A long value is not a long key.
	value := "k = \"" + strings.Repeat("v", MaxKeyBytes*4) + "\"\n"
	if err := checkStructure([]byte(value)); err != nil {
		t.Fatalf("long value: err = %v, want nil", err)
	}
}

func TestCheckStructureRefusesUnterminatedStrings(t *testing.T) {
	for _, src := range []string{"k = \"open\n", "k = 'open\n", "k = \"\"\"open\n", "k = '''open\n"} {
		if err := checkStructure([]byte(src)); !errors.Is(err, errUnterminated) {
			t.Errorf("checkStructure(%q) = %v, want errUnterminated", src, err)
		}
	}
}

func TestLoadRefusesExpensiveFilesBeforeDecoding(t *testing.T) {
	// 64 KiB of nested inline tables, the shape that cost seconds and
	// gigabytes when it reached the decoder.
	var b strings.Builder
	b.WriteString("a = ")
	for b.Len() < ConfigFileBytes-64 {
		b.WriteString("{a = ")
	}
	b.WriteString("1")
	nested := b.String()
	start := time.Now()
	wantPlainError(t, Inputs{Repo: []byte(nested), Flags: Flags{Gate: "x"}}, "nests")
	wantPlainError(t, Inputs{Home: []byte(nested), Flags: Flags{Gate: "x"}}, "nests")
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("structure check took %s, want well under a second", d)
	}
	header := "[" + strings.Repeat("a.", 32000) + "a]\n"
	wantPlainError(t, Inputs{Repo: []byte(header), Flags: Flags{Gate: "x"}}, "dotted segments")
}
