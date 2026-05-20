package unpacker

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// UnpackZip extracts srcZip into dstDir.
//
// Path-traversal safety: archive/zip's NewReader returns ErrInsecurePath
// for entries that aren't filepath.IsLocal. We reject the archive entirely
// in that case (treating ErrInsecurePath as fatal, never opting into
// GODEBUG=zipinsecurepath=1).
func UnpackZip(srcZip, dstDir string) error {
	r, err := zip.OpenReader(srcZip)
	if err != nil {
		return err
	}
	defer r.Close()
	for _, f := range r.File {
		if !filepath.IsLocal(f.Name) || strings.Contains(f.Name, "..") {
			return fmt.Errorf("unpacker: refusing insecure entry %q: %w", f.Name, zip.ErrInsecurePath)
		}
		dst := filepath.Join(dstDir, f.Name)
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(dst, 0755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
			return err
		}
		w, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
		if err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			w.Close()
			return err
		}
		_, err = io.Copy(w, rc)
		rc.Close()
		if cerr := w.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
	}
	return nil
}
