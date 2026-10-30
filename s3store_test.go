package filesapi

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
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

type mockDeleteObjectsRequest struct {
	Objects []struct {
		Key string `xml:"Key"`
	} `xml:"Object"`
}

func TestS3StoreDeleteObjectsPaginationAndBatching(t *testing.T) {
	t.Run("MultiPagePrefixWalkAndExactBatching", func(t *testing.T) {
		var mu sync.Mutex
		objects := make(map[string]bool)
		const totalKeys = 1005
		for i := 0; i < totalKeys; i++ {
			objects[fmt.Sprintf("dir/file-%04d.txt", i)] = true
		}

		var deleteBatchSizes []int
		deletedKeysCount := make(map[string]int)
		listCalls := 0

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()

			// GetObjectAttributes on prefix "dir" -> return 404 NoSuchKey so it's treated as a directory
			if r.Method == http.MethodGet && r.URL.Query().Has("attributes") {
				w.WriteHeader(http.StatusNotFound)
				w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><Error><Code>NoSuchKey</Code><Message>Not found</Message></Error>`))
				return
			}

			// ListObjectsV2
			if r.Method == http.MethodGet && r.URL.Query().Get("list-type") == "2" {
				listCalls++
				if listCalls > 20 {
					t.Errorf("Infinite loop detected in ListObjectsV2")
					w.WriteHeader(http.StatusInternalServerError)
					return
				}

				prefix := r.URL.Query().Get("prefix")
				token := r.URL.Query().Get("continuation-token")
				maxKeys := 500
				if mk, err := strconv.Atoi(r.URL.Query().Get("max-keys")); err == nil && mk > 0 {
					maxKeys = mk
				}

				// Deterministic sorted list of matching keys
				var allKeys []string
				for i := 0; i < totalKeys; i++ {
					k := fmt.Sprintf("dir/file-%04d.txt", i)
					if strings.HasPrefix(k, prefix) {
						allKeys = append(allKeys, k)
					}
				}
				sort.Strings(allKeys)

				startIdx := 0
				if token != "" {
					startIdx, _ = strconv.Atoi(token)
				}
				endIdx := startIdx + maxKeys
				isTruncated := false
				nextToken := ""
				if endIdx < len(allKeys) {
					isTruncated = true
					nextToken = strconv.Itoa(endIdx)
				} else {
					endIdx = len(allKeys)
				}

				var sb strings.Builder
				sb.WriteString(`<?xml version="1.0" encoding="UTF-8"?><ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">`)
				sb.WriteString(fmt.Sprintf("<IsTruncated>%v</IsTruncated>", isTruncated))
				if isTruncated {
					sb.WriteString(fmt.Sprintf("<NextContinuationToken>%s</NextContinuationToken>", nextToken))
				}
				for _, k := range allKeys[startIdx:endIdx] {
					sb.WriteString(fmt.Sprintf("<Contents><Key>%s</Key><Size>4</Size><LastModified>2026-01-01T00:00:00.000Z</LastModified></Contents>", k))
				}
				sb.WriteString(`</ListBucketResult>`)
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(sb.String()))
				return
			}

			// DeleteObjects
			if r.Method == http.MethodPost && r.URL.Query().Has("delete") {
				body, _ := io.ReadAll(r.Body)
				var req mockDeleteObjectsRequest
				if err := xml.Unmarshal(body, &req); err != nil {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				deleteBatchSizes = append(deleteBatchSizes, len(req.Objects))
				var sb strings.Builder
				sb.WriteString(`<?xml version="1.0" encoding="UTF-8"?><DeleteResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">`)
				for _, o := range req.Objects {
					deletedKeysCount[o.Key]++
					delete(objects, o.Key)
					sb.WriteString(fmt.Sprintf("<Deleted><Key>%s</Key></Deleted>", o.Key))
				}
				sb.WriteString(`</DeleteResult>`)
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(sb.String()))
				return
			}

			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()

		s3fs, err := NewS3Store(S3FSConfig{
			S3Region:    "us-east-1",
			S3Bucket:    "test-bucket",
			AltEndpoint: srv.URL,
			MaxKeys:     500, // Force multi-page ListObjectsV2 (3 pages for 1005 keys)
			Credentials: S3FS_Static{S3Id: "test", S3Key: "test"},
		})
		if err != nil {
			t.Fatalf("NewS3Store failed: %v", err)
		}

		errs := s3fs.DeleteObjects(DeleteObjectInput{
			Paths: PathConfig{Paths: []string{"dir"}},
		})
		if len(errs) != 0 {
			t.Fatalf("Expected no errors, got: %v", errs)
		}

		if len(deleteBatchSizes) != 2 || deleteBatchSizes[0] != 1000 || deleteBatchSizes[1] != 5 {
			t.Fatalf("Expected batch sizes [1000, 5], got %v", deleteBatchSizes)
		}

		if len(deletedKeysCount) != totalKeys {
			t.Fatalf("Expected %d unique keys deleted, got %d", totalKeys, len(deletedKeysCount))
		}
		for k, count := range deletedKeysCount {
			if count != 1 {
				t.Fatalf("Key %s was deleted %d times, expected 1", k, count)
			}
		}
	})

	t.Run("MultipleFilesBatchedWithoutDuplicationAndErrorsPropagated", func(t *testing.T) {
		var mu sync.Mutex
		var deleteBatchSizes []int
		deletedKeysCount := make(map[string]int)

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()

			// GetObjectAttributes -> return valid file attributes so IsDir() == false
			if r.Method == http.MethodGet && r.URL.Query().Has("attributes") {
				w.Header().Set("Last-Modified", "Thu, 01 Jan 2026 00:00:00 GMT")
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><GetObjectAttributesOutput xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><ETag>"12345"</ETag><ObjectSize>10</ObjectSize></GetObjectAttributesOutput>`))
				return
			}

			// DeleteObjects
			if r.Method == http.MethodPost && r.URL.Query().Has("delete") {
				body, _ := io.ReadAll(r.Body)
				var req mockDeleteObjectsRequest
				if err := xml.Unmarshal(body, &req); err != nil {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				deleteBatchSizes = append(deleteBatchSizes, len(req.Objects))
				var sb strings.Builder
				sb.WriteString(`<?xml version="1.0" encoding="UTF-8"?><DeleteResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">`)
				for _, o := range req.Objects {
					deletedKeysCount[o.Key]++
					if o.Key == "fail.txt" {
						sb.WriteString(`<Error><Key>fail.txt</Key><Code>AccessDenied</Code><Message>Access Denied</Message></Error>`)
					} else {
						sb.WriteString(fmt.Sprintf("<Deleted><Key>%s</Key></Deleted>", o.Key))
					}
				}
				sb.WriteString(`</DeleteResult>`)
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(sb.String()))
				return
			}

			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()

		s3fs, err := NewS3Store(S3FSConfig{
			S3Region:    "us-east-1",
			S3Bucket:    "test-bucket",
			AltEndpoint: srv.URL,
			Credentials: S3FS_Static{S3Id: "test", S3Key: "test"},
		})
		if err != nil {
			t.Fatalf("NewS3Store failed: %v", err)
		}

		errs := s3fs.DeleteObjects(DeleteObjectInput{
			Paths: PathConfig{Paths: []string{"file1.txt", "file2.txt", "fail.txt"}},
		})

		// Should batch all 3 files into a single DeleteObjects request without re-deleting earlier files
		if len(deleteBatchSizes) != 1 || deleteBatchSizes[0] != 3 {
			t.Fatalf("Expected single batch of 3 objects, got batches: %v", deleteBatchSizes)
		}
		for _, k := range []string{"file1.txt", "file2.txt", "fail.txt"} {
			if deletedKeysCount[k] != 1 {
				t.Fatalf("Expected %s to be submitted once, got %d", k, deletedKeysCount[k])
			}
		}

		// Should surface the batch delete error for fail.txt (not shadow errs)
		if len(errs) != 1 || !strings.Contains(errs[0].Error(), "fail.txt: AccessDenied") {
			t.Fatalf("Expected 1 AccessDenied error for fail.txt, got: %v", errs)
		}
	})

	t.Run("ExactBatchMultipleAndEmptyDirectoryDoNotReturnNothingToDelete", func(t *testing.T) {
		var mu sync.Mutex
		var deleteBatchSizes []int

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()

			if r.Method == http.MethodGet && r.URL.Query().Has("attributes") {
				w.WriteHeader(http.StatusNotFound)
				w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><Error><Code>NoSuchKey</Code><Message>Not found</Message></Error>`))
				return
			}

			if r.Method == http.MethodGet && r.URL.Query().Get("list-type") == "2" {
				prefix := r.URL.Query().Get("prefix")
				var sb strings.Builder
				sb.WriteString(`<?xml version="1.0" encoding="UTF-8"?><ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><IsTruncated>false</IsTruncated>`)
				if prefix == "exact1000" {
					for i := 0; i < 1000; i++ {
						sb.WriteString(fmt.Sprintf("<Contents><Key>exact1000/file-%04d.txt</Key><Size>4</Size><LastModified>2026-01-01T00:00:00.000Z</LastModified></Contents>", i))
					}
				}
				sb.WriteString(`</ListBucketResult>`)
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(sb.String()))
				return
			}

			if r.Method == http.MethodPost && r.URL.Query().Has("delete") {
				body, _ := io.ReadAll(r.Body)
				var req mockDeleteObjectsRequest
				if err := xml.Unmarshal(body, &req); err != nil {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				deleteBatchSizes = append(deleteBatchSizes, len(req.Objects))
				var sb strings.Builder
				sb.WriteString(`<?xml version="1.0" encoding="UTF-8"?><DeleteResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">`)
				for _, o := range req.Objects {
					sb.WriteString(fmt.Sprintf("<Deleted><Key>%s</Key></Deleted>", o.Key))
				}
				sb.WriteString(`</DeleteResult>`)
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(sb.String()))
				return
			}

			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()

		s3fs, err := NewS3Store(S3FSConfig{
			S3Region:    "us-east-1",
			S3Bucket:    "test-bucket",
			AltEndpoint: srv.URL,
			Credentials: S3FS_Static{S3Id: "test", S3Key: "test"},
		})
		if err != nil {
			t.Fatalf("NewS3Store failed: %v", err)
		}

		errs := s3fs.DeleteObjects(DeleteObjectInput{
			Paths: PathConfig{Paths: []string{"exact1000", "emptydir"}},
		})
		if len(errs) != 0 {
			t.Fatalf("Expected no errors, got: %v", errs)
		}
		if len(deleteBatchSizes) != 1 || deleteBatchSizes[0] != 1000 {
			t.Fatalf("Expected exactly 1 batch of 1000 deletes, got: %v", deleteBatchSizes)
		}
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
