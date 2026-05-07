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

// S3Config holds the AWS credentials and bucket names used to generate presigned URLs.
type S3Config struct {
	Region          string
	AccessKeyID     string
	SecretAccessKey string
	LogsBucket      string // bucket for build logs
	ArtifactsBucket string // bucket for build artifacts
	StateBucket     string // bucket for per-site state (defaults to ArtifactsBucket)
}

// GeneratepresignedURLs generates all presigned URLs the build job needs.
// Mirrors generatePresignedUrls() in the admin's build.ts.
func generatePresignedURLs(ctx context.Context, cfg S3Config, site, jobName string) (presignedURLs, error) {
	awsCfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(cfg.Region),
		config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		),
	)
	if err != nil {
		return presignedURLs{}, fmt.Errorf("loading aws config: %w", err)
	}

	presigner := s3.NewPresignClient(s3.NewFromConfig(awsCfg))

	logsUpload, err := presigner.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(cfg.LogsBucket),
		Key:    aws.String(fmt.Sprintf("%s/%s.log", site, jobName)),
	}, s3.WithPresignExpires(presignExpiry))
	if err != nil {
		return presignedURLs{}, fmt.Errorf("presigning logs upload: %w", err)
	}

	cacheKey := fmt.Sprintf("%s/npm-cache.tar.zst", site)

	cacheDownload, err := presigner.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(cfg.ArtifactsBucket),
		Key:    aws.String(cacheKey),
	}, s3.WithPresignExpires(presignExpiry))
	if err != nil {
		return presignedURLs{}, fmt.Errorf("presigning cache download: %w", err)
	}

	cacheUpload, err := presigner.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(cfg.ArtifactsBucket),
		Key:    aws.String(cacheKey),
	}, s3.WithPresignExpires(presignExpiry))
	if err != nil {
		return presignedURLs{}, fmt.Errorf("presigning cache upload: %w", err)
	}

	stateBucket := cfg.StateBucket
	if stateBucket == "" {
		stateBucket = cfg.ArtifactsBucket
	}
	stateDownload, err := presigner.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(stateBucket),
		Key:    aws.String(fmt.Sprintf("cfworkers/%s/state.json", site)),
	}, s3.WithPresignExpires(presignExpiry))
	if err != nil {
		return presignedURLs{}, fmt.Errorf("presigning state download: %w", err)
	}

	return presignedURLs{
		LogsUpload:    logsUpload.URL,
		CacheDownload: cacheDownload.URL,
		CacheUpload:   cacheUpload.URL,
		StateDownload: stateDownload.URL,
	}, nil
}
