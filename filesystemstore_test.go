package filesapi

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFileSystemStoreContract(t *testing.T) {
	fs, err := NewFileStore(BlockFSConfig{})
	if err != nil {
		t.Fatal(err)
	}

	setup := func() func() {
		return func() {}
	}

	RunFileStoreContractTests(t, fs, setup)
}

func TestFileSystemStorePathTraversal(t *testing.T) {
	dir := t.TempDir()
	fs, err := NewFileStore(BlockFSConfig{BaseDir: dir})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("PathTraversalWithDotDot", func(t *testing.T) {
		traversalPath := filepath.Join(dir, "..", "..", "etc", "passwd")
		_, err := fs.GetObjectInfo(PathConfig{Path: traversalPath})
		if err == nil {
			t.Fatal("Expected error for path traversal attempt")
		}
	})

	t.Run("PathTraversalInPutObject", func(t *testing.T) {
		traversalPath := filepath.Join(dir, "..", "outside.txt")
		poi := PutObjectInput{
			Source:  ObjectSource{Data: []byte("test")},
			Dest:    PathConfig{Path: traversalPath},
			Context: context.Background(),
		}
		_, err := fs.PutObject(poi)
		if err != nil {
			t.Logf("PutObject with traversal path returned error: %v", err)
		}
	})
}

func TestFileSystemStorePermissions(t *testing.T) {
	dir := t.TempDir()
	fs, err := NewFileStore(BlockFSConfig{BaseDir: dir})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("ReadOnlyDirectory", func(t *testing.T) {
		roDir := filepath.Join(dir, "readonly")
		if err := os.Mkdir(roDir, 0555); err != nil {
			t.Fatal(err)
		}

		testFile := filepath.Join(roDir, "test.txt")
		err := os.WriteFile(testFile, []byte("test"), 0644)
		if err != nil {
			t.Skip("Cannot write to read-only directory on this system")
		}

		_, err = fs.GetObjectInfo(PathConfig{Path: testFile})
		if err != nil {
			t.Logf("GetObjectInfo on read-only file: %v", err)
		}

		_, err = fs.ListDir(ListDirInput{Path: PathConfig{Path: roDir}})
		if err != nil {
			t.Logf("ListDir on read-only dir: %v", err)
		}
	})
}

func TestFileSystemStoreSymlinks(t *testing.T) {
	dir := t.TempDir()
	fs, err := NewFileStore(BlockFSConfig{BaseDir: dir})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("SymlinkToFile", func(t *testing.T) {
		targetFile := filepath.Join(dir, "target.txt")
		if err := os.WriteFile(targetFile, []byte("target content"), 0644); err != nil {
			t.Fatal(err)
		}

		linkPath := filepath.Join(dir, "link.txt")
		if err := os.Symlink(targetFile, linkPath); err != nil {
			t.Fatal(err)
		}

		info, err := fs.GetObjectInfo(PathConfig{Path: linkPath})
		if err != nil {
			t.Fatalf("GetObjectInfo on symlink failed: %v", err)
		}

		if info.IsDir() {
			t.Fatal("Symlink to file should not be a directory")
		}
	})

	t.Run("SymlinkToDirectory", func(t *testing.T) {
		targetDir := filepath.Join(dir, "targetdir")
		if err := os.Mkdir(targetDir, 0755); err != nil {
			t.Fatal(err)
		}

		linkPath := filepath.Join(dir, "linkdir")
		if err := os.Symlink(targetDir, linkPath); err != nil {
			t.Fatal(err)
		}

		info, err := fs.GetObjectInfo(PathConfig{Path: linkPath})
		if err != nil {
			t.Fatalf("GetObjectInfo on symlink to dir failed: %v", err)
		}

		if !info.IsDir() {
			t.Fatal("Symlink to directory should be a directory")
		}
	})

	t.Run("BrokenSymlink", func(t *testing.T) {
		linkPath := filepath.Join(dir, "broken_link")
		if err := os.Symlink("/nonexistent/target", linkPath); err != nil {
			t.Fatal(err)
		}

		_, err := fs.GetObjectInfo(PathConfig{Path: linkPath})
		if err == nil {
			t.Fatal("Expected error for broken symlink")
		}
	})
}

func TestFileSystemStoreWalkContext(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 5; i++ {
		if err := os.WriteFile(filepath.Join(dir, string(rune('a'+i))), []byte("test"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	fs, err := NewFileStore(BlockFSConfig{})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("WalkWithCancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		wi := WalkInput{
			Path:    PathConfig{Path: dir, Context: ctx},
			Context: ctx,
		}

		err := fs.Walk(wi, func(path string, info os.FileInfo) error {
			return nil
		})

		if err != nil {
			t.Logf("Walk with cancelled context returned error: %v", err)
		}
	})
}

func TestFileSystemStoreFileNotFound(t *testing.T) {
	fs, err := NewFileStore(BlockFSConfig{})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("GetObjectInfoReturnsFileNotFoundError", func(t *testing.T) {
		_, err := fs.GetObjectInfo(PathConfig{Path: "/nonexistent/path"})
		if err == nil {
			t.Fatal("Expected error for non-existent path")
		}
		var notFound *FileNotFoundError
		if !errors.As(err, &notFound) {
			t.Fatalf("Expected FileNotFoundError, got %T", err)
		}
	})

	t.Run("GetObjectReturnsFileNotFoundError", func(t *testing.T) {
		_, err := fs.GetObject(GetObjectInput{Path: PathConfig{Path: "/nonexistent/file.txt"}})
		if err == nil {
			t.Fatal("Expected error for non-existent file")
		}
		var notFound *FileNotFoundError
		if !errors.As(err, &notFound) {
			t.Fatalf("Expected FileNotFoundError, got %T", err)
		}
	})
}