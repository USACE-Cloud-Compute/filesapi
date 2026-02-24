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
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
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
}

func (pc PathConfig) IsNil() bool {
	if len(pc.Paths) == 0 && pc.Path == "" {
		return true
	}
	return false
}

type FileOperationOutput struct {

	//AWS Etag for S3 results.  MD5 hash for file system operations
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
}

type CompletedObjectUploadConfig struct {

	//GUID for the file upload identifier
	UploadId string

	//Path to the object being uploaded into
	ObjectPath string

	//ETags for uploaded parts
	ChunkUploadIds []string
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
}

type PutObjectInput struct {
	Source     ObjectSource
	Dest       PathConfig
	Mutipart   bool
	PartSize   int
	Encryption EncryptionStrategy
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
		return os.Open(obs.Filepath.Path)
	}
	if obs.Data != nil {
		cl := int64(len(obs.Data))
		obs.ContentLength = &cl
		return bytes.NewReader(obs.Data), nil
	}
	return nil, errors.New("invalid objectsource configuration")
}

type DeleteObjectInput struct {
	Paths    PathConfig
	Progress ProgressFunction
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
}

type CopyObjectInput struct {
	Src      PathConfig
	Dest     PathConfig
	Progress ProgressFunction
}

type ListDirInput struct {
	Path   PathConfig
	Page   int
	Size   int32
	Filter string
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

	//initialize a multipart upload sessions
	InitializeObjectUpload(UploadConfig) (UploadResult, error)

	//write a chunk in a multipart upload session
	WriteChunk(UploadConfig) (UploadResult, error)

	//complete a multipart upload session
	CompleteObjectUpload(CompletedObjectUploadConfig) error

	//recursively deletes objects matching the path pattern
	DeleteObjects(DeleteObjectInput) []error

	//Walk a filestore starting at a given path
	//FileVisitFunction will be called for each object identified in the path
	Walk(WalkInput, FileVisitFunction) error
}

func NewFileStore(fsconfig any) (FileStore, error) {
	switch scType := fsconfig.(type) {
	case BlockFSConfig:
		config := fsconfig.(BlockFSConfig)
		if config.ChunkSize == 0 {
			config.ChunkSize = defaultChunkSize
		}
		fs := BlockFS{fsconfig.(BlockFSConfig)}
		return &fs, nil
	case S3FSConfig:
		var cfg aws.Config
		var err error
		maxKeys := DEFAULTMAXKEYS
		if scType.MaxKeys > 0 {
			maxKeys = scType.MaxKeys
		}
		delimiter := DEFAULTDELIMITER
		if scType.Delimiter != "" {
			delimiter = scType.Delimiter
		}
		loadOptions := []func(*config.LoadOptions) error{}
		if scType.AwsOptions != nil {
			loadOptions = append(loadOptions, scType.AwsOptions...)
		}
		loadOptions = append(loadOptions, config.WithRegion(scType.S3Region))
		/////AWS RETRY OPTION
		/*
			loadOptions = append(loadOptions, config.WithRetryer(func() aws.Retryer {
				return retry.AddWithMaxBackoffDelay(retry.NewStandard(), time.Second*5)
			}))
		*/
		////
		switch cred := scType.Credentials.(type) {
		case S3FS_Static:
			loadOptions = append(loadOptions, config.WithCredentialsProvider(
				credentials.NewStaticCredentialsProvider(cred.S3Id, cred.S3Key, ""),
			))
		case S3FS_Attached:
			//if attached credentials are used and cred.Profile=="", then the AWS default credential chain is invoked
			//otherwise add the profile.
			if cred.Profile != "" {
				loadOptions = append(loadOptions, config.WithSharedConfigProfile(cred.Profile))
			}
		case S3FS_Role:
			return nil, errors.New("Assumed rules are not supported")

		default:
			return nil, errors.New("Invalid S3 Credentials")
		}

		cfg, err = config.LoadDefaultConfig(
			context.TODO(),
			loadOptions...,
		)

		if err != nil {
			return nil, err
		}

		f := []func(o *s3.Options){}
		if scType.AltEndpoint != "" {
			//cant use BaseEndpoint resolver since the driver will modify the domain values
			//when the domain is not an IP address.  I'm leaving this line in but commented out for now
			//since I might add separate support for a BasedEndpoint
			//f = append(f, func(o *s3.Options) { o.BaseEndpoint = &scType.AltEndpoint })

			//the solution for a static endpoint is to implement our own static resolver
			endpointUrl := fmt.Sprintf("%s/%s", scType.AltEndpoint, scType.S3Bucket)
			f = append(f, func(o *s3.Options) { o.EndpointResolverV2 = &StaticResolver{Url: endpointUrl} })
		}
		s3Client := s3.NewFromConfig(cfg, f...)

		fs := S3FS{
			s3client:  s3Client,
			config:    &scType,
			delimiter: delimiter,
			maxKeys:   maxKeys,
		}
		return &fs, nil

	case MinioFSConfig:
		maxKeys := DEFAULTMAXKEYS
		if scType.MaxKeys > 0 {
			maxKeys = scType.MaxKeys
		}
		delimiter := DEFAULTDELIMITER
		if scType.Delimiter != "" {
			delimiter = scType.Delimiter
		}
		loadOptions := []func(*config.LoadOptions) error{}
		if scType.AwsOptions != nil {
			loadOptions = append(loadOptions, scType.AwsOptions...)
		}
		loadOptions = append(loadOptions, config.WithRegion(scType.S3Region))

		resolver := aws.EndpointResolverWithOptionsFunc(func(service, region string, options ...any) (aws.Endpoint, error) {
			return aws.Endpoint{
				PartitionID:       "aws",
				URL:               scType.HostAddress,
				SigningRegion:     scType.S3Region,
				HostnameImmutable: true,
			}, nil
		})

		var creds S3FS_Static
		var ok bool
		if creds, ok = scType.Credentials.(S3FS_Static); !ok {
			return nil, errors.New("Minio Configure requires static credentials")
		}

		loadOptions = append(
			loadOptions, config.WithRegion(scType.S3Region),
			config.WithEndpointResolverWithOptions(resolver),
			config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(creds.S3Id, creds.S3Key, "")),
		)

		cfg, err := config.LoadDefaultConfig(context.Background(), loadOptions...)
		if err != nil {
			return nil, err
		}
		s3Client := s3.NewFromConfig(cfg)
		s3Type := S3FSConfig(scType.S3FSConfig)
		fs := S3FS{
			s3client:  s3Client,
			config:    &s3Type,
			delimiter: delimiter,
			maxKeys:   maxKeys,
		}
		return &fs, nil

	default:
		return nil, errors.New(fmt.Sprintf("Invalid File System System Type Configuration: %v", scType))
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

func sanitizePath(path string) string {
	return strings.ReplaceAll(path, "..", "")
}

// @TODO this is duplicated!!!!
func buildUrl(urlparts []string, pathType PATHTYPE) string {
	var b strings.Builder
	t := "/%s"
	for _, p := range urlparts {
		p = strings.Trim(strings.ReplaceAll(p, "//", "/"), "/")
		//p = strings.Trim(p, "/")
		if p != "" {
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
