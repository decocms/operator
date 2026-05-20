/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package buildsecrets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
)

// awsSecretsManagerAPI captures the AWS Secrets Manager surface this
// package uses. Defined so tests can swap a mock without depending on
// the real SDK client.
type awsSecretsManagerAPI interface {
	GetSecretValue(ctx context.Context, params *secretsmanager.GetSecretValueInput, optFns ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error)
}

// AWSSource resolves keys against AWS Secrets Manager. Credentials come
// from the ambient environment (Pod Identity / IRSA in-cluster; default
// chain locally). Region is taken from the AWS_REGION env var or the
// default chain — usually set by the EKS Pod Identity webhook.
type AWSSource struct {
	api awsSecretsManagerAPI
}

// NewAWSSource builds a Source backed by the live Secrets Manager
// client. The reconciler keeps the same instance for its lifetime.
func NewAWSSource(ctx context.Context) (*AWSSource, error) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}
	return &AWSSource{api: secretsmanager.NewFromConfig(cfg)}, nil
}

// Get fetches the JSON-encoded secret at `key` and decodes it into a
// flat map. A missing key returns (nil, false, nil) — the natural
// "not provisioned yet" state for tenants that have opted in but not
// supplied data.
func (s *AWSSource) Get(ctx context.Context, key string) (map[string]string, bool, error) {
	out, err := s.api.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
		SecretId: aws.String(key),
	})
	if err != nil {
		var nfe *smtypes.ResourceNotFoundException
		if errors.As(err, &nfe) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("get secret %q: %w", key, err)
	}

	if out.SecretString == nil {
		// Binary secrets are not supported — builds inject env vars,
		// not files, so a JSON object of strings is the only shape.
		return nil, false, fmt.Errorf("secret %q has no SecretString (binary secret?)", key)
	}

	var data map[string]string
	if err := json.Unmarshal([]byte(*out.SecretString), &data); err != nil {
		return nil, false, fmt.Errorf("parse %q as JSON object of strings: %w", key, err)
	}
	return data, true, nil
}
