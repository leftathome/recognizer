package delivery

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// TarSubtree writes a plain (non-gzipped) tarball of dirPath
// (recursive) into a freshly-created temp file under tmpDir, returning
// the temp file's path, body sha256, and body size. Callers MUST
// os.Remove the path when done; the file is deliberately not auto-
// deleted because the caller streams it to glovebox via tus PATCH and
// only knows it's safe to remove after the upload succeeds.
//
// NOTE on format: glovebox's docs/handoffs/recognizer-archive-delivery.md
// §3b calls these "gzipped tarball" but the actual finalize code
// (internal/ingest/archives/finalize.go) feeds the body straight into
// tar.NewReader with no gzip wrapper. Real upload of a .tar.gz
// returned `untar: tar read: archive/tar: invalid tar header` at
// finalize on 2026-05-23. Filed as glovebox-m696. Until that's
// resolved we ship plain .tar (the simpler interpretation; CPU win
// per delivery vs the gzip pass anyway).
//
// The tarball includes:
//   - Regular files (mode preserved to the low 9 bits).
//   - Directory entries (preserved for empty leaf dirs).
//   - Symlinks: REJECTED. Recognizer's matcher should never produce
//     symlinks (spec 03 unpacker bans them), but if a future provider
//     surfaces them the tar uploader fails closed so we don't trip
//     glovebox's tar_unsafe_entry guard mid-upload.
//
// Entry names are relative to dirPath (so untar at glovebox lands the
// contents inside <archive_id>/extracted/, not nested under the
// original dir's name). This matches spec 13 §4.7 expectations.
func TarSubtree(dirPath, tmpDir string) (path, sha256hex string, size int64, err error) {
	tmpFile, err := os.CreateTemp(tmpDir, "subtree-*.tar")
	if err != nil {
		return "", "", 0, fmt.Errorf("create temp: %w", err)
	}
	cleanupOnErr := true
	defer func() {
		if cleanupOnErr {
			tmpFile.Close()
			os.Remove(tmpFile.Name())
		}
	}()

	hasher := sha256.New()
	counter := &countingWriter{}
	// Tee writes to (a) the temp file we'll later upload, (b) the
	// sha256 hasher, (c) the size counter. Each PATCH chunk later
	// reads back from the temp file.
	mw := io.MultiWriter(tmpFile, hasher, counter)

	tw := tar.NewWriter(mw)

	walkErr := filepath.Walk(dirPath, func(p string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(dirPath, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("tar: symlink %s not permitted (glovebox would reject as tar_unsafe_entry)", rel)
		}
		hdr := &tar.Header{
			Name:    filepath.ToSlash(rel),
			Mode:    int64(info.Mode().Perm()),
			ModTime: info.ModTime(),
			Size:    0,
		}
		if info.IsDir() {
			hdr.Typeflag = tar.TypeDir
			hdr.Name += "/"
			return tw.WriteHeader(hdr)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("tar: %s is not a regular file (mode=%v)", rel, info.Mode())
		}
		hdr.Typeflag = tar.TypeReg
		hdr.Size = info.Size()
		if err := tw.WriteHeader(hdr); err != nil {
			return fmt.Errorf("tar header %s: %w", rel, err)
		}
		f, err := os.Open(p)
		if err != nil {
			return fmt.Errorf("open %s: %w", rel, err)
		}
		_, err = io.Copy(tw, f)
		f.Close()
		if err != nil {
			return fmt.Errorf("copy %s: %w", rel, err)
		}
		return nil
	})
	if walkErr != nil {
		return "", "", 0, walkErr
	}
	if err := tw.Close(); err != nil {
		return "", "", 0, fmt.Errorf("close tar: %w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		return "", "", 0, fmt.Errorf("sync temp: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return "", "", 0, fmt.Errorf("close temp: %w", err)
	}
	cleanupOnErr = false
	return tmpFile.Name(), hex.EncodeToString(hasher.Sum(nil)), counter.n, nil
}

// HashFile computes sha256 + size of a regular file in a single pass.
// Used for raw deliveries (mbox) where the upload body is the file
// as-is.
func HashFile(path string) (sha256hex string, size int64, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

type countingWriter struct{ n int64 }

func (c *countingWriter) Write(p []byte) (int, error) {
	c.n += int64(len(p))
	return len(p), nil
}
