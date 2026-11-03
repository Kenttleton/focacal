package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"

	"gopkg.in/yaml.v3"
)

func addKey(doc *yaml.Node, key, val string) {
	root := doc.Content[0]
	root.Content = append(root.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Value: val})
}

func writeTemp(t *testing.T, content string, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestUpdateIsAtomic(t *testing.T) {
	p := writeTemp(t, "# keep\na: 1\n", 0644)
	before, _ := os.ReadFile(p)
	err := NewWriter(p).Update(func(doc *yaml.Node) error {
		addKey(doc, "b", "2")
		return fmt.Errorf("boom")
	})
	if err == nil {
		t.Fatal("expected error")
	}
	after, _ := os.ReadFile(p)
	if !bytes.Equal(before, after) {
		t.Fatalf("file changed: %q", after)
	}
	tmps, _ := filepath.Glob(filepath.Join(filepath.Dir(p), "*.tmp"))
	if len(tmps) != 0 {
		t.Fatalf("temp files left: %v", tmps)
	}
}

func TestUpdatePreservesMode(t *testing.T) {
	p := writeTemp(t, "a: 1\n", 0600)
	if err := NewWriter(p).Update(func(doc *yaml.Node) error {
		addKey(doc, "b", "2")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(p)
	if st.Mode().Perm() != 0600 {
		t.Fatalf("mode = %v", st.Mode().Perm())
	}
}

func TestUpdatePreservesComments(t *testing.T) {
	p := writeTemp(t, "# top comment\na: 1 # inline\n", 0644)
	if err := NewWriter(p).Update(func(doc *yaml.Node) error {
		addKey(doc, "b", "2")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p)
	if !bytes.Contains(got, []byte("# top comment")) || !bytes.Contains(got, []byte("# inline")) || !bytes.Contains(got, []byte("b: 2")) {
		t.Fatalf("got %q", got)
	}
}

func TestUpdateFallsBackWhenRenameFails(t *testing.T) {
	p := writeTemp(t, "a: 1\n", 0644)
	orig := renameFile
	renameFile = func(string, string) error { return syscall.EBUSY }
	defer func() { renameFile = orig }()

	if err := NewWriter(p).Update(func(doc *yaml.Node) error {
		addKey(doc, "b", "2")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p)
	if !bytes.Contains(got, []byte("b: 2")) {
		t.Fatalf("not updated: %q", got)
	}
	bak, err := os.ReadFile(p + ".bak")
	if err != nil {
		t.Fatal(err)
	}
	if string(bak) != "a: 1\n" {
		t.Fatalf("bak = %q", bak)
	}
	tmps, _ := filepath.Glob(filepath.Join(filepath.Dir(p), "*.tmp"))
	if len(tmps) != 0 {
		t.Fatalf("temp files left: %v", tmps)
	}
}

func TestUpdateNoBakOnRenameSuccess(t *testing.T) {
	p := writeTemp(t, "a: 1\n", 0644)
	if err := NewWriter(p).Update(func(doc *yaml.Node) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p + ".bak"); !os.IsNotExist(err) {
		t.Fatalf("unexpected .bak: %v", err)
	}
}

func TestUpdateSerializesConcurrentCallers(t *testing.T) {
	p := writeTemp(t, "a: 1\n", 0644)
	w := NewWriter(p)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := w.Update(func(doc *yaml.Node) error {
				addKey(doc, fmt.Sprintf("k%d", i), "v")
				return nil
			}); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	data, _ := os.ReadFile(p)
	var m map[string]string
	if err := yaml.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		if _, ok := m[fmt.Sprintf("k%d", i)]; !ok {
			t.Fatalf("missing k%d", i)
		}
	}
}

func TestUpdateCreatesMissingFileWithMode(t *testing.T) {
	p := filepath.Join(t.TempDir(), "new.yaml")
	if err := NewWriterMode(p, 0600).Update(func(doc *yaml.Node) error {
		addKey(doc, "a", "1")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0600 {
		t.Fatalf("mode = %v", st.Mode().Perm())
	}
	got, _ := os.ReadFile(p)
	if !bytes.Contains(got, []byte("a: \"1\"")) && !bytes.Contains(got, []byte("a: 1")) {
		t.Fatalf("got %q", got)
	}
}

func TestUpdateOnEmptyFileGivesEmptyMapping(t *testing.T) {
	p := writeTemp(t, "", 0644)
	var kind yaml.Kind
	var childKind yaml.Kind
	if err := NewWriter(p).Update(func(doc *yaml.Node) error {
		kind = doc.Kind
		if len(doc.Content) == 1 {
			childKind = doc.Content[0].Kind
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if kind != yaml.DocumentNode || childKind != yaml.MappingNode {
		t.Fatalf("kind=%v child=%v", kind, childKind)
	}
}
