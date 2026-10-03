package rotate

import (
	"errors"
	"os"
	"testing"
)

func TestOrdinaryZeroBackupRemoveFailureReopensCurrentFile(t *testing.T) {
	want := errors.New("active remove failed")
	first, reopened := &fakeFile{info: fakeInfo{size: 1}}, &fakeFile{info: fakeInfo{size: 1}}
	opens := 0
	t.Cleanup(replaceOpenFile(func(string, int, os.FileMode) (file, error) {
		opens++
		if opens == 1 {
			return first, nil
		}
		return reopened, nil
	}))
	oldRemove := removeFile
	removeFile = func(string) error { return want }
	t.Cleanup(func() { removeFile = oldRemove })
	writer := mustNewWriter(t, Options{Path: "owned.log", MaxBytes: 1})
	t.Cleanup(func() {
		if err := writer.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	if n, err := writer.Write([]byte("y")); n != 0 || !errors.Is(err, want) {
		t.Fatalf("failed removal Write = %d, %v", n, err)
	}
	if !first.closed || writer.file != reopened || reopened.closed || writer.Stats() != (Stats{Bytes: 1}) {
		t.Fatalf("failed removal changed recoverable state: firstClosed=%t reopenedClosed=%t stats=%#v", first.closed, reopened.closed, writer.Stats())
	}
	if err := writer.Sync(); err != nil {
		t.Fatalf("recovered file Sync: %v", err)
	}
}

func TestOrdinaryZeroBackupReopenFailureKeepsRotationUncountedAndRetryable(t *testing.T) {
	want := errors.New("replacement open failed")
	first, replacement := &fakeFile{info: fakeInfo{size: 1}}, &fakeFile{info: fakeInfo{}}
	var opens int
	fail := true
	t.Cleanup(replaceOpenFile(func(string, int, os.FileMode) (file, error) {
		opens++
		if opens == 1 {
			return first, nil
		}
		if fail {
			return nil, want
		}
		return replacement, nil
	}))
	oldRemove := removeFile
	removals := 0
	removeFile = func(string) error { removals++; return nil }
	t.Cleanup(func() { removeFile = oldRemove })
	writer := mustNewWriter(t, Options{Path: "owned.log", MaxBytes: 1})
	t.Cleanup(func() {
		if err := writer.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	if n, err := writer.Write([]byte("y")); n != 0 || !errors.Is(err, want) {
		t.Fatalf("failed replacement Write = %d, %v", n, err)
	}
	if !first.closed || writer.file != nil || removals != 1 || writer.Stats() != (Stats{Bytes: 1}) {
		t.Fatalf("failed replacement state: closed=%t active=%v removals=%d stats=%#v", first.closed, writer.file, removals, writer.Stats())
	}
	fail = false
	if n, err := writer.Write([]byte("y")); n != 1 || err != nil {
		t.Fatalf("retry Write = %d, %v", n, err)
	}
	if writer.Stats() != (Stats{Bytes: 1}) || replacement.closed {
		t.Fatalf("retry state: stats=%#v closed=%t", writer.Stats(), replacement.closed)
	}
}

func TestOrdinaryPostOpenPathInfoFailureClosesRejectedFile(t *testing.T) {
	want := errors.New("pathname info failed")
	opened := &fakeFile{info: fakeInfo{}}
	t.Cleanup(replaceOpenFile(func(string, int, os.FileMode) (file, error) { return opened, nil }))
	var calls int
	lstatFile = func(string) (os.FileInfo, error) {
		calls++
		if calls == 2 {
			return nil, want
		}
		return fakeInfo{}, nil
	}
	writer, err := New(Options{Path: "owned.log", MaxBytes: 1})
	if writer != nil || !errors.Is(err, want) || !errors.Is(err, ErrUnsafePath) || !opened.closed {
		t.Fatalf("post-open rejection: writer=%v error=%v closed=%t", writer, err, opened.closed)
	}
}

func TestOrdinaryRotationStatFailurePreservesActiveWriterAndAccounting(t *testing.T) {
	want := errors.New("active descriptor stat failed")
	opened := &fakeFile{info: fakeInfo{size: 1}}
	t.Cleanup(replaceOpenFile(func(string, int, os.FileMode) (file, error) { return opened, nil }))
	writer := mustNewWriter(t, Options{Path: "owned.log", MaxBytes: 1, Backups: 1})
	t.Cleanup(func() {
		if err := writer.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	opened.statErr = want
	if n, err := writer.Write([]byte("y")); n != 0 || !errors.Is(err, want) {
		t.Fatalf("descriptor-stat rejection Write = %d, %v", n, err)
	}
	if opened.closed || writer.file != opened || writer.Stats() != (Stats{Bytes: 1}) {
		t.Fatalf("descriptor-stat rejection changed ownership/accounting: closed=%t stats=%#v", opened.closed, writer.Stats())
	}
	opened.statErr = nil
	if err := writer.Sync(); err != nil {
		t.Fatalf("still-owned active writer Sync: %v", err)
	}
}
