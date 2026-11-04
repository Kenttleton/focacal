package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"

	"go.yaml.in/yaml/v3"
)

// appendLine returns a fn for Update that appends "key: val".
func appendLine(key, val string) func([]byte) ([]byte, error) {
	return func(cur []byte) ([]byte, error) {
		return append(append([]byte{}, cur...), []byte(key+": "+val+"\n")...), nil
	}
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
	err := NewWriter(p).Update(func(cur []byte) ([]byte, error) {
		return append(cur, "b: 2\n"...), fmt.Errorf("boom")
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
	if err := NewWriter(p).Update(appendLine("b", "2")); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(p)
	if st.Mode().Perm() != 0600 {
		t.Fatalf("mode = %v", st.Mode().Perm())
	}
}

func TestUpdateWritesFnBytesVerbatim(t *testing.T) {
	p := writeTemp(t, "# top comment\na: 1 # inline\n", 0644)
	if err := NewWriter(p).Update(appendLine("b", "2")); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p)
	if string(got) != "# top comment\na: 1 # inline\nb: 2\n" {
		t.Fatalf("got %q", got)
	}
}

func TestWriterUpdateSeesDiskState(t *testing.T) {
	p := writeTemp(t, "a: 1\n", 0644)
	w := NewWriter(p)
	if err := w.Update(appendLine("b", "2")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("external: yes\n"), 0644); err != nil {
		t.Fatal(err)
	}
	var seen string
	if err := w.Update(func(cur []byte) ([]byte, error) {
		seen = string(cur)
		return cur, nil
	}); err != nil {
		t.Fatal(err)
	}
	if seen != "external: yes\n" {
		t.Fatalf("second Update saw %q", seen)
	}
}

func TestUpdateFallsBackWhenRenameFails(t *testing.T) {
	p := writeTemp(t, "a: 1\n", 0644)
	orig := renameFile
	renameFile = func(string, string) error { return syscall.EBUSY }
	defer func() { renameFile = orig }()

	if err := NewWriter(p).Update(appendLine("b", "2")); err != nil {
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
	if err := NewWriter(p).Update(func(cur []byte) ([]byte, error) { return cur, nil }); err != nil {
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
			if err := w.Update(appendLine(fmt.Sprintf("k%d", i), "v")); err != nil {
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
	if err := NewWriterMode(p, 0600).Update(appendLine("a", "1")); err != nil {
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

func TestUpdateMissingFileSeesEmptyBytes(t *testing.T) {
	p := filepath.Join(t.TempDir(), "nope.yaml")
	var seen []byte = []byte("sentinel")
	if err := NewWriter(p).Update(func(cur []byte) ([]byte, error) {
		seen = cur
		return []byte("a: 1\n"), nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 0 {
		t.Fatalf("missing file should read as empty, got %q", seen)
	}
}

func TestUpdateTempStageFailureDoesNotFallBack(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("read-only directory is not enforced for root")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte("a: 1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0755)

	err := NewWriter(p).Update(appendLine("b", "2"))
	if err == nil {
		t.Fatal("expected error")
	}
	got, _ := os.ReadFile(p)
	if string(got) != "a: 1\n" {
		t.Fatalf("target changed: %q", got)
	}
	if _, err := os.Stat(p + ".bak"); !os.IsNotExist(err) {
		t.Fatalf("unexpected .bak: %v", err)
	}
}
