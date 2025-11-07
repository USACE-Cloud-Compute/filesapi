package filesapi

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
)

var testout string = `&[{0 10  filestore_tests/10/  true 0001-01-01 00:00:00 +0000 UTC } {1 2D Unsteady Flow Hydraulics  filestore_tests/2D Unsteady Flow Hydraulics/  true 0001-01-01 00:00:00 +0000 UTC } {2 filestore_tests 0 filestore_tests  false 2023-10-06 21:02:48 +0000 UTC } {3 Archive.zip 73501923 filestore_tests .zip false 2023-10-18 19:56:14 +0000 UTC } {4 Archive2.zip 73501923 filestore_tests .zip false 2023-10-19 13:05:36 +0000 UTC } {5 image1.jpg 177142 filestore_tests .jpg false 2023-10-06 21:07:11 +0000 UTC }]`

var testProfile string = os.Getenv("TEST_PROFILE")
var testObjectString string = "HELLO WORLD"

func TestProfileCreds(t *testing.T) {
	config := S3FSConfig{
		Credentials: S3FS_Attached{
			Profile: testProfile,
		},
		S3Region: os.Getenv("AWS_REGION"),
		S3Bucket: os.Getenv("AWS_BUCKET"),
	}

	fs, err := NewFileStore(config)
	if err != nil {
		t.Fatal(err)
	}
	path := PathConfig{Path: os.Getenv("TEST_DIR")}
	dirs, err := fs.GetDir(path)
	if err != nil {
		t.Fatal(err)
	}
	out := fmt.Sprintln(dirs)
	//fmt.Println(out)
	//fmt.Println(testout)
	if out != testout {
		t.Fatalf(`Failed Test Profile Creds, got %s expected %s`, out, testout)
	}
	fmt.Println(out)
}

func TestStaticCreds(t *testing.T) {
	config := S3FSConfig{
		Credentials: S3FS_Static{
			S3Id:  os.Getenv("AWS_ACCESS_KEY_ID"),
			S3Key: os.Getenv("AWS_SECRET_ACCESS_KEY"),
		},
		S3Region:    os.Getenv("AWS_REGION"),
		S3Bucket:    os.Getenv("AWS_BUCKET"),
		AltEndpoint: os.Getenv("AWS_ENDPOINT"),
	}

	fs, err := NewFileStore(config)
	if err != nil {
		t.Fatal(err)
	}
	path := PathConfig{Path: os.Getenv("TEST_DIR")}
	dirs, err := fs.GetDir(path)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println(dirs)
}

func TestListDir(t *testing.T) {
	config := S3FSConfig{
		Credentials: S3FS_Attached{
			Profile: testProfile,
		},
		S3Region: os.Getenv("AWS_REGION"),
		S3Bucket: os.Getenv("AWS_BUCKET"),
	}

	fs, err := NewFileStore(config)
	if err != nil {
		t.Fatal(err)
	}
	path := PathConfig{Path: os.Getenv("TEST_DIR")}
	input := ListDirInput{
		Path:   path,
		Page:   1,
		Size:   5,
		Filter: "8",
	}
	dirs, err := fs.ListDir(input)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range *dirs {
		fmt.Println(d)
	}

}

func TestGetDir(t *testing.T) {
	config := S3FSConfig{
		Credentials: S3FS_Attached{
			Profile: testProfile,
		},
		S3Region: os.Getenv("AWS_REGION"),
		S3Bucket: os.Getenv("AWS_BUCKET"),
	}

	fs, err := NewFileStore(config)
	if err != nil {
		t.Fatal(err)
	}
	path := PathConfig{Path: os.Getenv("TEST_DIR")}
	dirs, err := fs.GetDir(path)
	if err != nil {
		t.Fatal(err)
	}
	out := fmt.Sprintln(dirs)
	fmt.Println(out)
	fmt.Println(testout)
	if out != testout {
		t.Fatalf(`Failed Test Profile Creds, got %s expected %s\n`, out, testout)
	}
	fmt.Println(out)
}

func TestGetObjectInfo(t *testing.T) {
	config := S3FSConfig{
		Credentials: S3FS_Attached{
			Profile: testProfile,
		},
		S3Region: os.Getenv("AWS_REGION"),
		S3Bucket: os.Getenv("AWS_BUCKET"),
	}

	fs, err := NewFileStore(config)
	if err != nil {
		t.Fatal(err)
	}
	path := PathConfig{Path: os.Getenv("TEST_TEXT_FILE")}
	//path := PathConfig{Path: "asdfasdf"}
	info, err := fs.GetObjectInfo(path)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println(info)
}

func TestGetObject(t *testing.T) {
	config := S3FSConfig{
		Credentials: S3FS_Attached{
			Profile: testProfile,
		},
		S3Region: os.Getenv("AWS_REGION"),
		S3Bucket: os.Getenv("AWS_BUCKET"),
	}

	fs, err := NewFileStore(config)
	if err != nil {
		t.Fatal(err)
	}

	path := PathConfig{Path: os.Getenv("TEST_TEXT_FILE")}
	reader, err := fs.GetObject(GetObjectInput{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	buf := new(strings.Builder)
	io.Copy(buf, reader)
	out := buf.String()
	if out != testObjectString {
		t.Fatalf(`Failed Test GetObject, got %s expected %s\n`, out, testObjectString)
	}
}

func TestGetObjectRetry(t *testing.T) {
	config := S3FSConfig{
		Credentials: S3FS_Attached{
			Profile: testProfile,
		},
		S3Region: os.Getenv("AWS_REGION"),
		S3Bucket: os.Getenv("AWS_BUCKET"),
	}

	fs, err := NewFileStore(config)
	if err != nil {
		t.Fatal(err)
	}
	path := PathConfig{Path: os.Getenv("TEST_TEXT_FILE")}
	retryer := Retryer[io.ReadCloser]{}
	reader, err := retryer.Send(func() (io.ReadCloser, error) {
		return fs.GetObject(GetObjectInput{Path: path})
	})
	if err != nil {
		t.Fatal(err)
	}
	buf := new(strings.Builder)
	io.Copy(buf, reader)
	out := buf.String()
	if out != testObjectString {
		t.Fatalf(`Failed Test GetObject, got %s expected %s\n`, out, testObjectString)
	}
}

func TestGetObjectSlice(t *testing.T) {
	config := S3FSConfig{
		Credentials: S3FS_Attached{
			Profile: testProfile,
		},
		S3Region: os.Getenv("AWS_REGION"),
		S3Bucket: os.Getenv("AWS_BUCKET"),
	}

	fs, err := NewFileStore(config)
	if err != nil {
		t.Fatal(err)
	}
	path := PathConfig{Path: os.Getenv("TEST_TEXT_FILE")}
	reader, err := fs.GetObject(GetObjectInput{Path: path, Range: "bytes=0-4"})
	if err != nil {
		t.Fatal(err)
	}
	buf := new(strings.Builder)
	io.Copy(buf, reader)
	out := buf.String()
	if out != testObjectString[:5] {
		t.Fatalf(`Failed Test GetObject, got %s expected %s`, out, testObjectString[:5])
	}
}

func TestResourceName(t *testing.T) {
	config := S3FSConfig{
		Credentials: S3FS_Attached{
			Profile: testProfile,
		},
		S3Region: os.Getenv("AWS_REGION"),
		S3Bucket: os.Getenv("AWS_BUCKET"),
	}

	fs, err := NewFileStore(config)
	if err != nil {
		t.Fatal(err)
	}
	name := fs.ResourceName()
	testname := os.Getenv("AWS_BUCKET")
	if name != testname {
		t.Fatalf(`Failed Test GetObject, got %s expected %s\n`, name, testname)
	}
}

func TestPutEmptyObject(t *testing.T) {
	config := S3FSConfig{
		Credentials: S3FS_Attached{
			Profile: testProfile,
		},
		S3Region: os.Getenv("AWS_REGION"),
		S3Bucket: os.Getenv("AWS_BUCKET"),
	}

	fs, err := NewFileStore(config)
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("")
	path := PathConfig{Path: os.Getenv("TEST_TEXT_FILE")}
	poi := PutObjectInput{
		Source: ObjectSource{
			Data: data,
		},
		Dest: path,
	}
	out, err := fs.PutObject(poi)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println(out.ETag)
}

func TestPutBytesReader(t *testing.T) {
	config := S3FSConfig{
		Credentials: S3FS_Attached{
			Profile: testProfile,
		},
		S3Region: os.Getenv("AWS_REGION"),
		S3Bucket: os.Getenv("AWS_BUCKET"),
	}

	fs, err := NewFileStore(config)
	if err != nil {
		t.Fatal(err)
	}
	data := []byte(" ")
	reader := bytes.NewReader(data)
	path := PathConfig{Path: os.Getenv("TEST_TEXT_FILE")}
	poi := PutObjectInput{
		Source: ObjectSource{Reader: reader},
		Dest:   path,
	}
	out, err := fs.PutObject(poi)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println(out.ETag)
}

func TestPutObjectBytes(t *testing.T) {
	config := S3FSConfig{
		Credentials: S3FS_Attached{
			Profile: testProfile,
		},
		S3Region: os.Getenv("AWS_REGION"),
		S3Bucket: os.Getenv("AWS_BUCKET"),
	}

	fs, err := NewFileStore(config)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(os.Getenv("TEST_TXT_SRC"))
	if err != nil {
		t.Fatal(err)
	}
	path := PathConfig{Path: os.Getenv("TEST_TEXT_FILE")}
	poi := PutObjectInput{
		Source: ObjectSource{Data: data},
		Dest:   path,
	}
	out, err := fs.PutObject(poi)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println(out.ETag)
}

func TestPutObjectReader(t *testing.T) {
	config := S3FSConfig{
		Credentials: S3FS_Attached{
			Profile: testProfile,
		},
		S3Region: os.Getenv("AWS_REGION"),
		S3Bucket: os.Getenv("AWS_BUCKET"),
	}

	fs, err := NewFileStore(config)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := os.Open(os.Getenv("TEST_PUT_SRC"))
	if err != nil {
		t.Fatal(err)
	}
	path := PathConfig{Path: os.Getenv("TEST_PUT_DEST")}
	poi := PutObjectInput{
		Source: ObjectSource{Reader: reader},
		Dest:   path,
	}
	out, err := fs.PutObject(poi)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println(out.ETag)
}

func TestPutObjectFile(t *testing.T) {
	config := S3FSConfig{
		Credentials: S3FS_Attached{
			Profile: testProfile,
		},
		S3Region: os.Getenv("AWS_REGION"),
		S3Bucket: os.Getenv("AWS_BUCKET"),
	}

	fs, err := NewFileStore(config)
	if err != nil {
		t.Fatal(err)
	}
	filepath := PathConfig{Path: os.Getenv("TEST_TXT_SRC")}

	path := PathConfig{Path: os.Getenv("TEST_TEXT_FILE") + ".f.txt"}
	poi := PutObjectInput{
		Source: ObjectSource{Filepath: filepath},
		Dest:   path,
	}
	out, err := fs.PutObject(poi)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println(out.ETag)
}

func TestCopyObject(t *testing.T) {
	config := S3FSConfig{
		Credentials: S3FS_Attached{
			Profile: testProfile,
		},
		S3Region: os.Getenv("AWS_REGION"),
		S3Bucket: os.Getenv("AWS_BUCKET"),
	}

	fs, err := NewFileStore(config)
	if err != nil {
		t.Fatal(err)
	}

	srcpath := PathConfig{Path: os.Getenv("TEST_COPY_SRC")}
	destpath := PathConfig{Path: os.Getenv("TEST_COPY_DEST")}
	err = fs.CopyObject(CopyObjectInput{
		Src:  srcpath,
		Dest: destpath,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestDeleteObject(t *testing.T) {
	config := S3FSConfig{
		Credentials: S3FS_Attached{
			Profile: testProfile,
		},
		S3Region: os.Getenv("AWS_REGION"),
		S3Bucket: os.Getenv("AWS_BUCKET"),
	}

	fs, err := NewFileStore(config)
	if err != nil {
		t.Fatal(err)
	}

	path := os.Getenv("TEST_COPY_DEST")

	errs := fs.DeleteObjects(DeleteObjectInput{
		Paths: PathConfig{Paths: []string{path}},
	})
	if len(errs) > 0 {
		t.Fatal(errs[0])
	}
}

func TestDeleteObjects(t *testing.T) {
	config := S3FSConfig{
		Credentials: S3FS_Attached{
			Profile: testProfile,
		},
		S3Region: os.Getenv("AWS_REGION"),
		S3Bucket: os.Getenv("AWS_BUCKET"),
	}

	fs, err := NewFileStore(config)
	if err != nil {
		t.Fatal(err)
	}

	path := PathConfig{Paths: []string{
		os.Getenv("TEST_DELETE_OBJECTS_SRC"),
		os.Getenv("TEST_COPY_DEST"),
	}}

	errs := fs.DeleteObjects(DeleteObjectInput{
		Paths: path,
	})
	if len(errs) > 0 {
		t.Fatal(errs)
	}
}

func TestWalk(t *testing.T) {
	config := S3FSConfig{
		Credentials: S3FS_Attached{
			Profile: testProfile,
		},
		S3Region:    os.Getenv("AWS_REGION"),
		S3Bucket:    os.Getenv("AWS_BUCKET"),
		AltEndpoint: os.Getenv("AWS_ENDPOINT"),
		MaxKeys:     2,
	}

	fs, err := NewFileStore(config)
	if err != nil {
		t.Fatal(err)
	}
	count := 0

	startpath := os.Getenv("TEST_DIR")

	wi := WalkInput{Path: PathConfig{Path: startpath}}

	err = fs.Walk(wi, func(path string, file os.FileInfo) error {
		fmt.Println(file.Name())
		count++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println(count)
}
