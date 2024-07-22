package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/textproto"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

var s3Actions S3Actions
var s3Bucket string

func initAWS() {
	s3Bucket = os.Getenv("S3_BUCKET")
	if s3Bucket == "" {
		log.Fatal("S3_BUCKET variable not set")
	}

	// Initialize the Amazon Web Services SDK.
	sdkConfig, err := config.LoadDefaultConfig(context.TODO())
	if err != nil {
		log.Fatal("Could not load default AWS configuration:", err)
	}

	s3Actions = S3Actions{
		S3Client: s3.NewFromConfig(sdkConfig),
		S3Manager: manager.NewUploader(s3.NewFromConfig(sdkConfig), func(u *manager.Uploader) {
			// Define a strategy that will buffer the maximum upload size for files.
			u.BufferProvider = manager.NewBufferedReadSeekerWriteToPool(maxUploadSize)
		}),
	}
}

// A FileObject is the structure we store in the S3 bucket. We encode the structure as a gob before uploading.
type FileObject struct {
	Filename string
	Header   textproto.MIMEHeader
	Size     int64
	Modtime  time.Time // Last modified
	Contents []byte
}

// NewFileObject creates a FileObject by opening and reading the fields from a multipart FileHeader uploaded by a user.
func NewFileObject(fileHeader *multipart.FileHeader, modtime time.Time) (*FileObject, error) {
	object := new(FileObject)
	object.Filename = fileHeader.Filename
	object.Header = fileHeader.Header
	object.Size = fileHeader.Size
	object.Modtime = modtime

	mpFile, err := fileHeader.Open()
	if err != nil {
		return nil, fmt.Errorf("failed to open multipart FileHeader: %v", err)
	}
	contents, err := io.ReadAll(mpFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read multipart File contents: %v", err)
	}
	object.Contents = contents

	return object, nil
}

// S3Actions wraps S3 service actions.
type S3Actions struct {
	S3Client  *s3.Client
	S3Manager *manager.Uploader
}

// UploadLargeObject uses an upload manager to upload data to an object in a bucket.
// The upload manager breaks large data into parts and uploads the parts concurrently.
//
// Code modified from: https://docs.aws.amazon.com/code-library/latest/ug/go_2_s3_code_examples.html#heading:r4v:
func (actor S3Actions) UploadObject(ctx context.Context, bucket string, key string, contents []byte) (string, error) {
	var outKey string
	input := &s3.PutObjectInput{
		Bucket:            aws.String(bucket),
		Key:               aws.String(key),
		Body:              bytes.NewReader(contents),
		ChecksumAlgorithm: types.ChecksumAlgorithmSha256,
	}
	output, err := actor.S3Manager.Upload(ctx, input)
	if err != nil {
		var noBucket *types.NoSuchBucket
		if errors.As(err, &noBucket) {
			log.Printf("Bucket %s does not exist.\n", bucket)
			err = noBucket
		}
	} else {
		err := s3.NewObjectExistsWaiter(actor.S3Client).Wait(ctx, &s3.HeadObjectInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(key),
		}, time.Minute)
		if err != nil {
			log.Printf("Failed attempt to wait for object %s to exist in %s.\n", key, bucket)
		} else {
			outKey = *output.Key
		}
	}
	return outKey, err
}

// DownloadLargeObject uses a download manager to download an object from a bucket.
// The download manager gets the data in parts and writes them to a buffer until all of
// the data has been downloaded.
//
// Code modified from: https://docs.aws.amazon.com/code-library/latest/ug/go_2_s3_code_examples.html#heading:r4v:
func (actor S3Actions) DownloadLargeObject(bucketName string, objectKey string) ([]byte, error) {
	downloader := manager.NewDownloader(actor.S3Client, func(d *manager.Downloader) {
		d.PartSize = maxUploadSize
	})
	buffer := manager.NewWriteAtBuffer([]byte{})
	_, err := downloader.Download(context.TODO(), buffer, &s3.GetObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to download large object %v: %v", objectKey, err)
	}
	return buffer.Bytes(), err
}
