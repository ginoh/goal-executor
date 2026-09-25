// Package buildinput fixes local build inputs before they are identified and built.
package buildinput

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/moby/patternmatcher"
	"github.com/moby/patternmatcher/ignorefile"
)

type Snapshot struct {
	Dir        string
	Dockerfile string
	ID         string
}

// Prepare copies a local context once. Caller owns the returned temporary directory.
func Prepare(goalPath, contextPath, dockerfile string) (snapshot Snapshot, err error) {
	if filepath.IsAbs(contextPath) || contextPath == "" || filepath.IsAbs(dockerfile) || dockerfile == "" {
		return snapshot, errors.New("build paths must be relative")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(goalPath), contextPath))
	info, err := os.Lstat(root)
	if err != nil {
		return snapshot, fmt.Errorf("inspect build context: %w", err)
	}
	if !info.IsDir() {
		return snapshot, errors.New("build context must be a directory")
	}
	dockerfile = filepath.Clean(dockerfile)
	if dockerfile == ".." || strings.HasPrefix(dockerfile, ".."+string(filepath.Separator)) || dockerfile == "." {
		return snapshot, errors.New("Dockerfile must be inside build context")
	}
	fileInfo, err := os.Lstat(filepath.Join(root, dockerfile))
	if err != nil {
		return snapshot, fmt.Errorf("inspect Dockerfile: %w", err)
	}
	if !fileInfo.Mode().IsRegular() {
		return snapshot, errors.New("Dockerfile must be a regular file")
	}
	ignoreName := dockerfile + ".dockerignore"
	if _, err := os.Lstat(filepath.Join(root, ignoreName)); errors.Is(err, os.ErrNotExist) {
		ignoreName = ".dockerignore"
	} else if err != nil {
		return snapshot, err
	}
	var patterns []string
	if f, openErr := os.Open(filepath.Join(root, ignoreName)); openErr == nil {
		patterns, err = ignorefile.ReadAll(f)
		closeErr := f.Close()
		if err != nil {
			return snapshot, fmt.Errorf("read ignore file: %w", err)
		}
		if closeErr != nil {
			return snapshot, closeErr
		}
	} else if !errors.Is(openErr, os.ErrNotExist) {
		return snapshot, openErr
	} else {
		ignoreName = ""
	}
	matcher, err := patternmatcher.New(patterns)
	if err != nil {
		return snapshot, fmt.Errorf("invalid ignore rule: %w", err)
	}
	temp, err := os.MkdirTemp("", "goal-executor-build-")
	if err != nil {
		return snapshot, err
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(temp)
		}
	}()
	err = filepath.WalkDir(root, func(full string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, full)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		excluded, err := matcher.MatchesOrParentMatches(filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		if excluded && rel != dockerfile && rel != ignoreName {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return os.MkdirAll(filepath.Join(temp, rel), info.Mode().Perm())
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported build input %s", rel)
		}
		if err := os.MkdirAll(filepath.Dir(filepath.Join(temp, rel)), 0700); err != nil {
			return err
		}
		return copyFile(full, filepath.Join(temp, rel), info.Mode().Perm())
	})
	if err != nil {
		return snapshot, fmt.Errorf("copy build input: %w", err)
	}
	if _, err := os.Stat(filepath.Join(temp, dockerfile)); err != nil {
		return snapshot, fmt.Errorf("Dockerfile was not copied: %w", err)
	}
	h := sha256.New()
	// Selecting a different Dockerfile changes the build even when the copied tree is identical.
	_, _ = fmt.Fprintf(h, "dockerfile\x00%s\x00", filepath.ToSlash(dockerfile))
	err = filepath.WalkDir(temp, func(full string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(temp, full)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		kind := "file"
		if entry.IsDir() {
			kind = "dir"
		}
		_, _ = fmt.Fprintf(h, "%s\x00%s\x00%o\x00", kind, filepath.ToSlash(rel), info.Mode().Perm())
		if !entry.IsDir() {
			f, err := os.Open(full)
			if err != nil {
				return err
			}
			_, err = io.Copy(h, f)
			closeErr := f.Close()
			if err != nil {
				return err
			}
			if closeErr != nil {
				return closeErr
			}
		}
		_, _ = h.Write([]byte{0})
		return nil
	})
	if err != nil {
		return snapshot, fmt.Errorf("identify build input: %w", err)
	}
	return Snapshot{Dir: temp, Dockerfile: dockerfile, ID: hex.EncodeToString(h.Sum(nil))}, nil
}

func copyFile(from, to string, mode fs.FileMode) error {
	src, err := os.Open(from)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(dst, src)
	closeErr := dst.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}
