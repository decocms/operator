package build

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

const presignExpiry = time.Hour

// S3Config holds the AWS credentials used to generate presigned URLs.
type S3Config struct {
	Region          string
	AccessKeyID     string
	SecretAccessKey string
}

// GeneratePresignedURLs generates the three presigned URLs the build job needs.
// Mirrors generatePresignedUrls() in the admin's build.ts.
func GeneratePresignedURLs(ctx context.Context, cfg S3Config, site, jobName string) (PresignedURLs, error) {
	awsCfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(cfg.Region),
		config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		),
	)
	if err != nil {
		return PresignedURLs{}, fmt.Errorf("loading aws config: %w", err)
	}

	presigner := s3.NewPresignClient(s3.NewFromConfig(awsCfg))

	logsUpload, err := presigner.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(LogsBucket),
		Key:    aws.String(fmt.Sprintf("%s/%s.log", site, jobName)),
	}, s3.WithPresignExpires(presignExpiry))
	if err != nil {
		return PresignedURLs{}, fmt.Errorf("presigning logs upload: %w", err)
	}

	cacheKey := fmt.Sprintf("%s/npm-cache.tar.zst", site)

	cacheDownload, err := presigner.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(CacheBucket),
		Key:    aws.String(cacheKey),
	}, s3.WithPresignExpires(presignExpiry))
	if err != nil {
		return PresignedURLs{}, fmt.Errorf("presigning cache download: %w", err)
	}

	cacheUpload, err := presigner.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(CacheBucket),
		Key:    aws.String(cacheKey),
	}, s3.WithPresignExpires(presignExpiry))
	if err != nil {
		return PresignedURLs{}, fmt.Errorf("presigning cache upload: %w", err)
	}

	return PresignedURLs{
		LogsUpload:    logsUpload.URL,
		CacheDownload: cacheDownload.URL,
		CacheUpload:   cacheUpload.URL,
	}, nil
}
