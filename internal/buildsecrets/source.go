/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Package buildsecrets owns the operator's per-tenant build-time secret
// sync. Reconciler keeps a K8s Secret named `build-secrets` in opted-in
// site namespaces aligned with an upstream secret backend (AWS Secrets
// Manager today; other backends slot in by implementing Source).
package buildsecrets

import (
	"context"
	"errors"
)

const (
	// SecretName is the K8s Secret consumed by the builder Job via
	// envFrom (admin PR #3201 — `optional: true` so absence is a no-op).
	SecretName = "build-secrets"

	// ManagedByLabel + FeatureLabel mark Secrets the operator created so
	// it knows what is safe to update or delete. A Secret without these
	// labels is treated as user-managed and left alone.
	ManagedByLabel = "deco.sites/managed-by"
	FeatureLabel   = "deco.sites/feature"

	// KeyTemplate is the path convention in the upstream backend. AWS
	// Secrets Manager stores `sites/<site>/build` as a JSON object whose
	// keys/values land verbatim in the K8s Secret data.
	KeyTemplate = "sites/%s/build"
)

// ErrNotOwned signals the K8s Secret `build-secrets` exists in the
// namespace without the operator's labels. Sync and Remove refuse to
// touch it so we don't trample on a Secret a human created by hand.
var ErrNotOwned = errors.New("build-secrets Secret exists without operator labels; refusing to manage it")

// Source abstracts the upstream secret backend behind a tiny shape that
// hides AWS-specifics from the reconciler. Implementations:
//
//   - AWSSource (this package): AWS Secrets Manager via aws-sdk-go-v2
//   - future GCPSource / VaultSource: same interface, different SDK
//
// Get returns (data, true, nil) when the upstream key exists; (nil,
// false, nil) when it does not — *not* an error, just "no upstream
// data yet" (normal state for un-provisioned tenants). Network or
// permission failures bubble up via the error return.
type Source interface {
	Get(ctx context.Context, key string) (data map[string]string, exists bool, err error)
}
