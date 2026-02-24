package filesapi

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
)

var pathError *fs.PathError

// @TODO this is kind of clunky.  BlockFSConfig is only used in NewFileStore as a type case so we know to create a Block File Store
// as of now I don't actually need any config properties
type BlockFSConfig struct {
	ChunkSize int64
}

type BlockFS struct {
	Config BlockFSConfig
}

func (b *BlockFS) GetObjectInfo(path PathConfig) (fs.FileInfo, error) {
	file, err := os.Stat(path.Path)
	if errors.As(err, &pathError) {
		err = &FileNotFoundError{path.Path}
	}
	return file, err
}

func (b *BlockFS) ListDir(input ListDirInput) (*[]FileStoreResultObject, error) {
	dirContents, err := ioutil.ReadDir(input.Path.Path)
	if err != nil {
		return nil, err
	}
	objects := make([]FileStoreResultObject, len(dirContents))
	for i, f := range dirContents {
		size := strconv.FormatInt(f.Size(), 10)
		objects[i] = FileStoreResultObject{
			ID:         i,
			Name:       f.Name(),
			Size:       size,
			Path:       input.Path.Path,
			Type:       filepath.Ext(f.Name()),
			IsDir:      f.IsDir(),
			Modified:   f.ModTime(),
			ModifiedBy: "",
		}
	}
	return &objects, nil
}

func (b *BlockFS) GetDir(path PathConfig) (*[]FileStoreResultObject, error) {
	dirContents, err := ioutil.ReadDir(path.Path)
	if err != nil {
		return nil, err
	}
	objects := make([]FileStoreResultObject, len(dirContents))
	for i, f := range dirContents {
		size := strconv.FormatInt(f.Size(), 10)
		objects[i] = FileStoreResultObject{
			ID:         i,
			Name:       f.Name(),
			Size:       size,
			Path:       path.Path,
			Type:       filepath.Ext(f.Name()),
			IsDir:      f.IsDir(),
			Modified:   f.ModTime(),
			ModifiedBy: "",
		}
	}
	return &objects, nil
}

func (b *BlockFS) ResourceName() string {
	return ""
}

func (b *BlockFS) GetObject(goi GetObjectInput) (io.ReadCloser, error) {
	reader, err := os.Open(goi.Path.Path)
	if goi.Range == "" || err != nil {
		if errors.As(err, &pathError) {
			err = &FileNotFoundError{goi.Path.Path}
		}
		return reader, err
	}
	readRange, err := parseRange(goi.Range)
	if err != nil {
		return nil, err
	}
	buf := make([]byte, readRange.End-readRange.Start+1)
	_, err = reader.ReadAt(buf, readRange.Start)
	if err != nil && err != io.EOF {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(buf)), nil
}

func (b *BlockFS) PutObject(poi PutObjectInput) (*FileOperationOutput, error) {
	foo := FileOperationOutput{}
	var err error
	var src io.Reader

	//get the src reader
	switch {
	case poi.Source.Data != nil && len(poi.Source.Data) == 0:
		err = os.MkdirAll(filepath.Dir(poi.Dest.Path), os.ModePerm)
		return &foo, err
	case poi.Source.Data != nil:
		src = bytes.NewReader(poi.Source.Data)
	case poi.Source.Filepath.Path != "":
		f, err := os.OpenFile(poi.Source.Filepath.Path, os.O_RDONLY, os.ModePerm)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		src = f
	case poi.Source.Reader != nil:
		src = poi.Source.Reader
	}

	//opena and write to the destination
	f, err := os.OpenFile(poi.Dest.Path, os.O_RDWR|os.O_CREATE, os.ModePerm)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	_, err = io.Copy(f, src)
	if err != nil {
		return nil, err
	}

	md5, err := getFileMd5(f)
	if err != nil {
		return nil, err
	}
	foo.ETag = md5
	return &foo, err
}

func (b *BlockFS) CopyObject(coi CopyObjectInput) error {
	src, err := os.Open(coi.Src.Path)
	if err != nil {
		return err
	}
	defer src.Close()

	dest, err := os.Create(coi.Dest.Path)
	if err != nil {
		return err
	}
	defer dest.Close()

	_, err = io.Copy(dest, src)
	return err
}

func (b *BlockFS) DeleteObjects(doi DeleteObjectInput) []error {
	var err error
	for i, p := range doi.Paths.Paths {
		if isDir(p) {
			err = os.RemoveAll(p)
		} else {
			err = os.Remove(p)
		}
		if doi.Progress != nil {
			doi.Progress(ProgressData{
				Index: i,
				Max:   -1,
				Value: p,
			})
		}
	}
	return []error{err}
}

func (b *BlockFS) InitializeObjectUpload(u UploadConfig) (UploadResult, error) {
	fmt.Println(u.ObjectPath)
	result := UploadResult{}
	os.MkdirAll(filepath.Dir(u.ObjectPath), os.ModePerm) //@TODO incomplete
	f, err := os.Create(u.ObjectPath)                    //@TODO incomplete
	if err != nil {
		return result, err
	}
	_ = f.Close()
	result.ID = uuid.New().String()
	return result, nil
}

func (b *BlockFS) WriteChunk(u UploadConfig) (UploadResult, error) {
	result := UploadResult{}
	//var err error
	mutex := &sync.Mutex{}
	mutex.Lock()
	defer mutex.Unlock()
	f, err := os.OpenFile(u.ObjectPath, os.O_WRONLY|os.O_CREATE, 0644) //@TODO incomplete
	if err != nil {
		return result, err
	}
	defer f.Close()
	_, err = f.WriteAt(u.Data, (int64(u.ChunkId) * b.Config.ChunkSize))
	result.WriteSize = len(u.Data)
	return result, err
}

func (b *BlockFS) CompleteObjectUpload(u CompletedObjectUploadConfig) error {
	//return md5 hash for file
	return nil
}

func (b *BlockFS) Walk(input WalkInput, visitorFunction FileVisitFunction) error {
	if input.FsUseWalk {
		err := filepath.Walk(input.Path.Path,
			func(path string, fileinfo os.FileInfo, err error) error {
				if err != nil {
					return err
				}
				err = visitorFunction(path, fileinfo)
				return err
			})
		return err
	} else {
		err := filepath.WalkDir(input.Path.Path,
			func(path string, direntry fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				//pass a full FileInfo to the visitor fucntion
				if input.FsFullInfo {
					info, err := direntry.Info()
					if err != nil {
						return err
					}
					err = visitorFunction(path, info)
				} else {
					err = visitorFunction(path, &DirEntryFileInfo{DirEntry: direntry})
				}
				return err
			})
		return err
	}
}

type DirEntryFileInfo struct {
	DirEntry fs.DirEntry
	info     fs.FileInfo
	// size     int64
	// modTime  time.Time
	// fsSys    any
	// fileMode fs.FileMode
}

func (defi DirEntryFileInfo) Name() string {
	return defi.DirEntry.Name()
}

func (defi DirEntryFileInfo) Size() int64 {
	if defi.info != nil {
		return defi.info.Size()
	}
	return -1
}

func (defi DirEntryFileInfo) Mode() fs.FileMode {
	if defi.info == nil {
		return defi.DirEntry.Type()
	}
	return defi.info.Mode()
}

func (defi DirEntryFileInfo) ModTime() time.Time {
	if defi.info != nil {
		return defi.info.ModTime()
	}
	return time.Time{}
}

func (defi DirEntryFileInfo) IsDir() bool {
	return defi.DirEntry.IsDir()
}

func (defi DirEntryFileInfo) Sys() any {
	if defi.info != nil {
		return defi.info.Sys()
	}
	return nil
}

func (defi *DirEntryFileInfo) GetInfo() error {
	fi, err := defi.DirEntry.Info()
	if err != nil {
		return err
	}
	defi.info = fi
	return nil
}
