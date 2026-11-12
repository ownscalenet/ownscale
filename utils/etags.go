package utils

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
)

// ContentETags returns the ETag of every file in fsys, keyed by its path.
// Embedded files have no modification time, so without an ETag
// http.FileServer can't answer revalidations with 304.
func ContentETags(fsys fs.FS) (map[string]string, error) {
	etags := map[string]string{}
	err := fs.WalkDir(fsys, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		body, err := fs.ReadFile(fsys, name)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(body)
		etags[name] = `"` + hex.EncodeToString(sum[:8]) + `"`
		return nil
	})
	return etags, err
}
