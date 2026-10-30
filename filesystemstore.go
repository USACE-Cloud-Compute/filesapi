package filesapi

import (
	"bytes"
	"crypto/md5"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"io/ioutil"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

var pathError *fs.PathError

type internalOptions struct {
	logger *slog.Logger
}

type Option func(*internalOptions)

func WithLogger(l *slog.Logger) Option {
	return func(o *internalOptions) {
		o.logger = l
	}
}

type BlockFSConfig struct {
	ChunkSize int64
	BaseDir   string
}

type BlockFS struct {
	Config BlockFSConfig
	logger *slog.Logger
	mutex  *sync.Mutex
}

func NewFileSystemStore(cfg BlockFSConfig, opts ...Option) (*BlockFS, error) {
	if cfg.ChunkSize == 0 {
		cfg.ChunkSize = defaultChunkSize
	}

	options := internalOptions{}
	for _, opt := range opts {
		opt(&options)
	}

	if options.logger == nil {
		options.logger = slog.New(slog.NewTextHandler(os.Stdout, nil))
	}

	fs := &BlockFS{
		Config: cfg,
		logger: options.logger,
		mutex:  &sync.Mutex{},
	}

	return fs, nil
}

// verifyPath checks whether p resides within b.Config.BaseDir.
// If BaseDir is unset, it allows the path.
func (b *BlockFS) verifyPath(p string) error {
	if b.Config.BaseDir == "" {
		return nil
	}

	// Normalize the path to handle trailing slashes and redundant separators
	p = filepath.Clean(p)

	// 1. Resolve the real, physical path of the base directory
	base, err := filepath.Abs(filepath.Clean(b.Config.BaseDir))
	if err != nil {
		return fmt.Errorf("invalid base directory: %w", err)
	}
	base, err = filepath.EvalSymlinks(base)
	if err != nil {
		return fmt.Errorf("invalid base directory: %w", err)
	}

	var target string
	// 2. Try to resolve the real path of the target
	target, err = filepath.EvalSymlinks(p)
	if err != nil {
		if os.IsNotExist(err) {
			// 3. If the file doesn't exist, resolve the parent directory instead
			parent := filepath.Dir(p)
			resolvedParent, err := filepath.EvalSymlinks(parent)
			if err != nil {
				// If the parent doesn't exist, we fallback to the absolute path
				target, err = filepath.Abs(p)
				if err != nil {
					return fmt.Errorf("invalid target path: %w", err)
				}
			} else {
				// Construct the target path using the resolved physical parent
				target = filepath.Join(resolvedParent, filepath.Base(p))
			}
		} else {
			// It's a real error (e.g., permission denied), so return it
			return fmt.Errorf("failed to resolve symlinks for %q: %w", p, err)
		}
	}

	// 4. Verify the resolved physical path is still within the base directory
	rel, err := filepath.Rel(base, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("path traversal violation: %q is outside base directory %q", p, base)
	}

	return nil
}

func (b *BlockFS) GetObjectInfo(path PathConfig) (fs.FileInfo, error) {
	if err := b.verifyPath(path.Path); err != nil {
		return nil, err
	}
	file, err := os.Stat(path.Path)
	if errors.As(err, &pathError) {
		err = &FileNotFoundError{path.Path}
	}
	return file, err
}

func (b *BlockFS) ListDirOld(input ListDirInput) (*[]FileStoreResultObject, error) {
	if err := b.verifyPath(input.Path.Path); err != nil {
		return nil, err
	}
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

func (b *BlockFS) ListDir(input ListDirInput) (*[]FileStoreResultObject, error) {
	if err := b.verifyPath(input.Path.Path); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(input.Path.Path)
	if err != nil {
		return nil, err
	}

	objects := make([]FileStoreResultObject, len(entries))
	for i, entry := range entries {
		// os.DirEntry requires calling Info() to access Size and ModTime
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}

		size := strconv.FormatInt(info.Size(), 10)
		objects[i] = FileStoreResultObject{
			ID:         i,
			Name:       entry.Name(),
			Size:       size,
			Path:       input.Path.Path,
			Type:       filepath.Ext(entry.Name()),
			IsDir:      entry.IsDir(),
			Modified:   info.ModTime(),
			ModifiedBy: "",
		}
	}
	return &objects, nil
}

func (b *BlockFS) GetDirOld(path PathConfig) (*[]FileStoreResultObject, error) {
	if err := b.verifyPath(path.Path); err != nil {
		return nil, err
	}
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

func (b *BlockFS) GetDir(path PathConfig) (*[]FileStoreResultObject, error) {
	if err := b.verifyPath(path.Path); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(path.Path)
	if err != nil {
		return nil, err
	}

	objects := make([]FileStoreResultObject, len(entries))
	for i, entry := range entries {
		// os.DirEntry requires calling Info() to access Size and ModTime
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}

		size := strconv.FormatInt(info.Size(), 10)
		objects[i] = FileStoreResultObject{
			ID:         i,
			Name:       entry.Name(),
			Size:       size,
			Path:       path.Path,
			Type:       filepath.Ext(entry.Name()),
			IsDir:      entry.IsDir(),
			Modified:   info.ModTime(),
			ModifiedBy: "",
		}
	}
	return &objects, nil
}

func (b *BlockFS) ResourceName() string {
	return ""
}

func (b *BlockFS) GetObject(goi GetObjectInput) (io.ReadCloser, error) {
	if err := b.verifyPath(goi.Path.Path); err != nil {
		return nil, err
	}
	reader, err := os.Open(goi.Path.Path)
	if err != nil {
		if errors.As(err, &pathError) {
			err = &FileNotFoundError{goi.Path.Path}
		}
		return nil, err
	}

	// CASE 1: Full file requested.
	// Return the handle. The caller will call reader.Close().
	if goi.Range == "" {
		return reader, nil
	}

	// CASE 2: Range requested.
	// We must close the file ourselves once we are done reading the chunk.
	defer reader.Close()
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
	if err := b.verifyPath(poi.Dest.Path); err != nil {
		return nil, err
	}
	foo := FileOperationOutput{}

	//handle case for creating a new directory path
	if poi.Source.Data != nil && len(poi.Source.Data) == 0 {
		err := os.MkdirAll(filepath.Dir(poi.Dest.Path), os.ModePerm)
		return &foo, err
	}

	src, err := poi.Source.GetReader()
	if err != nil {
		return nil, fmt.Errorf("unable to get the source reader: %s", err)
	}
	defer poi.Source.Close()

	//open and write to the destination
	f, err := os.OpenFile(poi.Dest.Path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, os.ModePerm)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	// write to the file and compute the hash simultaneously
	h := md5.New()
	mw := io.MultiWriter(f, h)

	_, err = CopyWithContext(poi.Context, mw, src)
	if err != nil {
		return nil, err
	}

	foo.ETag = fmt.Sprintf("%x", h.Sum(nil))
	return &foo, nil
}

func (b *BlockFS) CopyObject(coi CopyObjectInput) error {
	if err := b.verifyPath(coi.Src.Path); err != nil {
		return err
	}
	if err := b.verifyPath(coi.Dest.Path); err != nil {
		return err
	}
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

	_, err = CopyWithContext(coi.Context, dest, src)
	//_, err = io.Copy(dest, src)
	return err
}

func (b *BlockFS) DeleteObjects(doi DeleteObjectInput) []error {
	for _, p := range doi.Paths.Paths {
		if err := b.verifyPath(p); err != nil {
			return []error{err}
		}
	}
	var err error
	for i, p := range doi.Paths.Paths {
		if doi.Context != nil {
			select {
			case <-doi.Context.Done():
				return []error{doi.Context.Err()}
			default:
			}
		}
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

func (b *BlockFS) getStagingPath(objectPath, uploadID string) string {
	if uploadID == "" {
		return objectPath
	}
	dir := filepath.Dir(objectPath)
	base := filepath.Base(objectPath)
	return filepath.Join(dir, fmt.Sprintf(".%s.%s.tmp", base, uploadID))
}

func (b *BlockFS) findUploadPath(objectPath, uploadID string) (string, error) {
	if uploadID != "" {
		staging := b.getStagingPath(objectPath, uploadID)
		if _, err := os.Stat(staging); err == nil {
			return staging, nil
		}
	}

	// If no staging file with explicit uploadID, check for any staging file for objectPath
	dir := filepath.Dir(objectPath)
	base := filepath.Base(objectPath)
	prefix := fmt.Sprintf(".%s.", base)
	suffix := ".tmp"

	entries, err := os.ReadDir(dir)
	if err == nil {
		for _, entry := range entries {
			name := entry.Name()
			if strings.HasPrefix(name, prefix) && strings.HasSuffix(name, suffix) {
				return filepath.Join(dir, name), nil
			}
		}
	}

	// Fallback to direct object path
	if _, err := os.Stat(objectPath); err == nil {
		return objectPath, nil
	}

	if uploadID != "" {
		return b.getStagingPath(objectPath, uploadID), nil
	}
	return objectPath, nil
}

func (b *BlockFS) InitializeObjectUpload(u UploadConfig) (UploadResult, error) {
	if err := b.verifyPath(u.ObjectPath); err != nil {
		return UploadResult{}, err
	}
	if u.Context != nil {
		if err := u.Context.Err(); err != nil {
			return UploadResult{}, err
		}
	}

	result := UploadResult{}
	uploadID := u.UploadId
	if uploadID == "" {
		uploadID = uuid.New().String()
	}
	result.ID = uploadID

	stagingPath := b.getStagingPath(u.ObjectPath, uploadID)
	if err := os.MkdirAll(filepath.Dir(stagingPath), os.ModePerm); err != nil {
		return result, err
	}

	b.mutex.Lock()
	defer b.mutex.Unlock()

	f, err := os.OpenFile(stagingPath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return result, err
	}
	_ = f.Close()

	return result, nil
}

func (b *BlockFS) WriteChunk(u UploadConfig) (UploadResult, error) {
	if err := b.verifyPath(u.ObjectPath); err != nil {
		return UploadResult{}, err
	}
	if u.Context != nil {
		if err := u.Context.Err(); err != nil {
			return UploadResult{}, err
		}
	}

	result := UploadResult{}
	targetPath, err := b.findUploadPath(u.ObjectPath, u.UploadId)
	if err != nil {
		return result, err
	}

	b.mutex.Lock()
	defer b.mutex.Unlock()

	f, err := os.OpenFile(targetPath, os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		return result, err
	}
	defer f.Close()

	_, err = f.WriteAt(u.Data, int64(u.ChunkId)*b.Config.ChunkSize)
	result.WriteSize = len(u.Data)
	result.ID = u.UploadId
	return result, err
}

func (b *BlockFS) CompleteObjectUpload(u CompletedObjectUploadConfig) error {
	if err := b.verifyPath(u.ObjectPath); err != nil {
		return err
	}
	if u.Context != nil {
		if err := u.Context.Err(); err != nil {
			return err
		}
	}

	stagingPath, err := b.findUploadPath(u.ObjectPath, u.UploadId)
	if err != nil {
		return err
	}

	b.mutex.Lock()
	defer b.mutex.Unlock()

	if stagingPath != u.ObjectPath {
		if _, err := os.Stat(stagingPath); err == nil {
			if err := os.MkdirAll(filepath.Dir(u.ObjectPath), os.ModePerm); err != nil {
				return err
			}
			if err := os.Rename(stagingPath, u.ObjectPath); err != nil {
				return err
			}
		} else if !os.IsNotExist(err) {
			return err
		} else {
			if _, targetErr := os.Stat(u.ObjectPath); targetErr != nil {
				return fmt.Errorf("upload staging file not found: %w", err)
			}
		}
	}

	f, err := os.Open(u.ObjectPath)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = getFileMd5(f)
	return err
}

func (b *BlockFS) Walk(input WalkInput, visitorFunction FileVisitFunction) error {
	if err := b.verifyPath(input.Path.Path); err != nil {
		return err
	}
	if input.FsUseWalk {
		err := filepath.Walk(input.Path.Path,
			func(path string, fileinfo os.FileInfo, err error) error {
				if err != nil {
					return err
				}
				if input.Context != nil {
					select {
					case <-input.Context.Done():
						return input.Context.Err()
					default:
					}
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
