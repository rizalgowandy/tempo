package s3

import (
	"github.com/go-kit/kit/log"
	"github.com/prometheus/common/model"
	"github.com/thanos-io/thanos/pkg/objstore"
	"github.com/thanos-io/thanos/pkg/objstore/s3"
)

// NewBucketClient creates a new S3 bucket client
func NewBucketClient(cfg Config, name string, logger log.Logger) (objstore.Bucket, error) {
	return s3.NewBucketWithConfig(logger, newS3Config(cfg), name)
}

// NewBucketReaderClient creates a new S3 bucket client
func NewBucketReaderClient(cfg Config, name string, logger log.Logger) (objstore.BucketReader, error) {
	return s3.NewBucketWithConfig(logger, newS3Config(cfg), name)
}

func newS3Config(cfg Config) s3.Config {
	return s3.Config{
		Bucket:    cfg.BucketName,
		Endpoint:  cfg.Endpoint,
		AccessKey: cfg.AccessKeyID,
		SecretKey: cfg.SecretAccessKey.Value,
		Insecure:  cfg.Insecure,
		HTTPConfig: s3.HTTPConfig{
			IdleConnTimeout:       model.Duration(cfg.HTTP.IdleConnTimeout),
			ResponseHeaderTimeout: model.Duration(cfg.HTTP.ResponseHeaderTimeout),
			InsecureSkipVerify:    cfg.HTTP.InsecureSkipVerify,
			TLSHandshakeTimeout:   model.Duration(cfg.HTTP.TLSHandshakeTimeout),
			ExpectContinueTimeout: model.Duration(cfg.HTTP.ExpectContinueTimeout),
			MaxIdleConns:          cfg.HTTP.MaxIdleConns,
			MaxIdleConnsPerHost:   cfg.HTTP.MaxIdleConnsPerHost,
			MaxConnsPerHost:       cfg.HTTP.MaxConnsPerHost,
			Transport:             cfg.HTTP.Transport,
		},
		// Enforce signature version 2 if CLI flag is set
		SignatureV2: cfg.SignatureVersion == SignatureVersionV2,
	}
}
