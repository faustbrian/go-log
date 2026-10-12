// Package rotate provides an optional bounded local-file writer for use with
// the standard log/slog JSON and text handlers. Start with New and own Close;
// Write/Sync/Close serialize filesystem work with no context cancellation.
// Checked leaf identities are not a directory sandbox: callers must control
// parent directories, concurrent path mutation and backup count. No worker,
// network transport, redaction or crash-atomic rotation protocol is owned.
package rotate

import (
	"errors"
	"io"
	"os"
	"strconv"
	"sync"
)

var (
	// ErrInvalidPath is returned when no file path is configured.
	ErrInvalidPath = errors.New("rotate: path is required")
	// ErrInvalidMaxBytes is returned when the size limit is not positive.
	ErrInvalidMaxBytes = errors.New("rotate: max bytes must be greater than zero")
	// ErrInvalidBackups is returned when the backup count is negative.
	ErrInvalidBackups = errors.New("rotate: backups must not be negative")
	// ErrInvalidMode is returned when Mode contains file-type bits.
	ErrInvalidMode = errors.New("rotate: invalid file mode")
	// ErrClosed is returned by Write and Sync after Close.
	ErrClosed = errors.New("rotate: writer closed")
	// ErrUnsafePath reports a symlink or non-regular active or backup path.
	ErrUnsafePath = errors.New("rotate: unsafe file path")
)

// Options configures local file rotation.
type Options struct {
	// Path is the active log file. Symbolic links and non-regular active or
	// numbered backup paths are rejected. Parent directories must already exist
	// and remain trusted; errors may disclose this caller-configured path.
	Path string
	// MaxBytes is the active file size that triggers rotation before the next
	// serialized write. A single larger write is never split by rotation, though
	// underlying I/O can return a partial count/error. This is not a byte quota.
	MaxBytes int64
	// Backups is the number of numbered old files to retain. Zero truncates the
	// active path to a newly created empty file on rotation.
	Backups int
	// Mode permissions are enforced on every active file. Zero defaults to 0600;
	// file-type bits are rejected and other non-permission flags discarded.
	Mode os.FileMode
}

// Stats is a point-in-time writer snapshot.
type Stats struct {
	// Bytes is tracked active-file size, including successful partial writes.
	// It is not cumulative bytes or a fresh stat after external file mutation.
	Bytes int64
	// Rotations counts completed new-active-file transitions, not failed attempts.
	Rotations uint64
}

type file interface {
	io.Writer
	Stat() (os.FileInfo, error)
	Chmod(os.FileMode) error
	Sync() error
	Close() error
}

var (
	openFile = func(name string, flag int, perm os.FileMode) (file, error) {
		// #nosec G304 -- Path is explicit caller configuration; this writer
		// rejects non-regular and symlink leaf paths before and after open.
		return os.OpenFile(name, flag, perm)
	}
	renameFile = os.Rename
	removeFile = os.Remove
	lstatFile  = os.Lstat
	sameFile   = os.SameFile
)

// Writer serializes writes and rotates files before a write would exceed the
// configured limit. Writer implements io.WriteCloser and is safe for
// concurrent use. Use New; zero Writer has no valid configured path. The mutex
// covers filesystem calls, so slow I/O also delays concurrent Close/Stats.
// Caller owns final Close and must not share path mutation with another writer.
type Writer struct {
	mu        sync.Mutex
	options   Options
	file      file
	size      int64
	rotations uint64
	closed    bool
}

// New validates options, appends an existing regular file or creates one and
// enforces its mode before publishing a Writer. Nil is returned on failure;
// filesystem and rejected-file cleanup errors may be joined. No parent directory
// is created. Backups has no upper cap; caller configuration bounds rotation work.
func New(options Options) (*Writer, error) {
	if options.Path == "" {
		return nil, ErrInvalidPath
	}
	if options.MaxBytes <= 0 {
		return nil, ErrInvalidMaxBytes
	}
	if options.Backups < 0 {
		return nil, ErrInvalidBackups
	}
	if options.Mode == 0 {
		options.Mode = 0o600
	}
	if options.Mode&os.ModeType != 0 {
		return nil, ErrInvalidMode
	}
	options.Mode = options.Mode.Perm()
	writer := &Writer{options: options}
	if err := writer.open(false); err != nil {
		return nil, err
	}

	return writer, nil
}

// Write writes p atomically relative to other Writer calls. It rotates first
// when an existing non-empty file plus p would exceed MaxBytes.
func (writer *Writer) Write(p []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.closed {
		return 0, ErrClosed
	}
	if writer.file == nil {
		if err := writer.open(false); err != nil {
			return 0, err
		}
	}
	if wouldExceedLimit(writer.size, len(p), writer.options.MaxBytes) {
		if err := writer.rotate(); err != nil {
			return 0, err
		}
	}

	written, err := writer.file.Write(p)
	writer.size += int64(written)
	if err == nil && written != len(p) {
		err = io.ErrShortWrite
	}

	return written, err
}

func wouldExceedLimit(size int64, writeBytes int, maximum int64) bool {
	return size > 0 && writeBytes > 0 && int64(writeBytes) > maximum-size
}

// Sync requests synchronization of the active file through its filesystem.
// It returns ErrClosed after Close and may retry opening after rotation failure.
// It does not sync parent directories or make the rename sequence crash-atomic.
func (writer *Writer) Sync() error {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.closed {
		return ErrClosed
	}
	if writer.file == nil {
		if err := writer.open(false); err != nil {
			return err
		}
	}

	return writer.file.Sync()
}

// Close marks the writer terminal, then attempts both file Sync and Close and
// joins their errors. Repeated calls succeed even after an earlier close error;
// Write/Sync thereafter return ErrClosed. No underlying operation is preempted.
func (writer *Writer) Close() error {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.closed {
		return nil
	}
	writer.closed = true
	if writer.file == nil {
		return nil
	}
	err := errors.Join(writer.file.Sync(), writer.file.Close())
	writer.file = nil

	return err
}

// Stats returns the active file size and successful rotation count.
func (writer *Writer) Stats() Stats {
	writer.mu.Lock()
	defer writer.mu.Unlock()

	return Stats{Bytes: writer.size, Rotations: writer.rotations}
}

// Rotation is deliberately a filesystem sequence, not a rollback transaction.
// Validate identities before removing/renaming; a failure can leave shifted
// backups. Reopen recovery preserves a usable active path where possible and
// joins errors, never counts an incomplete transition as a successful rotation.
func (writer *Writer) rotate() error {
	if err := writer.validateRotationPaths(); err != nil {
		return err
	}
	closeErr := errors.Join(writer.file.Sync(), writer.file.Close())
	writer.file = nil
	if closeErr != nil {
		return writer.recoverRotation(closeErr)
	}
	if writer.options.Backups == 0 {
		if err := removeFile(writer.options.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return writer.recoverRotation(err)
		}
		if err := writer.open(true); err != nil {
			return err
		}
		writer.rotations++

		return nil
	}

	oldest := backupName(writer.options.Path, writer.options.Backups)
	if err := removeFile(oldest); err != nil && !errors.Is(err, os.ErrNotExist) {
		return writer.recoverRotation(err)
	}
	for offset := range writer.options.Backups - 1 {
		index := writer.options.Backups - 1 - offset
		from := backupName(writer.options.Path, index)
		to := backupName(writer.options.Path, index+1)
		if err := renameFile(from, to); err != nil && !errors.Is(err, os.ErrNotExist) {
			return writer.recoverRotation(err)
		}
	}
	if err := renameFile(writer.options.Path, backupName(writer.options.Path, 1)); err != nil {
		return writer.recoverRotation(err)
	}
	if err := writer.open(true); err != nil {
		return err
	}
	writer.rotations++

	return nil
}

func (writer *Writer) recoverRotation(rotationErr error) error {
	return errors.Join(rotationErr, writer.open(false))
}

func (writer *Writer) open(truncate bool) error {
	before, err := regularPathInfo(writer.options.Path, true)
	if err != nil {
		return err
	}
	flags := os.O_CREATE | os.O_WRONLY
	if truncate {
		flags |= os.O_EXCL
	} else {
		flags |= os.O_APPEND
	}
	opened, err := openFile(writer.options.Path, flags, writer.options.Mode)
	if err != nil {
		return err
	}
	info, err := opened.Stat()
	if err != nil {
		return errors.Join(err, opened.Close())
	}
	after, err := regularPathInfo(writer.options.Path, false)
	if err != nil || !sameFile(info, after) || before != nil && !sameFile(before, info) {
		return errors.Join(ErrUnsafePath, err, opened.Close())
	}
	if err := opened.Chmod(writer.options.Mode); err != nil {
		return errors.Join(err, opened.Close())
	}
	writer.file = opened
	writer.size = info.Size()

	return nil
}

func (writer *Writer) validateRotationPaths() error {
	active, err := regularPathInfo(writer.options.Path, false)
	if err != nil {
		return err
	}
	opened, err := writer.file.Stat()
	if err != nil {
		return err
	}
	if !sameFile(active, opened) {
		return ErrUnsafePath
	}
	for offset := range writer.options.Backups {
		if _, err := regularPathInfo(backupName(writer.options.Path, offset+1), true); err != nil {
			return err
		}
	}

	return nil
}

func regularPathInfo(path string, allowMissing bool) (os.FileInfo, error) {
	info, err := lstatFile(path)
	if err != nil {
		if allowMissing && errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, ErrUnsafePath
	}

	return info, nil
}

func backupName(path string, index int) string {
	return path + "." + strconv.Itoa(index)
}
