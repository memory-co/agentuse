package file

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

type S3Config struct{ Endpoint, Region, Bucket, Prefix, AccessKey, SecretKey string }
type S3 struct {
	client         *s3.Client
	bucket, prefix string
}

func NewS3(c S3Config) (*Service, error) {
	if c.Bucket == "" || c.AccessKey == "" || c.SecretKey == "" {
		return nil, fmt.Errorf("S3 bucket and credentials are required")
	}
	if c.Region == "" {
		c.Region = "us-east-1"
	}
	client := s3.NewFromConfig(aws.Config{Region: c.Region, Credentials: credentials.NewStaticCredentialsProvider(c.AccessKey, c.SecretKey, ""), RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired, ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired}, func(o *s3.Options) {
		o.UsePathStyle = true
		if c.Endpoint != "" {
			o.BaseEndpoint = aws.String(c.Endpoint)
		}
	})
	return New(&S3{client, c.Bucket, strings.Trim(c.Prefix, "/")}), nil
}
func (s *S3) key(key string) string {
	if s.prefix == "" {
		return key
	}
	return s.prefix + "/" + key
}
func s3Error(err error) error {
	var api smithy.APIError
	if errors.As(err, &api) {
		switch api.ErrorCode() {
		case "NoSuchKey", "NotFound":
			return os.ErrNotExist
		case "PreconditionFailed", "ConditionalRequestConflict":
			return ErrExists
		}
	}
	return err
}
func (s *S3) Put(ctx context.Context, key string, source io.ReadSeeker, size int64, exclusive bool) error {
	in := &s3.PutObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(s.key(key)), Body: source, ContentLength: aws.Int64(size)}
	if exclusive {
		in.IfNoneMatch = aws.String("*")
	}
	_, err := s.client.PutObject(ctx, in)
	return s3Error(err)
}
func (s *S3) Get(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(s.key(key))})
	if err != nil {
		return nil, 0, s3Error(err)
	}
	return out.Body, aws.ToInt64(out.ContentLength), nil
}
func (s *S3) List(ctx context.Context, prefix string) ([]Entry, error) {
	pages := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{Bucket: aws.String(s.bucket), Prefix: aws.String(s.key(prefix))})
	out := []Entry{}
	for pages.HasMorePages() {
		page, err := pages.NextPage(ctx)
		if err != nil {
			return nil, s3Error(err)
		}
		for _, o := range page.Contents {
			key := aws.ToString(o.Key)
			if s.prefix != "" {
				key = strings.TrimPrefix(key, s.prefix+"/")
			}
			out = append(out, Entry{key, aws.ToInt64(o.Size)})
		}
	}
	return out, nil
}
func (s *S3) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(s.key(key))})
	return s3Error(err)
}
