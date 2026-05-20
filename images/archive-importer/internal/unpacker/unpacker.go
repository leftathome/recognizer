// Package unpacker extracts archives into a target directory.
// V1 supports zip; tar.gz and 7z are reserved.
package unpacker

// Unpacker extracts an archive at srcPath into dstDir.
type Unpacker interface {
	Unpack(srcPath, dstDir string) error
}
