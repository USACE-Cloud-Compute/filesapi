package filesapi

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	b64 "encoding/base64"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"time"
)

const (
	signatureQueryName  string = "X-Amz-Signature"
	expirationQueryName string = "X-Amz-Expiration"
	credentialQueryName string = "X-Amz-Credential"
	timeQueryName       string = "X-Amz-Date"
	timeFormat          string = "20060102T150405Z"
	maxExpiration       int    = 86400 * 30 //30 days
)

var fileNotFoundError *FileNotFoundError

type Retryer[T any] struct {

	//Max retry attempts
	MaxAttempts int

	//Max backoff in seconds
	MaxBackoff float64

	//base value for exponential backoff (usually 2)
	//https://docs.aws.amazon.com/sdkref/latest/guide/feature-retry-behavior.html
	R float64
}

// Send function for platform agnostic retry with exponential backoff and jitter
// based on : https://docs.aws.amazon.com/sdkref/latest/guide/feature-retry-behavior.html
func (r Retryer[T]) Send(sendFunction func() (T, error)) (T, error) {
	attempts := 0
	for {
		t, err := sendFunction()
		if err == nil || attempts > r.MaxAttempts {
			return t, err
		}
		b := rand.Float64() //@TODO should probably use crypto random.....
		secondsToSleep := math.Min(b*math.Pow(r.R, float64(attempts)), r.MaxBackoff)
		time.Sleep(time.Second * time.Duration(secondsToSleep))
		attempts++
	}
}

type CountInput struct {

	//the filestore that will be walked
	FileStore FileStore

	//the starting directory
	DirPath PathConfig

	//an optional regular expression pattern for counting specific occurences of files
	Pattern string
}

// Counts the number of files matching an optional pattern.
// It accomplishes this by recursively walking the file system
// starting at the dirpath
func Count(ci CountInput) (int64, error) {
	var count int64 = 0
	var err error
	if ci.Pattern == "" {
		err = ci.FileStore.Walk(WalkInput{Path: ci.DirPath}, func(path string, file os.FileInfo) error {
			count++
			return nil
		})

	} else {
		var r *regexp.Regexp
		r, err := regexp.Compile("")
		if err != nil {
			return -1, fmt.Errorf("Failed to compile file search pattern: %s\n", err)
		}
		err = ci.FileStore.Walk(WalkInput{Path: ci.DirPath}, func(path string, file os.FileInfo) error {
			if r.MatchString(path) {
				count++
			}
			return nil
		})
	}

	if err != nil {
		return -1, err
	}
	return count, nil
}

type PresignInputOptions struct {

	//full uri, including query params, to sign or verify
	Uri string

	//HMAC 256 signing key
	SigningKey []byte

	//Expiration time in seconds
	Expiration int

	//X-Amz-Credential
	Credential string
}

// Signs a uri object.  Object should be a full uri with query parameters.
// method returns a new uri with the signature included.
// Signing parameter names are borrowed from AWS and use their spec:
// https://docs.aws.amazon.com/AmazonS3/latest/API/sigv4-query-string-auth.html
func PresignObject(options PresignInputOptions) (string, error) {
	if options.Expiration > maxExpiration {
		return "", errors.New("Expiration time too long")
	}
	uri, err := url.Parse(options.Uri)
	if err != nil {
		return "", err
	}
	qp := uri.Query()
	qp.Add(timeQueryName, time.Now().UTC().Format(timeFormat))
	qp.Add(expirationQueryName, strconv.Itoa(options.Expiration))
	qp.Add(credentialQueryName, b64.StdEncoding.EncodeToString([]byte(options.Credential)))
	uri.RawQuery = qp.Encode()
	signature, err := sign([]byte(uri.String()), options.SigningKey)
	sEnc := b64.StdEncoding.EncodeToString(signature)
	sUrl := url.QueryEscape(sEnc)
	qp.Add(signatureQueryName, sUrl)
	uri.RawQuery = qp.Encode()
	return uri.String(), nil
}

func sign(data []byte, signingKey []byte) ([]byte, error) {
	mac := hmac.New(sha256.New, signingKey)
	_, err := mac.Write(data)
	if err != nil {
		return nil, err
	}
	return mac.Sum(nil), nil
}

// verify a signed object.  Returns a boolean.  True if verified, false on any error
// or validation failure
func VerifySignedObject(options PresignInputOptions) bool {
	uri, err := url.Parse(options.Uri)
	if err != nil {
		return false
	}
	sigok := verifySignature(uri, options.SigningKey)
	timeok := verifyExpiration(uri.Query())
	return sigok && timeok
}

func verifySignature(uri *url.URL, key []byte) bool {
	qp := uri.Query()
	urlSignature := qp.Get(signatureQueryName)
	b64signature, err := url.QueryUnescape(urlSignature)
	if err != nil {
		return false
	}
	signature, err := b64.StdEncoding.DecodeString(b64signature)
	if err != nil {
		return false
	}
	qp.Del(signatureQueryName)
	uri.RawQuery = qp.Encode()
	expectedSignature, err := sign([]byte(uri.String()), key)
	if err != nil {
		return false
	}
	return bytes.Equal(signature, expectedSignature)
}

func verifyExpiration(qp url.Values) bool {
	t, err := time.Parse(timeFormat, qp.Get(timeQueryName))
	if err != nil {
		return false
	}
	d, err := strconv.Atoi(qp.Get(expirationQueryName))
	if err != nil {
		return false
	}
	t = t.Add(time.Second * time.Duration(d))
	return t.After(time.Now().UTC())

}

func FileExists(fs FileStore, path string) bool {
	_, err := fs.GetObjectInfo(PathConfig{Path: path})
	return !errors.As(err, &fileNotFoundError)
}

func Ref[T any](t T) *T {
	return &t
}

func CopyWithContext(ctx context.Context, dst io.Writer, src io.Reader) (int64, error) {
	// If the source reader supports a Close method, we can stop io.Copy
	// mid-stream by closing the source when the context is cancelled.
	if closer, ok := src.(io.ReadCloser); ok {
		stop := context.AfterFunc(ctx, func() {
			closer.Close()
		})
		defer stop() // Clean up the AfterFunc registration
	}

	n, err := io.Copy(dst, src)

	// If the context was canceled, return the context error instead of a generic "closed" error
	if ctx.Err() != nil {
		return n, ctx.Err()
	}
	return n, err
}
