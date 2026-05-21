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
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
)

type mockSMClient struct {
	out *secretsmanager.GetSecretValueOutput
	err error
}

func (m *mockSMClient) GetSecretValue(_ context.Context, _ *secretsmanager.GetSecretValueInput, _ ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error) {
	return m.out, m.err
}

func TestAWSSourceGet_Found(t *testing.T) {
	src := &AWSSource{api: &mockSMClient{
		out: &secretsmanager.GetSecretValueOutput{
			SecretString: aws.String(`{"DENO_AUTH_TOKENS":"github_pat_xxx@raw.githubusercontent.com","FOO":"bar"}`),
		},
	}}

	data, exists, err := src.Get(context.Background(), "sites/acme/build")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !exists {
		t.Fatal("exists = false, want true")
	}
	if data["DENO_AUTH_TOKENS"] != "github_pat_xxx@raw.githubusercontent.com" {
		t.Fatalf("DENO_AUTH_TOKENS = %q", data["DENO_AUTH_TOKENS"])
	}
	if data["FOO"] != "bar" {
		t.Fatalf("FOO = %q", data["FOO"])
	}
}

func TestAWSSourceGet_NotFoundIsNotAnError(t *testing.T) {
	src := &AWSSource{api: &mockSMClient{
		err: &smtypes.ResourceNotFoundException{},
	}}

	data, exists, err := src.Get(context.Background(), "sites/acme/build")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if exists {
		t.Fatal("exists = true on missing key, want false")
	}
	if data != nil {
		t.Fatalf("data = %v, want nil", data)
	}
}

func TestAWSSourceGet_BinaryRejected(t *testing.T) {
	src := &AWSSource{api: &mockSMClient{
		out: &secretsmanager.GetSecretValueOutput{
			SecretBinary: []byte{0x01, 0x02},
		},
	}}

	_, _, err := src.Get(context.Background(), "sites/acme/build")
	if err == nil {
		t.Fatal("expected error for binary secret, got nil")
	}
}

func TestAWSSourceGet_InvalidJSON(t *testing.T) {
	src := &AWSSource{api: &mockSMClient{
		out: &secretsmanager.GetSecretValueOutput{
			SecretString: aws.String(`not json`),
		},
	}}

	_, _, err := src.Get(context.Background(), "sites/acme/build")
	if err == nil {
		t.Fatal("expected JSON parse error, got nil")
	}
}

func TestAWSSourceGet_NullJSONRejected(t *testing.T) {
	src := &AWSSource{api: &mockSMClient{
		out: &secretsmanager.GetSecretValueOutput{
			SecretString: aws.String(`null`),
		},
	}}

	_, _, err := src.Get(context.Background(), "sites/acme/build")
	if err == nil {
		t.Fatal("expected error for null payload, got nil")
	}
}

func TestAWSSourceGet_EmptyObjectAccepted(t *testing.T) {
	src := &AWSSource{api: &mockSMClient{
		out: &secretsmanager.GetSecretValueOutput{
			SecretString: aws.String(`{}`),
		},
	}}

	data, exists, err := src.Get(context.Background(), "sites/acme/build")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !exists {
		t.Fatal("empty object should be treated as existing")
	}
	if data == nil {
		t.Fatal("empty object should yield non-nil empty map")
	}
	if len(data) != 0 {
		t.Fatalf("data should be empty, got %v", data)
	}
}

func TestAWSSourceGet_OtherErrorPropagates(t *testing.T) {
	sentinel := errors.New("boom")
	src := &AWSSource{api: &mockSMClient{err: sentinel}}

	_, _, err := src.Get(context.Background(), "sites/acme/build")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("error did not wrap sentinel: %v", err)
	}
}
