package filesapi

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestFileSystemStore(t *testing.T) {
	fs, err := NewFileStore(BlockFSConfig{})
	if err != nil {
		t.Fatal(err)
	}

	setup := func() func() {
		return func() {}
	}

	RunFileStoreContractTests(t, fs, setup)
}

func TestFileSystemStoreWithTempDir(t *testing.T) {
	fs, err := NewFileStore(BlockFSConfig{})
	if err != nil {
		t.Fatal(err)
	}

	RunFileStoreContractTests(t, fs, func() func() {
		return func() {}
	})
}

func TestFileSystemStoreEdgeCases(t *testing.T) {
	fs, err := NewFileStore(BlockFSConfig{})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("PathTraversal", func(t *testing.T) {
		dir := t.TempDir()

		t.Run("GetObjectInfoTraversal", func(t *testing.T) {
			traversalPath := filepath.Join(dir, "..", "..", "etc", "passwd")
			_, err := fs.GetObjectInfo(PathConfig{Path: traversalPath})
			if err == nil {
				t.Fatal("Expected error for path traversal attempt")
			}
		})

		t.Run("GetObjectTraversal", func(t *testing.T) {
			traversalPath := filepath.Join(dir, "..", "..", "etc", "passwd")
			_, err := fs.GetObject(GetObjectInput{Path: PathConfig{Path: traversalPath}})
			if err == nil {
				t.Fatal("Expected error for path traversal in GetObject")
			}
		})

		t.Run("PutObjectTraversal", func(t *testing.T) {
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

		t.Run("DeleteObjectsTraversal", func(t *testing.T) {
			traversalPath := filepath.Join(dir, "..", "outside.txt")
			errs := fs.DeleteObjects(DeleteObjectInput{
				Paths: PathConfig{Paths: []string{traversalPath}},
			})
			if len(errs) > 0 && errs[0] != nil {
				t.Logf("DeleteObjects with traversal path returned error: %v", errs[0])
			}
		})
	})

	t.Run("Permissions", func(t *testing.T) {
		dir := t.TempDir()

		t.Run("ReadOnlyDirectory", func(t *testing.T) {
			roDir := filepath.Join(dir, "readonly")
			if err := os.Mkdir(roDir, 0555); err != nil {
				t.Fatal(err)
			}

			testFile := filepath.Join(roDir, "test.txt")
			if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
				t.Skip("Cannot write to read-only directory on this system")
			}

			_, err := fs.GetObjectInfo(PathConfig{Path: testFile})
			if err != nil {
				t.Logf("GetObjectInfo on read-only file: %v", err)
			}

			_, err = fs.ListDir(ListDirInput{Path: PathConfig{Path: roDir}})
			if err != nil {
				t.Logf("ListDir on read-only dir: %v", err)
			}
		})

		t.Run("WriteToReadOnlyFile", func(t *testing.T) {
			roFile := filepath.Join(dir, "readonly.txt")
			if err := os.WriteFile(roFile, []byte("original"), 0444); err != nil {
				t.Fatal(err)
			}

			poi := PutObjectInput{
				Source:  ObjectSource{Data: []byte("new content")},
				Dest:    PathConfig{Path: roFile},
				Context: context.Background(),
			}
			_, err := fs.PutObject(poi)
			if err != nil {
				t.Logf("PutObject to read-only file returned error: %v", err)
			}
		})
	})

	t.Run("Symlinks", func(t *testing.T) {
		dir := t.TempDir()

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

			reader, err := fs.GetObject(GetObjectInput{Path: PathConfig{Path: linkPath}})
			if err != nil {
				t.Fatalf("GetObject on symlink failed: %v", err)
			}
			defer reader.Close()

			content, err := io.ReadAll(reader)
			if err != nil {
				t.Fatalf("Reading symlink content failed: %v", err)
			}

			if !bytes.Equal(content, []byte("target content")) {
				t.Fatalf("Expected 'target content', got '%s'", string(content))
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
	})

	t.Run("WalkContext", func(t *testing.T) {
		dir := t.TempDir()
		for i := 0; i < 5; i++ {
			if err := os.WriteFile(filepath.Join(dir, string(rune('a'+i))), []byte("test"), 0644); err != nil {
				t.Fatal(err)
			}
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
	})

	t.Run("CopyObject", func(t *testing.T) {
		dir := t.TempDir()

		t.Run("CopyAcrossDirectories", func(t *testing.T) {
			srcDir := filepath.Join(dir, "src")
			dstDir := filepath.Join(dir, "dst")
			if err := os.MkdirAll(srcDir, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(dstDir, 0755); err != nil {
				t.Fatal(err)
			}

			srcFile := filepath.Join(srcDir, "source.txt")
			dstFile := filepath.Join(dstDir, "dest.txt")
			if err := os.WriteFile(srcFile, []byte("test content"), 0644); err != nil {
				t.Fatal(err)
			}

			coi := CopyObjectInput{
				Src:      PathConfig{Path: srcFile},
				Dest:     PathConfig{Path: dstFile},
				Progress: nil,
				Context:  context.Background(),
			}
			err := fs.CopyObject(coi)
			if err != nil {
				t.Fatalf("CopyObject across directories failed: %v", err)
			}

			content, err := os.ReadFile(dstFile)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(content, []byte("test content")) {
				t.Fatal("Copied content doesn't match expected")
			}
		})

		t.Run("CopyNonExistentSource", func(t *testing.T) {
			dir := t.TempDir()
			coi := CopyObjectInput{
				Src:      PathConfig{Path: filepath.Join(dir, "nonexistent.txt")},
				Dest:     PathConfig{Path: filepath.Join(dir, "dest.txt")},
				Progress: nil,
				Context:  context.Background(),
			}
			err := fs.CopyObject(coi)
			if err == nil {
				t.Fatal("Expected error for non-existent source")
			}
		})
	})

	t.Run("DeleteObjects", func(t *testing.T) {
		t.Run("DeleteNonExistent", func(t *testing.T) {
			dir := t.TempDir()
			errs := fs.DeleteObjects(DeleteObjectInput{
				Paths: PathConfig{Paths: []string{filepath.Join(dir, "nonexistent.txt")}},
			})
			if len(errs) == 0 || errs[0] == nil {
				t.Fatal("Expected error for non-existent file")
			}
		})
	})

	t.Run("GetObjectRange", func(t *testing.T) {
		dir := t.TempDir()
		filePath := filepath.Join(dir, "test.txt")
		content := []byte("Hello, World!")
		if err := os.WriteFile(filePath, content, 0644); err != nil {
			t.Fatal(err)
		}

		t.Run("ValidRange", func(t *testing.T) {
			reader, err := fs.GetObject(GetObjectInput{
				Path:  PathConfig{Path: filePath},
				Range: "bytes=0-4",
			})
			if err != nil {
				t.Fatalf("GetObject with valid range failed: %v", err)
			}
			defer reader.Close()

			data, err := io.ReadAll(reader)
			if err != nil {
				t.Fatalf("Reading GetObject result failed: %v", err)
			}
			if !bytes.Equal(data, []byte("Hello")) {
				t.Fatalf("Expected 'Hello', got '%s'", string(data))
			}
		})

		t.Run("InvalidRangeFormat", func(t *testing.T) {
			_, err := fs.GetObject(GetObjectInput{
				Path:  PathConfig{Path: filePath},
				Range: "invalid",
			})
			if err == nil {
				t.Fatal("Expected error for invalid range format")
			}
		})
	})

	t.Run("WalkError", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "test.txt"), []byte("test"), 0644); err != nil {
			t.Fatal(err)
		}

		wi := WalkInput{Path: PathConfig{Path: dir}}
		expectedErr := errors.New("walk error")
		err := fs.Walk(wi, func(path string, info os.FileInfo) error {
			return expectedErr
		})
		if err == nil {
			t.Fatal("Expected error from visitor function")
		}
	})

	t.Run("MultipartDirectWriteStaging", func(t *testing.T) {
		dir := t.TempDir()
		targetFile := filepath.Join(dir, "target.dat")
		chunkSize := int64(16)
		blockFS, err := NewFileStore(BlockFSConfig{ChunkSize: chunkSize})
		if err != nil {
			t.Fatalf("Failed to create store: %v", err)
		}

		initRes, err := blockFS.InitializeObjectUpload(UploadConfig{
			ObjectPath: targetFile,
		})
		if err != nil {
			t.Fatalf("InitializeObjectUpload failed: %v", err)
		}

		// Verify target file does not yet exist while upload is in flight
		if _, err := os.Stat(targetFile); err == nil {
			t.Fatal("Target file should not exist during in-flight upload before completion")
		}

		chunk0 := []byte("0123456789abcdef")
		chunk1 := []byte("fedcba9876543210")

		write0, err := blockFS.WriteChunk(UploadConfig{
			ObjectPath: targetFile,
			UploadId:   initRes.ID,
			ChunkId:    0,
			Data:       chunk0,
		})
		if err != nil {
			t.Fatalf("WriteChunk 0 failed: %v", err)
		}
		if write0.WriteSize != len(chunk0) {
			t.Fatalf("Expected write size %d, got %d", len(chunk0), write0.WriteSize)
		}

		// Still should not exist at targetFile
		if _, err := os.Stat(targetFile); err == nil {
			t.Fatal("Target file should not exist before CompleteObjectUpload")
		}

		write1, err := blockFS.WriteChunk(UploadConfig{
			ObjectPath: targetFile,
			UploadId:   initRes.ID,
			ChunkId:    1,
			Data:       chunk1,
		})
		if err != nil {
			t.Fatalf("WriteChunk 1 failed: %v", err)
		}
		if write1.WriteSize != len(chunk1) {
			t.Fatalf("Expected write size %d, got %d", len(chunk1), write1.WriteSize)
		}

		err = blockFS.CompleteObjectUpload(CompletedObjectUploadConfig{
			ObjectPath: targetFile,
			UploadId:   initRes.ID,
		})
		if err != nil {
			t.Fatalf("CompleteObjectUpload failed: %v", err)
		}

		// Now targetFile should exist and contain both chunks in order
		data, err := os.ReadFile(targetFile)
		if err != nil {
			t.Fatalf("Failed to read completed file: %v", err)
		}
		expected := append(chunk0, chunk1...)
		if !bytes.Equal(data, expected) {
			t.Fatalf("Expected file content %s, got %s", string(expected), string(data))
		}
	})
}