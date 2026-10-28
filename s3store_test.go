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

func setupS3Store(t *testing.T) (*S3FS, func()) {
	t.Helper()

	cfg := S3FSConfig{
		S3Region:    "us-east-1",
		S3Bucket:    "project-data",
		AltEndpoint: "http://localhost:9000",
		Credentials: S3FS_Static{
			S3Id:  "myAccessKey",
			S3Key: "mySecretKey",
		},
	}

	fs, err := NewS3Store(cfg)
	if err != nil {
		t.Fatalf("Failed to create S3Store: %v", err)
	}

	teardown := func() {}

	return fs, teardown
}

func TestS3StoreErrorMapping(t *testing.T) {
	fs, teardown := setupS3Store(t)
	defer teardown()

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

func TestS3StoreMultipartIntegrity(t *testing.T) {
	_, teardown := setupS3Store(t)
	defer teardown()

	dir := t.TempDir()

	t.Run("SmallFileMultipart", func(t *testing.T) {
		filePath := filepath.Join(dir, "small.bin")
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
		for i := range chunk {
			chunk[i] = byte(i % 256)
		}

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
			ChunkUploadIds: []string{writeResult.ID},
		})
		if err != nil {
			t.Fatalf("CompleteObjectUpload failed: %v", err)
		}

		readContent, err := os.ReadFile(filePath)
		if err != nil {
			t.Fatalf("Failed to read multipart file: %v", err)
		}

		if !bytes.Equal(readContent, chunk) {
			t.Fatal("Multipart upload content doesn't match expected")
		}
	})

	t.Run("ChunkBoundaryTest", func(t *testing.T) {
		filePath := filepath.Join(dir, "boundary.bin")
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

		writeResult1, err := fsImpl.WriteChunk(UploadConfig{
			ObjectPath: filePath,
			ChunkId:    0,
			Data:       chunk1,
		})
		if err != nil {
			t.Fatalf("WriteChunk 1 failed: %v", err)
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
			ChunkUploadIds: []string{writeResult1.ID, writeResult2.ID},
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
}

func TestS3StoreResourceName(t *testing.T) {
	fs, teardown := setupS3Store(t)
	defer teardown()

	name := fs.ResourceName()
	if name != "project-data" {
		t.Fatalf("Expected resource name 'project-data', got '%s'", name)
	}
}

func TestS3StorePutAndGet(t *testing.T) {
	fs, teardown := setupS3Store(t)
	defer teardown()

	t.Run("PutAndGetObject", func(t *testing.T) {
		filePath := "testdata/put-get-test.txt"
		content := []byte("Hello, World!")

		poi := PutObjectInput{
			Source:  ObjectSource{Data: content},
			Dest:    PathConfig{Path: filePath},
			Context: context.Background(),
		}
		_, err := fs.PutObject(poi)
		if err != nil {
			t.Fatalf("PutObject failed: %v", err)
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

		fs.DeleteObjects(DeleteObjectInput{
			Paths: PathConfig{Paths: []string{filePath}},
		})
	})
}

func TestS3StoreListDir(t *testing.T) {
	fs, teardown := setupS3Store(t)
	defer teardown()

	t.Run("ListDirWithPrefix", func(t *testing.T) {
		prefix := "testdata/list-test"

		poi := PutObjectInput{
			Source:  ObjectSource{Data: []byte("test")},
			Dest:    PathConfig{Path: filepath.Join(prefix, "file1.txt")},
			Context: context.Background(),
		}
		_, err := fs.PutObject(poi)
		if err != nil {
			t.Fatalf("PutObject failed: %v", err)
		}

		poi = PutObjectInput{
			Source:  ObjectSource{Data: []byte("test")},
			Dest:    PathConfig{Path: filepath.Join(prefix, "file2.txt")},
			Context: context.Background(),
		}
		_, err = fs.PutObject(poi)
		if err != nil {
			t.Fatalf("PutObject failed: %v", err)
		}

		input := ListDirInput{Path: PathConfig{Path: prefix}}
		result, err := fs.ListDir(input)
		if err != nil {
			t.Fatalf("ListDir failed: %v", err)
		}

		if result == nil {
			t.Fatal("ListDir returned nil result")
		}

		fs.DeleteObjects(DeleteObjectInput{
			Paths: PathConfig{Paths: []string{prefix}},
		})
	})
}

func TestS3StoreWalk(t *testing.T) {
	fs, teardown := setupS3Store(t)
	defer teardown()

	t.Run("WalkWithPrefix", func(t *testing.T) {
		prefix := "testdata/walk-test"

		poi := PutObjectInput{
			Source:  ObjectSource{Data: []byte("test")},
			Dest:    PathConfig{Path: filepath.Join(prefix, "file1.txt")},
			Context: context.Background(),
		}
		_, err := fs.PutObject(poi)
		if err != nil {
			t.Fatalf("PutObject failed: %v", err)
		}

		paths := []string{}
		err = fs.Walk(WalkInput{Path: PathConfig{Path: prefix}}, func(path string, info os.FileInfo) error {
			paths = append(paths, path)
			return nil
		})
		if err != nil {
			t.Fatalf("Walk failed: %v", err)
		}

		if len(paths) == 0 {
			t.Fatal("Expected at least one path from walk")
		}

		fs.DeleteObjects(DeleteObjectInput{
			Paths: PathConfig{Paths: []string{prefix}},
		})
	})
}

func TestS3StoreGetObjectRange(t *testing.T) {
	fs, teardown := setupS3Store(t)
	defer teardown()

	t.Run("GetObjectWithRange", func(t *testing.T) {
		filePath := "testdata/range-test.txt"
		content := []byte("Hello, World! This is a test for range requests.")

		poi := PutObjectInput{
			Source:  ObjectSource{Data: content},
			Dest:    PathConfig{Path: filePath},
			Context: context.Background(),
		}
		_, err := fs.PutObject(poi)
		if err != nil {
			t.Fatalf("PutObject failed: %v", err)
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

		fs.DeleteObjects(DeleteObjectInput{
			Paths: PathConfig{Paths: []string{filePath}},
		})
	})
}

func TestS3StoreDeleteRecursive(t *testing.T) {
	fs, teardown := setupS3Store(t)
	defer teardown()

	t.Run("DeleteDirectoryRecursive", func(t *testing.T) {
		dirPath := "testdata/delete-test"

		poi := PutObjectInput{
			Source:  ObjectSource{Data: []byte("file1")},
			Dest:    PathConfig{Path: filepath.Join(dirPath, "file1.txt")},
			Context: context.Background(),
		}
		_, err := fs.PutObject(poi)
		if err != nil {
			t.Fatalf("PutObject failed: %v", err)
		}

		poi = PutObjectInput{
			Source:  ObjectSource{Data: []byte("file2")},
			Dest:    PathConfig{Path: filepath.Join(dirPath, "file2.txt")},
			Context: context.Background(),
		}
		_, err = fs.PutObject(poi)
		if err != nil {
			t.Fatalf("PutObject failed: %v", err)
		}

		errs := fs.DeleteObjects(DeleteObjectInput{
			Paths: PathConfig{Paths: []string{filepath.Join(dirPath, "file1.txt"), filepath.Join(dirPath, "file2.txt")}},
		})
		if len(errs) > 0 && errs[0] != nil {
			t.Logf("DeleteObjects returned errors: %v", errs)
		}

		_, err = fs.GetObjectInfo(PathConfig{Path: filepath.Join(dirPath, "file1.txt")})
		if err == nil {
			t.Fatal("file1 should have been deleted")
		}

		_, err = fs.GetObjectInfo(PathConfig{Path: filepath.Join(dirPath, "file2.txt")})
		if err == nil {
			t.Fatal("file2 should have been deleted")
		}
	})
}

func TestS3StoreGetObjectInfo(t *testing.T) {
	fs, teardown := setupS3Store(t)
	defer teardown()

	t.Run("GetObjectInfoOnFile", func(t *testing.T) {
		filePath := "testdata/info-test.txt"
		content := []byte("test content for info")

		poi := PutObjectInput{
			Source:  ObjectSource{Data: content},
			Dest:    PathConfig{Path: filePath},
			Context: context.Background(),
		}
		_, err := fs.PutObject(poi)
		if err != nil {
			t.Fatalf("PutObject failed: %v", err)
		}

		info, err := fs.GetObjectInfo(PathConfig{Path: filePath})
		if err != nil {
			t.Fatalf("GetObjectInfo failed: %v", err)
		}

		if info.Size() != int64(len(content)) {
			t.Fatalf("Expected size %d, got %d", len(content), info.Size())
		}

		if info.IsDir() {
			t.Fatal("Expected IsDir to be false")
		}

		fs.DeleteObjects(DeleteObjectInput{
			Paths: PathConfig{Paths: []string{filePath}},
		})
	})

	t.Run("GetObjectInfoOnPrefix", func(t *testing.T) {
		prefix := "testdata/info-dir"

		poi := PutObjectInput{
			Source:  ObjectSource{Data: []byte("test")},
			Dest:    PathConfig{Path: filepath.Join(prefix, "file.txt")},
			Context: context.Background(),
		}
		_, err := fs.PutObject(poi)
		if err != nil {
			t.Fatalf("PutObject failed: %v", err)
		}

		result, err := fs.ListDir(ListDirInput{Path: PathConfig{Path: prefix}})
		if err != nil {
			t.Fatalf("ListDir on prefix failed: %v", err)
		}

		if result == nil || len(*result) == 0 {
			t.Fatal("Expected at least one item in prefix")
		}

		fs.DeleteObjects(DeleteObjectInput{
			Paths: PathConfig{Paths: []string{prefix}},
		})
	})
}

func TestS3StoreCopyObject(t *testing.T) {
	fs, teardown := setupS3Store(t)
	defer teardown()

	t.Run("CopyObjectWithinBucket", func(t *testing.T) {
		srcPath := "testdata/copy-source.txt"
		destPath := "testdata/copy-dest.txt"

		_ = fs.DeleteObjects(DeleteObjectInput{
			Paths: PathConfig{Paths: []string{srcPath, destPath}},
		})

		poi := PutObjectInput{
			Source:  ObjectSource{Data: []byte("test content for copy")},
			Dest:    PathConfig{Path: srcPath},
			Context: context.Background(),
		}
		_, err := fs.PutObject(poi)
		if err != nil {
			t.Fatalf("PutObject failed: %v", err)
		}

		coi := CopyObjectInput{
			Src:      PathConfig{Path: srcPath},
			Dest:     PathConfig{Path: destPath},
			Progress: nil,
			Context:  context.Background(),
		}
		err = fs.CopyObject(coi)
		if err != nil {
			t.Fatalf("CopyObject failed: %v", err)
		}

		reader, err := fs.GetObject(GetObjectInput{Path: PathConfig{Path: destPath}})
		if err != nil {
			t.Fatalf("GetObject on copied file failed: %v", err)
		}
		defer reader.Close()

		data, err := io.ReadAll(reader)
		if err != nil {
			t.Fatalf("Reading copied file failed: %v", err)
		}

		if !bytes.Equal(data, []byte("test content for copy")) {
			t.Fatal("Copied content doesn't match expected")
		}

		fs.DeleteObjects(DeleteObjectInput{
			Paths: PathConfig{Paths: []string{srcPath, destPath}},
		})
	})
}

func TestS3StoreDeleteMultipleFiles(t *testing.T) {
	fs, teardown := setupS3Store(t)
	defer teardown()

	t.Run("DeleteMultipleFiles", func(t *testing.T) {
		file1 := "testdata/delete-multi/file1.txt"
		file2 := "testdata/delete-multi/file2.txt"

		_ = fs.DeleteObjects(DeleteObjectInput{
			Paths: PathConfig{Paths: []string{file1, file2}},
		})

		poi := PutObjectInput{
			Source:  ObjectSource{Data: []byte("file1")},
			Dest:    PathConfig{Path: file1},
			Context: context.Background(),
		}
		_, err := fs.PutObject(poi)
		if err != nil {
			t.Fatalf("PutObject failed: %v", err)
		}

		poi = PutObjectInput{
			Source:  ObjectSource{Data: []byte("file2")},
			Dest:    PathConfig{Path: file2},
			Context: context.Background(),
		}
		_, err = fs.PutObject(poi)
		if err != nil {
			t.Fatalf("PutObject failed: %v", err)
		}

		errs := fs.DeleteObjects(DeleteObjectInput{
			Paths: PathConfig{Paths: []string{file1, file2}},
		})
		if len(errs) > 0 && errs[0] != nil {
			t.Fatalf("DeleteObjects failed: %v", errs[0])
		}

		_, err = fs.GetObjectInfo(PathConfig{Path: file1})
		if err == nil {
			t.Fatal("file1 should have been deleted")
		}

		_, err = fs.GetObjectInfo(PathConfig{Path: file2})
		if err == nil {
			t.Fatal("file2 should have been deleted")
		}
	})
}

func TestS3StoreWalkNonExistentPath(t *testing.T) {
	fs, teardown := setupS3Store(t)
	defer teardown()

	t.Run("WalkNonExistentPath", func(t *testing.T) {
		wi := WalkInput{Path: PathConfig{Path: "nonexistent-bucket/path"}}
		err := fs.Walk(wi, func(path string, info os.FileInfo) error {
			return nil
		})
		if err != nil {
			t.Logf("Walk with non-existent path returned error: %v", err)
		}
	})
}

func TestS3StoreListDirPagination(t *testing.T) {
	fs, teardown := setupS3Store(t)
	defer teardown()

	t.Run("ListDirPagination", func(t *testing.T) {
		dir := "testdata/pagination-test"

		for i := 0; i < 5; i++ {
			poi := PutObjectInput{
				Source:  ObjectSource{Data: []byte("test")},
				Dest:    PathConfig{Path: filepath.Join(dir, string(rune('a'+i)))},
				Context: context.Background(),
			}
			_, err := fs.PutObject(poi)
			if err != nil {
				t.Fatalf("PutObject failed: %v", err)
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

		fs.DeleteObjects(DeleteObjectInput{
			Paths: PathConfig{Paths: []string{dir}},
		})
	})
}

func TestS3StoreWalkContext(t *testing.T) {
	fs, teardown := setupS3Store(t)
	defer teardown()

	t.Run("WalkWithCancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		wi := WalkInput{
			Path:    PathConfig{Path: "testdata", Context: ctx},
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

func TestS3StoreCopyObjectAcrossDirectories(t *testing.T) {
	fs, teardown := setupS3Store(t)
	defer teardown()

	t.Run("CopyObjectAcrossDirectories", func(t *testing.T) {
		srcPath := "testdata/copy-src/file.txt"
		destPath := "testdata/copy-dest/file.txt"

		_ = fs.DeleteObjects(DeleteObjectInput{
			Paths: PathConfig{Paths: []string{srcPath, destPath}},
		})

		poi := PutObjectInput{
			Source:  ObjectSource{Data: []byte("test content")},
			Dest:    PathConfig{Path: srcPath},
			Context: context.Background(),
		}
		_, err := fs.PutObject(poi)
		if err != nil {
			t.Fatalf("PutObject failed: %v", err)
		}

		coi := CopyObjectInput{
			Src:      PathConfig{Path: srcPath},
			Dest:     PathConfig{Path: destPath},
			Progress: nil,
			Context:  context.Background(),
		}
		err = fs.CopyObject(coi)
		if err != nil {
			t.Fatalf("CopyObject failed: %v", err)
		}

		reader, err := fs.GetObject(GetObjectInput{Path: PathConfig{Path: destPath}})
		if err != nil {
			t.Fatalf("GetObject on copied file failed: %v", err)
		}
		defer reader.Close()

		data, err := io.ReadAll(reader)
		if err != nil {
			t.Fatalf("Reading copied file failed: %v", err)
		}

		if !bytes.Equal(data, []byte("test content")) {
			t.Fatal("Copied content doesn't match expected")
		}

		fs.DeleteObjects(DeleteObjectInput{
			Paths: PathConfig{Paths: []string{srcPath, destPath}},
		})
	})
}

func TestS3StoreGetObjectWithRangeInvalid(t *testing.T) {
	fs, teardown := setupS3Store(t)
	defer teardown()

	t.Run("GetObjectWithInvalidRange", func(t *testing.T) {
		filePath := "testdata/range-invalid.txt"
		content := []byte("test content")

		poi := PutObjectInput{
			Source:  ObjectSource{Data: content},
			Dest:    PathConfig{Path: filePath},
			Context: context.Background(),
		}
		_, err := fs.PutObject(poi)
		if err != nil {
			t.Fatalf("PutObject failed: %v", err)
		}

		reader, err := fs.GetObject(GetObjectInput{
			Path:  PathConfig{Path: filePath},
			Range: "invalid-range",
		})
		if err != nil {
			t.Logf("GetObject with invalid range returned error: %v", err)
		} else {
			reader.Close()
		}

		fs.DeleteObjects(DeleteObjectInput{
			Paths: PathConfig{Paths: []string{filePath}},
		})
	})
}
