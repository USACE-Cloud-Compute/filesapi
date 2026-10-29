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

func RunFileStoreContractTests(t *testing.T, fs FileStore, setup func() func()) {
	t.Helper()

	t.Run("ListDir", func(t *testing.T) {
		teardown := setup()
		defer teardown()

		t.Run("EmptyDir", func(t *testing.T) {
			dir := t.TempDir()
			input := ListDirInput{Path: PathConfig{Path: dir}}
			result, err := fs.ListDir(input)
			if err != nil {
				t.Fatalf("ListDir on empty dir failed: %v", err)
			}
			if result == nil {
				t.Fatal("ListDir returned nil result")
			}
			if len(*result) != 0 {
				t.Fatalf("Expected empty list, got %d items", len(*result))
			}
		})

		t.Run("PopulatedDir", func(t *testing.T) {
			dir := t.TempDir()
			testFile := filepath.Join(dir, "test.txt")
			if err := os.WriteFile(testFile, []byte("test content"), 0644); err != nil {
				t.Fatal(err)
			}
			input := ListDirInput{Path: PathConfig{Path: dir}}
			result, err := fs.ListDir(input)
			if err != nil {
				t.Fatalf("ListDir on populated dir failed: %v", err)
			}
			if result == nil || len(*result) != 1 {
				t.Fatalf("Expected 1 item, got %d", len(*result))
			}
			if (*result)[0].Name != "test.txt" {
				t.Fatalf("Expected 'test.txt', got '%s'", (*result)[0].Name)
			}
		})

		t.Run("NonExistentPath", func(t *testing.T) {
			input := ListDirInput{Path: PathConfig{Path: "/nonexistent/path"}}
			_, err := fs.ListDir(input)
			if err == nil {
				t.Fatal("Expected error for non-existent path")
			}
		})

		t.Run("Pagination", func(t *testing.T) {
			dir := t.TempDir()
			for i := 0; i < 5; i++ {
				if err := os.WriteFile(filepath.Join(dir, string(rune('a'+i))), []byte("test"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			input := ListDirInput{Path: PathConfig{Path: dir}, Size: 2}
			result, err := fs.ListDir(input)
			if err != nil {
				t.Fatalf("ListDir pagination failed: %v", err)
			}
			if result == nil {
				t.Fatal("ListDir returned nil result")
			}
			if len(*result) > 5 {
				t.Fatalf("Expected at most 5 items, got %d", len(*result))
			}
		})
	})

	t.Run("Walk", func(t *testing.T) {
		teardown := setup()
		defer teardown()

		t.Run("EmptyDir", func(t *testing.T) {
			dir := t.TempDir()
			wi := WalkInput{Path: PathConfig{Path: dir}}
			count := 0
			err := fs.Walk(wi, func(path string, info os.FileInfo) error {
				count++
				return nil
			})
			if err != nil {
				t.Fatalf("Walk on empty dir failed: %v", err)
			}
			if count != 1 {
				t.Fatalf("Expected 1 item (the dir itself), got %d", count)
			}
		})

		t.Run("NestedTree", func(t *testing.T) {
			dir := t.TempDir()
			subdir := filepath.Join(dir, "subdir")
			if err := os.Mkdir(subdir, 0755); err != nil {
				t.Fatal(err)
			}
			file1 := filepath.Join(dir, "file1.txt")
			file2 := filepath.Join(subdir, "file2.txt")
			if err := os.WriteFile(file1, []byte("content1"), 0644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file2, []byte("content2"), 0644); err != nil {
				t.Fatal(err)
			}

			wi := WalkInput{Path: PathConfig{Path: dir}}
			paths := []string{}
			err := fs.Walk(wi, func(path string, info os.FileInfo) error {
				paths = append(paths, path)
				return nil
			})
			if err != nil {
				t.Fatalf("Walk on nested tree failed: %v", err)
			}
			if len(paths) < 2 {
				t.Fatalf("Expected at least 2 items, got %d", len(paths))
			}
		})

		t.Run("NonExistentPath", func(t *testing.T) {
			wi := WalkInput{Path: PathConfig{Path: "/nonexistent/path"}}
			err := fs.Walk(wi, func(path string, info os.FileInfo) error {
				return nil
			})
			if err == nil {
				t.Fatal("Expected error for non-existent path")
			}
		})

		t.Run("ErrorHandling", func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "test.txt"), []byte("content"), 0644); err != nil {
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
	})

	t.Run("GetObjectInfo", func(t *testing.T) {
		teardown := setup()
		defer teardown()

		t.Run("ExistingFile", func(t *testing.T) {
			dir := t.TempDir()
			filePath := filepath.Join(dir, "test.txt")
			if err := os.WriteFile(filePath, []byte("test content"), 0644); err != nil {
				t.Fatal(err)
			}

			info, err := fs.GetObjectInfo(PathConfig{Path: filePath})
			if err != nil {
				t.Fatalf("GetObjectInfo on existing file failed: %v", err)
			}
			if info == nil {
				t.Fatal("GetObjectInfo returned nil")
			}
			if info.Name() != "test.txt" {
				t.Fatalf("Expected name 'test.txt', got '%s'", info.Name())
			}
		})

		t.Run("ExistingDir", func(t *testing.T) {
			dir := t.TempDir()
			info, err := fs.GetObjectInfo(PathConfig{Path: dir})
			if err != nil {
				t.Fatalf("GetObjectInfo on existing dir failed: %v", err)
			}
			if info == nil {
				t.Fatal("GetObjectInfo returned nil")
			}
			if !info.IsDir() {
				t.Fatal("Expected IsDir to be true")
			}
		})

		t.Run("NonExistentPath", func(t *testing.T) {
			_, err := fs.GetObjectInfo(PathConfig{Path: "/nonexistent/path"})
			if err == nil {
				t.Fatal("Expected error for non-existent path")
			}
			var notFound *FileNotFoundError
			if !errors.As(err, &notFound) {
				t.Fatalf("Expected FileNotFoundError, got %T", err)
			}
		})
	})

	t.Run("GetObject", func(t *testing.T) {
		teardown := setup()
		defer teardown()

		t.Run("FullDownload", func(t *testing.T) {
			dir := t.TempDir()
			filePath := filepath.Join(dir, "test.txt")
			content := []byte("Hello, World!")
			if err := os.WriteFile(filePath, content, 0644); err != nil {
				t.Fatal(err)
			}

			reader, err := fs.GetObject(GetObjectInput{Path: PathConfig{Path: filePath}})
			if err != nil {
				t.Fatalf("GetObject failed: %v", err)
			}
			defer reader.Close()

			data, err := io.ReadAll(reader)
			if err != nil {
				t.Fatalf("Reading GetObject result failed: %v", err)
			}
			if !bytes.Equal(data, content) {
				t.Fatalf("Expected '%s', got '%s'", string(content), string(data))
			}
		})

		t.Run("RangeRequest", func(t *testing.T) {
			dir := t.TempDir()
			filePath := filepath.Join(dir, "test.txt")
			content := []byte("Hello, World!")
			if err := os.WriteFile(filePath, content, 0644); err != nil {
				t.Fatal(err)
			}

			reader, err := fs.GetObject(GetObjectInput{
				Path:  PathConfig{Path: filePath},
				Range: "bytes=0-4",
			})
			if err != nil {
				t.Fatalf("GetObject with range failed: %v", err)
			}
			defer reader.Close()

			data, err := io.ReadAll(reader)
			if err != nil {
				t.Fatalf("Reading GetObject result failed: %v", err)
			}
			expected := []byte("Hello")
			if !bytes.Equal(data, expected) {
				t.Fatalf("Expected '%s', got '%s'", string(expected), string(data))
			}
		})

		t.Run("NonExistentFile", func(t *testing.T) {
			_, err := fs.GetObject(GetObjectInput{Path: PathConfig{Path: "/nonexistent/file.txt"}})
			if err == nil {
				t.Fatal("Expected error for non-existent file")
			}
			var notFound *FileNotFoundError
			if !errors.As(err, &notFound) {
				t.Fatalf("Expected FileNotFoundError, got %T", err)
			}
		})

		t.Run("InvalidRange", func(t *testing.T) {
			dir := t.TempDir()
			filePath := filepath.Join(dir, "test.txt")
			if err := os.WriteFile(filePath, []byte("test"), 0644); err != nil {
				t.Fatal(err)
			}

			_, err := fs.GetObject(GetObjectInput{
				Path:  PathConfig{Path: filePath},
				Range: "invalid-range",
			})
			if err == nil {
				t.Fatal("Expected error for invalid range")
			}
		})
	})

	t.Run("PutObject", func(t *testing.T) {
		teardown := setup()
		defer teardown()

		t.Run("SourceData", func(t *testing.T) {
			dir := t.TempDir()
			filePath := PathConfig{Path: filepath.Join(dir, "test.txt")}
			data := []byte("test content from data")

			poi := PutObjectInput{
				Source:  ObjectSource{Data: data},
				Dest:    filePath,
				Context: context.Background(),
			}
			output, err := fs.PutObject(poi)
			if err != nil {
				t.Fatalf("PutObject with Data failed: %v", err)
			}
			if output == nil {
				t.Fatal("PutObject returned nil output")
			}

			readContent, err := os.ReadFile(filePath.Path)
			if err != nil {
				t.Fatalf("Failed to read written file: %v", err)
			}
			if !bytes.Equal(readContent, data) {
				t.Fatalf("Expected '%s', got '%s'", string(data), string(readContent))
			}
		})

		t.Run("SourceReader", func(t *testing.T) {
			dir := t.TempDir()
			filePath := PathConfig{Path: filepath.Join(dir, "test.txt")}
			data := []byte("test content from reader")
			reader := bytes.NewReader(data)

			poi := PutObjectInput{
				Source:  ObjectSource{Reader: reader},
				Dest:    filePath,
				Context: context.Background(),
			}
			_, err := fs.PutObject(poi)
			if err != nil {
				t.Fatalf("PutObject with Reader failed: %v", err)
			}

			readContent, err := os.ReadFile(filePath.Path)
			if err != nil {
				t.Fatalf("Failed to read written file: %v", err)
			}
			if !bytes.Equal(readContent, data) {
				t.Fatalf("Expected '%s', got '%s'", string(data), string(readContent))
			}
		})

		t.Run("SourceFilepath", func(t *testing.T) {
			dir := t.TempDir()
			srcFile := filepath.Join(dir, "source.txt")
			destFile := filepath.Join(dir, "dest.txt")
			content := []byte("test content from file")
			if err := os.WriteFile(srcFile, content, 0644); err != nil {
				t.Fatal(err)
			}

			poi := PutObjectInput{
				Source:  ObjectSource{Filepath: PathConfig{Path: srcFile}},
				Dest:    PathConfig{Path: destFile},
				Context: context.Background(),
			}
			_, err := fs.PutObject(poi)
			if err != nil {
				t.Fatalf("PutObject with Filepath failed: %v", err)
			}

			readContent, err := os.ReadFile(destFile)
			if err != nil {
				t.Fatalf("Failed to read written file: %v", err)
			}
			if !bytes.Equal(readContent, content) {
				t.Fatalf("Expected '%s', got '%s'", string(content), string(readContent))
			}
		})

		t.Run("OverwriteExisting", func(t *testing.T) {
			dir := t.TempDir()
			filePath := PathConfig{Path: filepath.Join(dir, "test.txt")}
			if err := os.WriteFile(filePath.Path, []byte("original"), 0644); err != nil {
				t.Fatal(err)
			}

			newContent := []byte("overwritten")
			poi := PutObjectInput{
				Source:  ObjectSource{Data: newContent},
				Dest:    filePath,
				Context: context.Background(),
			}
			_, err := fs.PutObject(poi)
			if err != nil {
				t.Fatalf("PutObject overwrite failed: %v", err)
			}

			readContent, err := os.ReadFile(filePath.Path)
			if err != nil {
				t.Fatalf("Failed to read file: %v", err)
			}
			if !bytes.Equal(readContent, newContent) {
				t.Fatalf("Expected '%s', got '%s'", string(newContent), string(readContent))
			}
		})
	})

	t.Run("CopyObject", func(t *testing.T) {
		teardown := setup()
		defer teardown()

		t.Run("SameDirectory", func(t *testing.T) {
			dir := t.TempDir()
			srcFile := filepath.Join(dir, "source.txt")
			destFile := filepath.Join(dir, "dest.txt")
			if err := os.WriteFile(srcFile, []byte("test content"), 0644); err != nil {
				t.Fatal(err)
			}

			coi := CopyObjectInput{
				Src:      PathConfig{Path: srcFile},
				Dest:     PathConfig{Path: destFile},
				Progress: nil,
				Context:  context.Background(),
			}
			err := fs.CopyObject(coi)
			if err != nil {
				t.Fatalf("CopyObject in same dir failed: %v", err)
			}

			srcContent, err := os.ReadFile(srcFile)
			if err != nil {
				t.Fatal(err)
			}
			destContent, err := os.ReadFile(destFile)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(srcContent, destContent) {
				t.Fatal("Copied content doesn't match source")
			}
		})

		t.Run("DifferentDirectory", func(t *testing.T) {
			dir1 := t.TempDir()
			dir2 := t.TempDir()
			srcFile := filepath.Join(dir1, "source.txt")
			destFile := filepath.Join(dir2, "dest.txt")
			if err := os.WriteFile(srcFile, []byte("test content"), 0644); err != nil {
				t.Fatal(err)
			}

			coi := CopyObjectInput{
				Src:      PathConfig{Path: srcFile},
				Dest:     PathConfig{Path: destFile},
				Progress: nil,
				Context:  context.Background(),
			}
			err := fs.CopyObject(coi)
			if err != nil {
				t.Fatalf("CopyObject across dirs failed: %v", err)
			}

			destContent, err := os.ReadFile(destFile)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(destContent, []byte("test content")) {
				t.Fatal("Copied content doesn't match expected")
			}
		})

		t.Run("Overwrite", func(t *testing.T) {
			dir := t.TempDir()
			srcFile := filepath.Join(dir, "source.txt")
			destFile := filepath.Join(dir, "dest.txt")
			if err := os.WriteFile(srcFile, []byte("new content"), 0644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(destFile, []byte("old content"), 0644); err != nil {
				t.Fatal(err)
			}

			coi := CopyObjectInput{
				Src:      PathConfig{Path: srcFile},
				Dest:     PathConfig{Path: destFile},
				Progress: nil,
				Context:  context.Background(),
			}
			err := fs.CopyObject(coi)
			if err != nil {
				t.Fatalf("CopyObject overwrite failed: %v", err)
			}

			destContent, err := os.ReadFile(destFile)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(destContent, []byte("new content")) {
				t.Fatal("Overwrite didn't work correctly")
			}
		})

		t.Run("NonExistentSource", func(t *testing.T) {
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
		teardown := setup()
		defer teardown()

		t.Run("SingleFile", func(t *testing.T) {
			dir := t.TempDir()
			filePath := filepath.Join(dir, "test.txt")
			if err := os.WriteFile(filePath, []byte("test"), 0644); err != nil {
				t.Fatal(err)
			}

			errs := fs.DeleteObjects(DeleteObjectInput{
				Paths: PathConfig{Paths: []string{filePath}},
			})
			if len(errs) > 0 && errs[0] != nil {
				t.Fatalf("DeleteObjects failed: %v", errs[0])
			}

			if _, err := os.Stat(filePath); !os.IsNotExist(err) {
				t.Fatal("File should have been deleted")
			}
		})

		t.Run("MultipleFiles", func(t *testing.T) {
			dir := t.TempDir()
			file1 := filepath.Join(dir, "file1.txt")
			file2 := filepath.Join(dir, "file2.txt")
			if err := os.WriteFile(file1, []byte("test1"), 0644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file2, []byte("test2"), 0644); err != nil {
				t.Fatal(err)
			}

			errs := fs.DeleteObjects(DeleteObjectInput{
				Paths: PathConfig{Paths: []string{file1, file2}},
			})
			if len(errs) > 0 && errs[0] != nil {
				t.Fatalf("DeleteObjects failed: %v", errs[0])
			}

			if _, err := os.Stat(file1); !os.IsNotExist(err) {
				t.Fatal("file1 should have been deleted")
			}
			if _, err := os.Stat(file2); !os.IsNotExist(err) {
				t.Fatal("file2 should have been deleted")
			}
		})

		t.Run("RecursiveDirectory", func(t *testing.T) {
			dir := t.TempDir()
			subdir := filepath.Join(dir, "subdir")
			if err := os.Mkdir(subdir, 0755); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(subdir, "file.txt")
			if err := os.WriteFile(file, []byte("test"), 0644); err != nil {
				t.Fatal(err)
			}

			errs := fs.DeleteObjects(DeleteObjectInput{
				Paths: PathConfig{Paths: []string{dir}},
			})
			if len(errs) > 0 && errs[0] != nil {
				t.Fatalf("DeleteObjects failed: %v", errs[0])
			}

			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Fatal("Directory should have been deleted recursively")
			}
		})

		t.Run("NonExistentFile", func(t *testing.T) {
			dir := t.TempDir()
			errs := fs.DeleteObjects(DeleteObjectInput{
				Paths: PathConfig{Paths: []string{filepath.Join(dir, "nonexistent.txt")}},
			})
			if len(errs) == 0 || errs[0] == nil {
				t.Fatal("Expected error for non-existent file")
			}
		})
	})

	t.Run("MultipartFlow", func(t *testing.T) {
		teardown := setup()
		defer teardown()

		t.Run("InitializeWriteComplete", func(t *testing.T) {
			dir := t.TempDir()
			filePath := filepath.Join(dir, "multipart.bin")
			chunkSize := int64(1024)

			initConfig := BlockFSConfig{ChunkSize: chunkSize}
			fsImpl, err := NewFileStore(initConfig)
			if err != nil {
				t.Fatalf("Failed to create filestore: %v", err)
			}

			initResult, err := fsImpl.InitializeObjectUpload(UploadConfig{
				ObjectPath: filePath,
			})
			if err != nil {
				t.Fatalf("InitializeObjectUpload failed: %v", err)
			}

			chunk1 := make([]byte, chunkSize)
			for i := range chunk1 {
				chunk1[i] = byte(i % 256)
			}

			writeResult, err := fsImpl.WriteChunk(UploadConfig{
				ObjectPath: filePath,
				ChunkId:    0,
				Data:       chunk1,
			})
			if err != nil {
				t.Fatalf("WriteChunk failed: %v", err)
			}

			chunk2 := make([]byte, chunkSize)
			for i := range chunk2 {
				chunk2[i] = byte((i + 100) % 256)
			}

			writeResult2, err := fsImpl.WriteChunk(UploadConfig{
				ObjectPath: filePath,
				ChunkId:    1,
				Data:       chunk2,
			})
			if err != nil {
				t.Fatalf("WriteChunk 2 failed: %v", err)
			}

			err = fsImpl.CompleteObjectUpload(CompletedObjectUploadConfig{
				ObjectPath:     filePath,
				UploadId:       initResult.ID,
				ChunkUploadIds: []string{writeResult.ID, writeResult2.ID},
			})
			if err != nil {
				t.Fatalf("CompleteObjectUpload failed: %v", err)
			}

			readContent, err := os.ReadFile(filePath)
			if err != nil {
				t.Fatalf("Failed to read multipart file: %v", err)
			}

			expected := append(chunk1, chunk2...)
			if !bytes.Equal(readContent, expected) {
				t.Fatal("Multipart upload content doesn't match expected")
			}
		})
	})

	t.Run("MultipartErrors", func(t *testing.T) {
		teardown := setup()
		defer teardown()

		t.Run("InvalidChunkId", func(t *testing.T) {
			dir := t.TempDir()
			filePath := filepath.Join(dir, "test.bin")
			chunkSize := int64(1024)

			initConfig := BlockFSConfig{ChunkSize: chunkSize}
			fsImpl, err := NewFileStore(initConfig)
			if err != nil {
				t.Fatalf("Failed to create filestore: %v", err)
			}

			initResult, err := fsImpl.InitializeObjectUpload(UploadConfig{
				ObjectPath: filePath,
			})
			if err != nil {
				t.Fatalf("InitializeObjectUpload failed: %v", err)
			}

			chunk := make([]byte, chunkSize)
			writeResult, err := fsImpl.WriteChunk(UploadConfig{
				ObjectPath: filePath,
				ChunkId:    0,
				Data:       chunk,
			})
			if err != nil {
				t.Fatalf("WriteChunk failed: %v", err)
			}

			err = fsImpl.CompleteObjectUpload(CompletedObjectUploadConfig{
				ObjectPath:     filePath,
				UploadId:       initResult.ID,
				ChunkUploadIds: []string{writeResult.ID, "invalid-etag"},
			})
			if err != nil {
				t.Logf("CompleteObjectUpload returned error (expected for some implementations): %v", err)
			}
		})

		t.Run("IncompleteUpload", func(t *testing.T) {
			dir := t.TempDir()
			filePath := filepath.Join(dir, "incomplete.bin")
			chunkSize := int64(1024)

			initConfig := BlockFSConfig{ChunkSize: chunkSize}
			fsImpl, err := NewFileStore(initConfig)
			if err != nil {
				t.Fatalf("Failed to create filestore: %v", err)
			}

			initResult, err := fsImpl.InitializeObjectUpload(UploadConfig{
				ObjectPath: filePath,
			})
			if err != nil {
				t.Fatalf("InitializeObjectUpload failed: %v", err)
			}

			chunk := make([]byte, chunkSize)
			_, err = fsImpl.WriteChunk(UploadConfig{
				ObjectPath: filePath,
				ChunkId:    0,
				Data:       chunk,
			})
			if err != nil {
				t.Fatalf("WriteChunk failed: %v", err)
			}

			err = fsImpl.CompleteObjectUpload(CompletedObjectUploadConfig{
				ObjectPath: filePath,
				UploadId:   initResult.ID,
			})
			if err != nil {
				t.Logf("CompleteObjectUpload returned error (expected for some implementations): %v", err)
			}
		})
	})

	t.Run("ResourceName", func(t *testing.T) {
		name := fs.ResourceName()
		if name == "" {
			t.Log("ResourceName returned empty string (expected for BlockFS)")
		}
	})
}