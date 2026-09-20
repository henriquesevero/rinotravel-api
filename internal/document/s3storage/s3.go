// Package s3storage implements document.Storage on Amazon S3 or any S3-compatible service
// (MinIO in development). The bucket stays private: access is only through signed URLs.
package s3storage

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"

	"rinotravel-api/internal/document"
)

type Config struct {
	Bucket string
	Region string
	// Endpoint is only for S3-compatible services; leave it empty for AWS.
	Endpoint string
	// AccessKeyID and SecretAccessKey are optional: without them the default AWS credential chain applies.
	AccessKeyID     string
	SecretAccessKey string
}

type Storage struct {
	bucket  string
	client  *s3.Client
	presign *s3.PresignClient
}

func New(ctx context.Context, cfg Config) (*Storage, error) {
	opts := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(cfg.Region)}
	if cfg.AccessKeyID != "" {
		opts = append(opts, awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, "")))
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}
	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		// Checksums are declared explicitly per upload; the SDK's automatic CRC32 would add
		// parameters that S3-compatible services and browsers do not send.
		o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
		o.ResponseChecksumValidation = aws.ResponseChecksumValidationWhenRequired
		if cfg.Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
			o.UsePathStyle = true
		}
	})
	return &Storage{bucket: cfg.Bucket, client: client, presign: s3.NewPresignClient(client)}, nil
}

func (s *Storage) Client() *s3.Client { return s.client }

func (s *Storage) PresignUpload(ctx context.Context, key string, req document.UploadRequest) (document.Upload, error) {
	raw, err := hex.DecodeString(req.ChecksumSHA256)
	if err != nil {
		return document.Upload{}, fmt.Errorf("checksum is not hex: %w", err)
	}
	signed, err := s.presign.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket:         aws.String(s.bucket),
		Key:            aws.String(key),
		ContentType:    aws.String(req.ContentType),
		ContentLength:  aws.Int64(req.Size),
		ChecksumSHA256: aws.String(base64.StdEncoding.EncodeToString(raw)),
	}, s3.WithPresignExpires(req.TTL))
	if err != nil {
		return document.Upload{}, err
	}
	return document.Upload{URL: signed.URL, Method: signed.Method, Headers: headers(signed.SignedHeader), ExpiresAt: time.Now().Add(req.TTL).UTC()}, nil
}

func (s *Storage) Stat(ctx context.Context, key string) (document.ObjectInfo, error) {
	out, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket:       aws.String(s.bucket),
		Key:          aws.String(key),
		ChecksumMode: types.ChecksumModeEnabled,
	})
	if err != nil {
		var notFound *types.NotFound
		var apiErr smithy.APIError
		if errors.As(err, &notFound) || (errors.As(err, &apiErr) && (apiErr.ErrorCode() == "NotFound" || apiErr.ErrorCode() == "NoSuchKey")) {
			return document.ObjectInfo{}, document.ErrObjectNotFound
		}
		return document.ObjectInfo{}, err
	}
	info := document.ObjectInfo{Size: aws.ToInt64(out.ContentLength), ContentType: aws.ToString(out.ContentType)}
	if sum := aws.ToString(out.ChecksumSHA256); sum != "" {
		if raw, err := base64.StdEncoding.DecodeString(sum); err == nil {
			info.ChecksumSHA256 = hex.EncodeToString(raw)
		}
	}
	return info, nil
}

func (s *Storage) PresignDownload(ctx context.Context, key string, req document.DownloadRequest) (document.Upload, error) {
	signed, err := s.presign.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket:                     aws.String(s.bucket),
		Key:                        aws.String(key),
		ResponseContentDisposition: aws.String(document.ContentDisposition(req.FileName)),
		ResponseContentType:        aws.String(req.ContentType),
	}, s3.WithPresignExpires(req.TTL))
	if err != nil {
		return document.Upload{}, err
	}
	return document.Upload{URL: signed.URL, Method: signed.Method, ExpiresAt: time.Now().Add(req.TTL).UTC()}, nil
}

func (s *Storage) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	return err
}

// headers returns what the client must send; Host and Content-Length are set by its HTTP stack.
func headers(signed http.Header) map[string]string {
	out := map[string]string{}
	for name, values := range signed {
		switch strings.ToLower(name) {
		case "host", "content-length":
			continue
		}
		out[name] = strings.Join(values, ",")
	}
	return out
}
