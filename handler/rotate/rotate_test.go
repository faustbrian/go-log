package rotate

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNewValidatesOptions(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		options Options
		want    error
	}{
		"empty path":       {options: Options{MaxBytes: 1}, want: ErrInvalidPath},
		"zero bytes":       {options: Options{Path: "app.log"}, want: ErrInvalidMaxBytes},
		"negative backups": {options: Options{Path: "app.log", MaxBytes: 1, Backups: -1}, want: ErrInvalidBackups},
		"invalid mode":     {options: Options{Path: "app.log", MaxBytes: 1, Mode: os.ModeDir}, want: ErrInvalidMode},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			writer, err := New(test.options)
			if writer != nil {
				t.Fatalf("New() writer = %v, want nil", writer)
			}
			if !errors.Is(err, test.want) {
				t.Fatalf("New() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestNewAppendsExistingFileAndEnforcesPermissions(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "app.log")
	if err := os.WriteFile(path, []byte("existing\n"), 0o666); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	writer, err := New(Options{Path: path, MaxBytes: 100, Backups: 2, Mode: 0o640})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := writer.Write([]byte("new\n")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(contents) != "existing\nnew\n" {
		t.Fatalf("contents = %q, want existing and new", contents)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if got := info.Mode().Perm(); got != 0o640 {
		t.Fatalf("mode = %o, want 640", got)
	}
}

func TestNewDefaultsToOwnerOnlyPermissions(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "app.log")
	writer := mustNewWriter(t, Options{Path: path, MaxBytes: 100})
	if err := writer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode = %o, want 600", got)
	}
}

func TestWriteRotatesAndCascadesBackups(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "app.log")
	writer := mustNewWriter(t, Options{Path: path, MaxBytes: 5, Backups: 2, Mode: 0o600})
	for _, value := range []string{"one\n", "two\n", "three\n", "four\n"} {
		if _, err := writer.Write([]byte(value)); err != nil {
			t.Fatalf("Write(%q) error = %v", value, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	assertFile(t, path, "four\n")
	assertFile(t, path+".1", "three\n")
	assertFile(t, path+".2", "two\n")
	if got := writer.Stats(); got.Rotations != 3 || got.Bytes != 5 {
		t.Fatalf("Stats() = %+v, want three rotations and five current bytes", got)
	}
}

func TestZeroBackupsTruncatesOnRotation(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "app.log")
	writer := mustNewWriter(t, Options{Path: path, MaxBytes: 4, Backups: 0, Mode: 0o600})
	if _, err := writer.Write([]byte("old\n")); err != nil {
		t.Fatalf("Write(old) error = %v", err)
	}
	if _, err := writer.Write([]byte("new\n")); err != nil {
		t.Fatalf("Write(new) error = %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	assertFile(t, path, "new\n")
	if _, err := os.Stat(path + ".1"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("backup Stat() error = %v, want not exist", err)
	}
}

func TestOversizedWriteRemainsAtomicAndRotatesBeforeNextWrite(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "app.log")
	writer := mustNewWriter(t, Options{Path: path, MaxBytes: 3, Backups: 1, Mode: 0o600})
	if _, err := writer.Write([]byte("oversized")); err != nil {
		t.Fatalf("Write(oversized) error = %v", err)
	}
	if _, err := writer.Write([]byte("x")); err != nil {
		t.Fatalf("Write(x) error = %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	assertFile(t, path, "x")
	assertFile(t, path+".1", "oversized")
}

func TestConcurrentWritesRemainWhole(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "app.log")
	writer := mustNewWriter(t, Options{Path: path, MaxBytes: 1 << 20, Backups: 1, Mode: 0o600})
	const count = 100
	var wait sync.WaitGroup
	for index := 0; index < count; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			if _, err := writer.Write([]byte(strings.Repeat("x", index%10) + "line\n")); err != nil {
				t.Errorf("Write() error = %v", err)
			}
		}(index)
	}
	wait.Wait()
	if err := writer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	lines := strings.Split(strings.TrimSuffix(string(contents), "\n"), "\n")
	if len(lines) != count {
		t.Fatalf("line count = %d, want %d", len(lines), count)
	}
	sort.Strings(lines)
}

func TestCloseIsSafeDuringConcurrentWrites(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "app.log")
	writer := mustNewWriter(t, Options{Path: path, MaxBytes: 1 << 20, Backups: 1, Mode: 0o600})
	start := make(chan struct{})
	results := make(chan error, 101)
	var wait sync.WaitGroup
	for index := 0; index < 100; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, err := writer.Write([]byte("line\n"))
			results <- err
		}()
	}
	wait.Add(1)
	go func() {
		defer wait.Done()
		<-start
		results <- writer.Close()
	}()
	close(start)
	wait.Wait()
	close(results)
	for err := range results {
		if err != nil && !errors.Is(err, ErrClosed) {
			t.Fatalf("concurrent operation error = %v, want nil or ErrClosed", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("final Close() error = %v", err)
	}
}

func TestSyncCloseAndClosedWrites(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "app.log")
	writer := mustNewWriter(t, Options{Path: path, MaxBytes: 10, Backups: 1, Mode: 0o600})
	if err := writer.Sync(); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if err := writer.Sync(); !errors.Is(err, ErrClosed) {
		t.Fatalf("Sync() after Close error = %v, want ErrClosed", err)
	}
	if n, err := writer.Write([]byte("late")); n != 0 || !errors.Is(err, ErrClosed) {
		t.Fatalf("Write() after Close = (%d, %v), want 0 ErrClosed", n, err)
	}
}

func TestWriteReportsShortAndFailingWrites(t *testing.T) {
	short := &fakeFile{info: fakeInfo{size: 0}, writeN: 1}
	restore := replaceOpenFile(func(string, int, os.FileMode) (file, error) { return short, nil })
	writer, err := New(Options{Path: "app.log", MaxBytes: 10, Backups: 1, Mode: 0o600})
	restore()
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if n, err := writer.Write([]byte("abc")); n != 1 || !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("Write(short) = (%d, %v), want 1 ErrShortWrite", n, err)
	}

	want := errors.New("disk full")
	failing := &fakeFile{info: fakeInfo{size: 0}, writeErr: want}
	restore = replaceOpenFile(func(string, int, os.FileMode) (file, error) { return failing, nil })
	writer, err = New(Options{Path: "app.log", MaxBytes: 10, Backups: 1, Mode: 0o600})
	restore()
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := writer.Write([]byte("abc")); !errors.Is(err, want) {
		t.Fatalf("Write(failing) error = %v, want %v", err, want)
	}
}

func TestNewReportsOpenStatAndChmodFailures(t *testing.T) {
	wantOpen := errors.New("open failed")
	restore := replaceOpenFile(func(string, int, os.FileMode) (file, error) { return nil, wantOpen })
	writer, err := New(Options{Path: "app.log", MaxBytes: 10, Mode: 0o600})
	restore()
	if writer != nil || !errors.Is(err, wantOpen) {
		t.Fatalf("New(open failure) = (%v, %v)", writer, err)
	}

	wantStat := errors.New("stat failed")
	statFile := &fakeFile{statErr: wantStat}
	restore = replaceOpenFile(func(string, int, os.FileMode) (file, error) { return statFile, nil })
	writer, err = New(Options{Path: "app.log", MaxBytes: 10, Mode: 0o600})
	restore()
	if writer != nil || !errors.Is(err, wantStat) || !statFile.closed {
		t.Fatalf("New(stat failure) = (%v, %v), closed=%v", writer, err, statFile.closed)
	}

	wantChmod := errors.New("chmod failed")
	chmodFile := &fakeFile{info: fakeInfo{}, chmodErr: wantChmod}
	restore = replaceOpenFile(func(string, int, os.FileMode) (file, error) { return chmodFile, nil })
	writer, err = New(Options{Path: "app.log", MaxBytes: 10, Mode: 0o600})
	restore()
	if writer != nil || !errors.Is(err, wantChmod) || !chmodFile.closed {
		t.Fatalf("New(chmod failure) = (%v, %v), closed=%v", writer, err, chmodFile.closed)
	}
}

func TestRotationFailureReopensCurrentFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	writer := mustNewWriter(t, Options{Path: path, MaxBytes: 3, Backups: 1, Mode: 0o600})
	if _, err := writer.Write([]byte("old")); err != nil {
		t.Fatalf("Write(old) error = %v", err)
	}
	want := errors.New("rename failed")
	oldRename := renameFile
	renameFile = func(string, string) error { return want }
	_, err := writer.Write([]byte("new"))
	renameFile = oldRename
	if !errors.Is(err, want) {
		t.Fatalf("Write(rotation failure) error = %v, want %v", err, want)
	}
	if _, err := writer.Write([]byte("x")); err != nil {
		t.Fatalf("Write after rotation failure error = %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestRotationReportsSyncRemoveAndIntermediateRenameFailures(t *testing.T) {
	t.Run("sync", func(t *testing.T) {
		want := errors.New("sync failed")
		first := &fakeFile{info: fakeInfo{size: 3}, syncErr: want}
		reopened := &fakeFile{info: fakeInfo{size: 3}}
		calls := 0
		restore := replaceOpenFile(func(string, int, os.FileMode) (file, error) {
			calls++
			if calls == 1 {
				return first, nil
			}
			return reopened, nil
		})
		writer, err := New(Options{Path: "app.log", MaxBytes: 3, Backups: 1})
		if err != nil {
			restore()
			t.Fatalf("New() error = %v", err)
		}
		_, err = writer.Write([]byte("x"))
		restore()
		if !errors.Is(err, want) || writer.file != reopened {
			t.Fatalf("Write() error = %v, reopened=%v", err, writer.file == reopened)
		}
	})

	t.Run("remove", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "app.log")
		writer := mustNewWriter(t, Options{Path: path, MaxBytes: 3, Backups: 1})
		if _, err := writer.Write([]byte("old")); err != nil {
			t.Fatalf("Write(old) error = %v", err)
		}
		want := errors.New("remove failed")
		oldRemove := removeFile
		removeFile = func(string) error { return want }
		_, err := writer.Write([]byte("new"))
		removeFile = oldRemove
		if !errors.Is(err, want) {
			t.Fatalf("Write() error = %v, want %v", err, want)
		}
		if err := writer.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	t.Run("intermediate rename", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "app.log")
		if err := os.WriteFile(path+".1", []byte("backup"), 0o600); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
		writer := mustNewWriter(t, Options{Path: path, MaxBytes: 3, Backups: 2})
		if _, err := writer.Write([]byte("old")); err != nil {
			t.Fatalf("Write(old) error = %v", err)
		}
		want := errors.New("rename backup failed")
		oldRename := renameFile
		renameFile = func(from, to string) error {
			if from == path+".1" {
				return want
			}
			return oldRename(from, to)
		}
		_, err := writer.Write([]byte("new"))
		renameFile = oldRename
		if !errors.Is(err, want) {
			t.Fatalf("Write() error = %v, want %v", err, want)
		}
		if err := writer.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})
}

func TestRotationOpenFailureCanBeRetried(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	writer := mustNewWriter(t, Options{Path: path, MaxBytes: 3, Backups: 0})
	if _, err := writer.Write([]byte("old")); err != nil {
		t.Fatalf("Write(old) error = %v", err)
	}
	want := errors.New("open failed")
	oldOpen := openFile
	openFile = func(string, int, os.FileMode) (file, error) { return nil, want }
	_, err := writer.Write([]byte("new"))
	openFile = oldOpen
	if !errors.Is(err, want) {
		t.Fatalf("Write(rotation) error = %v, want %v", err, want)
	}
	if _, err := writer.Write([]byte("retry")); err != nil {
		t.Fatalf("Write(retry) error = %v", err)
	}
	if err := writer.Sync(); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestCloseAfterUnrecoverableRotationOpenFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	writer := mustNewWriter(t, Options{Path: path, MaxBytes: 3, Backups: 0})
	if _, err := writer.Write([]byte("old")); err != nil {
		t.Fatalf("Write(old) error = %v", err)
	}
	oldOpen := openFile
	want := errors.New("open failed")
	openFile = func(string, int, os.FileMode) (file, error) { return nil, want }
	_, _ = writer.Write([]byte("new"))
	if _, err := writer.Write([]byte("retry")); !errors.Is(err, want) {
		t.Fatalf("Write(reopen) error = %v, want %v", err, want)
	}
	if err := writer.Sync(); !errors.Is(err, want) {
		t.Fatalf("Sync(reopen) error = %v, want %v", err, want)
	}
	openFile = oldOpen
	if err := writer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestRotationReportsNewActiveFileOpenFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	writer := mustNewWriter(t, Options{Path: path, MaxBytes: 3, Backups: 1})
	if _, err := writer.Write([]byte("old")); err != nil {
		t.Fatalf("Write(old) error = %v", err)
	}
	want := errors.New("new active file failed")
	oldOpen := openFile
	openFile = func(string, int, os.FileMode) (file, error) { return nil, want }
	_, err := writer.Write([]byte("new"))
	openFile = oldOpen
	if !errors.Is(err, want) {
		t.Fatalf("Write(rotation) error = %v, want %v", err, want)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestSyncAndCloseJoinFileErrors(t *testing.T) {
	wantSync := errors.New("sync failed")
	wantClose := errors.New("close failed")
	fake := &fakeFile{info: fakeInfo{}, syncErr: wantSync, closeErr: wantClose}
	restore := replaceOpenFile(func(string, int, os.FileMode) (file, error) { return fake, nil })
	writer, err := New(Options{Path: "app.log", MaxBytes: 10, Mode: 0o600})
	restore()
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := writer.Sync(); !errors.Is(err, wantSync) {
		t.Fatalf("Sync() error = %v, want sync failure", err)
	}
	if err := writer.Close(); !errors.Is(err, wantSync) || !errors.Is(err, wantClose) {
		t.Fatalf("Close() error = %v, want joined errors", err)
	}
}

func mustNewWriter(t *testing.T, options Options) *Writer {
	t.Helper()
	writer, err := New(options)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return writer
}

func assertFile(t *testing.T, path, want string) {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}
	if string(contents) != want {
		t.Fatalf("file %q = %q, want %q", path, contents, want)
	}
}

type fakeFile struct {
	info     fakeInfo
	statErr  error
	chmodErr error
	writeN   int
	writeErr error
	syncErr  error
	closeErr error
	closed   bool
}

func (file *fakeFile) Write(value []byte) (int, error) {
	if file.writeN == 0 && file.writeErr == nil {
		return len(value), nil
	}
	return file.writeN, file.writeErr
}
func (file *fakeFile) Stat() (os.FileInfo, error) { return file.info, file.statErr }
func (file *fakeFile) Chmod(os.FileMode) error    { return file.chmodErr }
func (file *fakeFile) Sync() error                { return file.syncErr }
func (file *fakeFile) Close() error               { file.closed = true; return file.closeErr }

type fakeInfo struct{ size int64 }

func (info fakeInfo) Name() string       { return "app.log" }
func (info fakeInfo) Size() int64        { return info.size }
func (info fakeInfo) Mode() os.FileMode  { return 0o600 }
func (info fakeInfo) ModTime() time.Time { return time.Time{} }
func (info fakeInfo) IsDir() bool        { return false }
func (info fakeInfo) Sys() any           { return nil }

func replaceOpenFile(replacement func(string, int, os.FileMode) (file, error)) func() {
	old := openFile
	openFile = replacement
	return func() { openFile = old }
}
