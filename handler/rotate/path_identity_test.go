package rotate

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestRotationRejectsMissingActivePath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not permit deleting this open writer file")
	}
	path := filepath.Join(t.TempDir(), "app.log")
	writer := mustNewWriter(t, Options{Path: path, MaxBytes: 1, Backups: 1})
	t.Cleanup(func() {
		if err := writer.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	if _, err := writer.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if n, err := writer.Write([]byte("y")); n != 0 || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Write after removal = %d, %v; want zero and missing path", n, err)
	}
	if got := writer.Stats(); got != (Stats{Bytes: 1}) {
		t.Errorf("rejected rotation stats = %#v", got)
	}
	for _, name := range []string{path, path + ".1"} {
		if _, err := os.Lstat(name); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("rejected rotation created %q: %v", name, err)
		}
	}
}

func TestRotationRejectsReplacedActiveIdentity(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not permit renaming this open writer file")
	}
	path := filepath.Join(t.TempDir(), "app.log")
	oldPath := path + ".owned"
	writer := mustNewWriter(t, Options{Path: path, MaxBytes: 1, Backups: 1})
	t.Cleanup(func() {
		if err := writer.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	if _, err := writer.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, oldPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	if n, err := writer.Write([]byte("y")); n != 0 || !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("Write after replacement = %d, %v; want zero and unsafe path", n, err)
	}
	assertFile(t, path, "replacement")
	assertFile(t, oldPath, "x")
	if got := writer.Stats(); got != (Stats{Bytes: 1}) {
		t.Errorf("rejected rotation stats = %#v", got)
	}
	if _, err := os.Lstat(path + ".1"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("rejected rotation created backup: %v", err)
	}
}
