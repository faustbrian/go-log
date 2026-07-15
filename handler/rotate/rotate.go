// Package rotate provides an optional bounded local-file writer for use with
// the standard log/slog JSON and text handlers.
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
	// ErrInvalidMode is returned when Mode contains non-permission bits.
	ErrInvalidMode = errors.New("rotate: invalid file mode")
	// ErrClosed is returned by Write and Sync after Close.
	ErrClosed = errors.New("rotate: writer closed")
)

// Options configures local file rotation.
type Options struct {
	// Path is the active log file.
	Path string
	// MaxBytes is the active file size that triggers rotation before the next
	// atomic write. A single larger write is never split.
	MaxBytes int64
	// Backups is the number of numbered old files to retain. Zero truncates the
	// active file on rotation.
	Backups int
	// Mode is enforced on every active file. Zero defaults to 0600.
	Mode os.FileMode
}

// Stats is a point-in-time writer snapshot.
type Stats struct {
	Bytes     int64
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
		return os.OpenFile(name, flag, perm)
	}
	renameFile = os.Rename
	removeFile = os.Remove
)

// Writer serializes writes and rotates files before a write would exceed the
// configured limit. Writer implements io.WriteCloser and is safe for
// concurrent use.
type Writer struct {
	mu        sync.Mutex
	options   Options
	file      file
	size      int64
	rotations uint64
	closed    bool
}

// New opens or creates a rotating writer and enforces its configured mode.
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
	if writer.size > 0 && writer.size+int64(len(p)) > writer.options.MaxBytes {
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

// Sync commits the active file's contents to stable storage.
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

// Close syncs and closes the active file. Repeated calls succeed.
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

func (writer *Writer) rotate() error {
	closeErr := errors.Join(writer.file.Sync(), writer.file.Close())
	writer.file = nil
	if closeErr != nil {
		return writer.recoverRotation(closeErr)
	}
	if writer.options.Backups == 0 {
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
	for index := writer.options.Backups - 1; index >= 1; index-- {
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
	flags := os.O_CREATE | os.O_WRONLY
	if truncate {
		flags |= os.O_TRUNC
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
	if err := opened.Chmod(writer.options.Mode); err != nil {
		return errors.Join(err, opened.Close())
	}
	writer.file = opened
	writer.size = info.Size()

	return nil
}

func backupName(path string, index int) string {
	return path + "." + strconv.Itoa(index)
}
