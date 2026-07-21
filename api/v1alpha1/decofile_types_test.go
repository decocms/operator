/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package v1alpha1

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// The S3 object key must be derived identically by the reconciler (upload) and
// the Service webhook (DECO_RELEASE URL), so this pins the shared shape.
func TestDecofileS3ObjectKey(t *testing.T) {
	df := &Decofile{
		ObjectMeta: metav1.ObjectMeta{Name: "dep-123", Namespace: "sites-econverse"},
	}
	cases := []struct {
		name         string
		deploymentId string
		prefix       string
		want         string
	}{
		{"name as deploymentId, no prefix", "", "", "sites-econverse/dep-123/decofile.json"},
		{"explicit deploymentId", "abc", "", "sites-econverse/abc/decofile.json"},
		{"prefix trimmed", "", "decofiles/", "decofiles/sites-econverse/dep-123/decofile.json"},
		{"prefix without slash", "", "decofiles", "decofiles/sites-econverse/dep-123/decofile.json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			df.Spec.DeploymentId = tc.deploymentId
			if got := df.S3ObjectKey(tc.prefix); got != tc.want {
				t.Fatalf("S3ObjectKey(%q) = %q, want %q", tc.prefix, got, tc.want)
			}
		})
	}
}
