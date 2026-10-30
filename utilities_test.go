package filesapi

import (
	"fmt"
	"testing"
)

var testKey []byte = []byte("asdfasdfasdfasdfasdfasdfasdfasdf")

func TestSignUrl(t *testing.T) {
	uri := "https://test.com/path1/path2?param1=1234&param2=abcd"
	options := PresignInputOptions{
		Uri:        uri,
		SigningKey: testKey,
		Expiration: 60,
	}
	signedurl, err := PresignObject(options)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println(signedurl)
}

func TestValidateUrl(t *testing.T) {
	expiration := 60
	
	options := PresignInputOptions{
		Uri:        "https://test.com/path1/path2?param1=1234&param2=abcd",
		SigningKey: testKey,
		Expiration: expiration,
		Credential: "test-credential",
	}
	signedurl, err := PresignObject(options)
	if err != nil {
		t.Fatal(err)
	}

	verifyOptions := PresignInputOptions{
		Uri:        signedurl,
		SigningKey: testKey,
	}

	if VerifySignedObject(verifyOptions) {
		fmt.Println("VALID!")
	} else {
		t.Fatal("NOT VALID")
	}
}
