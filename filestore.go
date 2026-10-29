package filesapi

import (
	"bytes"
	"context"
	"crypto/md5"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type PATHTYPE int
type EncryptionStrategy string

const (
	FILE PATHTYPE = iota
	FOLDER
)

const (
	DEFAULTMAXKEYS    int32              = 1000
	DEFAULTDELIMITER  string             = "/"
	DefaultEncryption EncryptionStrategy = "DEFAULT"
	AesEncryption     EncryptionStrategy = "AES"
	VendorEncryption  EncryptionStrategy = "VENDOR"
)

var defaultChunkSize int64 = 10 * 1024 * 1024

type FileNotFoundError struct {
	path string
}

func (f *FileNotFoundError) Error() string {
	return fmt.Sprintf("File Not Found: %s\n", f.path)
}

// Path config a PATH or array of PATHS
// representing a resource.  Array of PATHS
// is used to support multi-file resources
// such as geospatial shape files
type PathConfig struct {
	Path  string
	Paths []string

	//optional context used for interface methods that take a PathConfig as a direct argument
	//context.Background is used if no context is provided
	Context context.Context
}

func (pc PathConfig) IsNil() bool {
	if len(pc.Paths) == 0 && pc.Path == "" {
		return true
	}
	return false
}

type FileOperationOutput struct {

	//AWS Etag for S3 results.
	// MD5 hash for file system operations
	// used specifically to identify uniqueness not for file integrity
	ETag string
}

type FileStoreResultObject struct {
	ID         int       `json:"id"`
	Name       string    `json:"fileName"`
	Size       string    `json:"size"`
	Path       string    `json:"filePath"`
	Type       string    `json:"type"`
	IsDir      bool      `json:"isdir"`
	Modified   time.Time `json:"modified"`
	ModifiedBy string    `json:"modifiedBy"`
}

type UploadConfig struct {

	//Path to the object being uploaded into
	ObjectPath string

	//upload chunk number
	ChunkId int32

	//GUID for the file upload identifier
	UploadId string

	//chunk data
	Data []byte

	//optional caller provided context. context.Background is used if no context is provided
	Context context.Context
}

type CompletedObjectUploadConfig struct {

	//GUID for the file upload identifier
	UploadId string

	//Path to the object being uploaded into
	ObjectPath string

	//ETags for uploaded parts
	ChunkUploadIds []string

	//optional caller provided context. context.Background is used if no context is provided
	Context context.Context
}

type UploadResult struct {
	ID         string `json:"id"`
	WriteSize  int    `json:"size"`
	IsComplete bool   `json:"isComplete"`
}

type FileVisitFunction func(path string, file os.FileInfo) error
type ProgressFunction func(pd ProgressData)

type ProgressData struct {
	Index int
	Max   int
	Value any
}

type GetObjectInput struct {

	//Path to resource
	Path PathConfig

	//Downloads the specified range bytes of an object. Uses rfc9110 syntax
	// https://www.rfc-editor.org/rfc/rfc9110.html#name-range
	//Note: Does not support multiple ranges in a single request
	Range string

	//optional caller provided context. context.Background is used if no context is provided
	Context context.Context
}

type PutObjectInput struct {
	Source     ObjectSource
	Dest       PathConfig
	Mutipart   bool
	PartSize   int
	Encryption EncryptionStrategy

	//optional caller provided context. context.Background is used if no context is provided
	Context            context.Context
	ContentDisposition *string
	ContentType        *string
}

type Range struct {
	Unit  string
	Start int64
	End   int64
}

type ObjectSource struct {

	//optional content length.  Will be determined automatically for byte slice sources (i.e. Data)
	ContentLength *int64

	//One of the next three sources must be provided
	//an existing io.ReadCloser
	Reader io.Reader

	//a byte slice of data
	Data []byte

	//a file path to a resource
	Filepath PathConfig

	//closer
	closer io.Closer
}

func (obs *ObjectSource) GetReader() (io.Reader, error) {
	if obs.Reader != nil {
		if br, ok := obs.Reader.(*bytes.Reader); ok {
			cl := br.Size()
			obs.ContentLength = &cl
		}
		return obs.Reader, nil
	}
	if obs.Filepath.Path != "" {
		f, err := os.Open(obs.Filepath.Path)
		if err != nil {
			return nil, err
		}
		obs.closer = f
		return f, nil
	}
	if obs.Data != nil {
		cl := int64(len(obs.Data))
		obs.ContentLength = &cl
		return bytes.NewReader(obs.Data), nil
	}
	return nil, errors.New("invalid objectsource configuration")
}

func (obs *ObjectSource) Close() error {
	if obs.closer != nil {
		err := obs.closer.Close()
		obs.closer = nil
		return err
	}
	return nil
}

type DeleteObjectInput struct {
	Paths    PathConfig
	Progress ProgressFunction

	//optional caller provided context. context.Background is used if no context is provided
	Context context.Context
}

type WalkInput struct {
	Path     PathConfig
	Progress ProgressFunction

	//file system only option
	//use filepath Walk instead of WalkDir
	FsUseWalk bool

	//if using WalkDir on a file system
	//when true will return a full fs.FileInfo struct
	//when false will return a DirEntryFileInfo that uses the DirEntry fields to create a FileInfo subset
	FsFullInfo bool

	//optional caller provided context. context.Background is used if no context is provided
	Context context.Context
}

type CopyObjectInput struct {
	Src      PathConfig
	Dest     PathConfig
	Progress ProgressFunction

	//optional caller provided context. context.Background is used if no context is provided
	Context context.Context
}

type ListDirInput struct {
	Path   PathConfig
	Page   int
	Size   int32
	Filter string

	//optional caller provided context. context.Background is used if no context is provided
	Context context.Context
}

type FileStore interface {

	//requests a slice of resources at a store directory
	//use instead of GetDir
	ListDir(input ListDirInput) (*[]FileStoreResultObject, error)

	//@Depricated
	//requests a slice of resources at a store directory
	GetDir(path PathConfig) (*[]FileStoreResultObject, error)

	//gets io/fs FileInfo for the resource
	GetObjectInfo(PathConfig) (fs.FileInfo, error)

	//gets a readcloser for the resource.
	//caller is responsible for closing the resource
	GetObject(GetObjectInput) (io.ReadCloser, error)

	//returns a resource name for the store.
	//refer to individual implementations for details on the resource name
	ResourceName() string

	//Put (upload) an object
	PutObject(PutObjectInput) (*FileOperationOutput, error)

	//copy an object in a filestore
	CopyObject(input CopyObjectInput) error

	//initialize a multipart upload session.
	//Note: For BlockFS direct-write uploads, upload tracking, chunk ordering,
	//distributed locking, retries, and stale upload cleanup are managed by the caller.
	InitializeObjectUpload(UploadConfig) (UploadResult, error)

	//write a chunk in a multipart upload session.
	//Writes chunk bytes directly to the calculated byte offset.
	WriteChunk(UploadConfig) (UploadResult, error)

	//complete a multipart upload session.
	//Finalizes the uploaded object, committing it to the final destination path.
	CompleteObjectUpload(CompletedObjectUploadConfig) error

	//recursively deletes objects matching the path pattern
	DeleteObjects(DeleteObjectInput) []error

	//Walk a filestore starting at a given path
	//FileVisitFunction will be called for each object identified in the path
	Walk(WalkInput, FileVisitFunction) error
}

// Deprecated: NewFileStore is deprecated. Use NewFileSystemStore or NewS3Store instead.
func NewFileStore(fsconfig any) (FileStore, error) {
	switch scType := fsconfig.(type) {
	case BlockFSConfig:
		return NewFileSystemStore(scType)
	case S3FSConfig:
		return NewS3Store(scType)
	default:
		return nil, fmt.Errorf("invalid File System System Type Configuration: %v", scType)
	}
}

//config.WithSharedConfigProfile("my-account-name"))

type PathParts struct {
	Parts []string
}

func (pp PathParts) ToPath(additionalParts ...string) string {
	parts := append(pp.Parts, additionalParts...)
	return buildUrl(parts, FOLDER)
}

func (pp PathParts) ToFilePath(additionalParts ...string) string {
	parts := append(pp.Parts, additionalParts...)
	return buildUrl(parts, FILE)
}

func sanitizePath(p string) string {
	cleaned := path.Clean("/" + strings.TrimSpace(p))
	return cleaned
}

// @TODO this is duplicated!!!!
func buildUrl(urlparts []string, pathType PATHTYPE) string {
	var b strings.Builder
	t := "/%s"
	for _, p := range urlparts {
		p = strings.Trim(filepath.ToSlash(p), "/")
		p = path.Clean("/" + p)
		p = strings.TrimPrefix(p, "/")
		if p != "" && p != "." {
			fmt.Fprintf(&b, t, p)
		}
	}
	if pathType == FOLDER {
		fmt.Fprintf(&b, "%s", "/")
	}
	return sanitizePath(b.String())
}

func getFileMd5(f *os.File) (string, error) {
	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

func isDir(path string) bool {
	fi, err := os.Stat(path)
	if err != nil {
		return false
	}
	return fi.Mode().IsDir()
}

func parseRange(input string) (Range, error) {
	re := regexp.MustCompile(`^([a-zA-Z]+)=(\d+)-(\d+)$`)
	matches := re.FindStringSubmatch(input)
	r := Range{}

	if len(matches) != 4 {
		return r, fmt.Errorf("invalid range input format")
	}
	r.Unit = matches[1]
	start, err := strconv.ParseInt(matches[2], 10, 64)
	if err != nil {
		return r, err
	}
	r.Start = start
	end, err := strconv.ParseInt(matches[3], 10, 64)
	if err != nil {
		return r, err
	}
	r.End = end
	return r, nil
}
