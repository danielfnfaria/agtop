package discovery

import (
	"encoding/json"
	"errors"
	"path/filepath"

	"github.com/danielfnfaria/agtop/internal/domain"
	"github.com/danielfnfaria/agtop/internal/errno"
)

// Files that the status line hook writes under <claudeDir>/agtop/. Same care as the other
// readers: the path stays inside the profile directory (symlinks resolved), the size is capped
// and the content strictly validated.

// UsageDir is <claudeDir>/agtop, where the usage and context files live.
func UsageDir(claudeDir string) string { return filepath.Join(claudeDir, domain.UsageDirName) }

// ReadKind tells how a read ended.
type ReadKind string

const (
	// ReadOK: the file was read and is valid.
	ReadOK ReadKind = "ok"
	// ReadMissing: no file (or not inside the profile directory).
	ReadMissing ReadKind = "missing"
	// ReadError: unreadable or invalid; Code is a short code, never file content: TOO_LARGE,
	// INVALID_JSON, INVALID, or the errno name of the read.
	ReadError ReadKind = "error"
)

// FileRead is the result of reading one of the files.
type FileRead[T any] struct {
	Kind  ReadKind
	Value T
	// Sig is "size:mtimeMs" ("" if the fs does not report it).
	Sig  string
	Code string
}

// ReadUsageFile reads and validates the usage file of a profile.
func ReadUsageFile(fs FS, claudeDir string) FileRead[domain.UsageFile] {
	return readAgtopFile(fs, claudeDir, domain.UsageFileName, domain.MaxUsageFileBytes, domain.ParseUsageFile)
}

// ReadContextFile reads and validates the context file of a profile.
func ReadContextFile(fs FS, claudeDir string) FileRead[domain.ContextFile] {
	return readAgtopFile(fs, claudeDir, domain.ContextFileName, domain.MaxContextFileBytes, domain.ParseContextFile)
}

// UsageSignature is the signature of the usage file, to skip re-reading an unchanged file;
// false if missing or not reported.
func UsageSignature(fs FS, claudeDir string) (string, bool) {
	return fileSignature(fs, claudeDir, domain.UsageFileName)
}

// ContextSignature is UsageSignature for the context file.
func ContextSignature(fs FS, claudeDir string) (string, bool) {
	return fileSignature(fs, claudeDir, domain.ContextFileName)
}

func fileSignature(fs FS, claudeDir, name string) (string, bool) {
	file, ok := containedFile(fs, claudeDir, name)
	if !ok {
		return "", false
	}
	return StatSignature(fs, file)
}

func containedFile(fs FS, claudeDir, name string) (string, bool) {
	root, ok := NewContainmentRoot(fs, claudeDir)
	if !ok {
		return "", false
	}
	return ResolveContained(fs, root, filepath.Join(UsageDir(claudeDir), name))
}

func readAgtopFile[T any](fs FS, claudeDir, name string, maxBytes int, parse func(any) (T, bool)) FileRead[T] {
	file, ok := containedFile(fs, claudeDir, name)
	if !ok {
		return FileRead[T]{Kind: ReadMissing}
	}
	sig, _ := StatSignature(fs, file)
	raw, err := fs.ReadFile(file, maxBytes)
	if err != nil {
		if errors.Is(err, ErrFileTooLarge) {
			return FileRead[T]{Kind: ReadError, Code: "TOO_LARGE"}
		}
		code := errno.Code(err)
		switch code {
		case "ENOENT":
			return FileRead[T]{Kind: ReadMissing}
		case "":
			code = "UNKNOWN"
		}
		return FileRead[T]{Kind: ReadError, Code: code}
	}
	var decoded any
	if json.Unmarshal([]byte(raw), &decoded) != nil {
		return FileRead[T]{Kind: ReadError, Code: "INVALID_JSON"}
	}
	value, ok := parse(decoded)
	if !ok {
		return FileRead[T]{Kind: ReadError, Code: "INVALID"}
	}
	return FileRead[T]{Kind: ReadOK, Value: value, Sig: sig}
}
