/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	decositesv1alpha1 "github.com/deco-sites/decofile-operator/api/v1alpha1"
)

// S3Uploader delivers the merged decofile as a plain-JSON object to S3, from
// which the deco runtime reads it over HTTP (DECO_RELEASE=https://…). It is the
// escape hatch for content-heavy sites whose decofile exceeds the ~1MB etcd
// ConfigMap limit. Nil when DECOFILE_S3_BUCKET is unset (s3 target disabled).
type S3Uploader struct {
	client     *s3.Client
	bucket     string
	region     string
	prefix     string
	publicHost string // host used to build the runtime-facing HTTP URL
}

// NewS3UploaderFromEnv builds an uploader from DECOFILE_S3_* env. Returns
// (nil, nil) when DECOFILE_S3_BUCKET is unset so the operator runs unchanged for
// clusters not using the s3 target. AWS credentials resolve via the default
// chain (IRSA web-identity in EKS).
func NewS3UploaderFromEnv(ctx context.Context) (*S3Uploader, error) {
	bucket := os.Getenv("DECOFILE_S3_BUCKET")
	if bucket == "" {
		return nil, nil
	}
	region := os.Getenv("DECOFILE_S3_REGION")
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("load aws config for decofile s3 target: %w", err)
	}
	return &S3Uploader{
		client:     s3.NewFromConfig(cfg),
		bucket:     bucket,
		region:     region,
		prefix:     os.Getenv("DECOFILE_S3_PREFIX"),
		publicHost: os.Getenv("DECOFILE_S3_PUBLIC_HOST"),
	}, nil
}

// Upload PUTs the raw decofile JSON. Served as plain JSON so the runtime's HTTP
// provider (fetch + JSON.parse) reads it directly — no compression, so no
// base64/brotli decode is needed on the runtime side.
// ponytail: uncompressed; add gzip + Content-Encoding if in-region egress/latency
// ever measures as a problem (Deno fetch auto-gunzips).
func (u *S3Uploader) Upload(ctx context.Context, key, jsonContent string) error {
	_, err := u.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:       aws.String(u.bucket),
		Key:          aws.String(key),
		Body:         strings.NewReader(jsonContent),
		ContentType:  aws.String("application/json"),
		CacheControl: aws.String("no-cache"),
	})
	if err != nil {
		return fmt.Errorf("put s3://%s/%s: %w", u.bucket, key, err)
	}
	return nil
}

// URLFor builds the runtime-facing HTTP URL for an object key. Prefers the
// configured public host (a CloudFront/VPC-restricted domain); falls back to the
// regional virtual-hosted S3 endpoint.
func (u *S3Uploader) URLFor(key string) string {
	host := u.publicHost
	if host == "" {
		host = fmt.Sprintf("%s.s3.%s.amazonaws.com", u.bucket, u.region)
	}
	return fmt.Sprintf("https://%s/%s", host, key)
}

func sha256hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// reconcileS3 delivers the decofile via S3+HTTP instead of a ConfigMap. It
// mirrors the ConfigMap path (retrieve → deliver → notify pods) but writes to
// S3 and gates re-work on a content hash so it never blows the etcd limit.
func (r *DecofileReconciler) reconcileS3(ctx context.Context, req ctrl.Request, decofile *decositesv1alpha1.Decofile) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	if r.S3 == nil {
		return ctrl.Result{}, fmt.Errorf("decofile target=s3 but operator has no S3 config (DECOFILE_S3_BUCKET unset)")
	}

	deploymentId := decofile.DeploymentIdOrName()

	// GitHub gate: if the commit is unchanged and we've delivered before, there's
	// nothing to do — skip the (expensive) repo download entirely.
	if decofile.Spec.Source == SourceTypeGitHub && decofile.Spec.GitHub != nil &&
		decofile.Status.GitHubCommit == decofile.Spec.GitHub.Commit &&
		decofile.Status.ContentHash != "" {
		log.V(1).Info("s3: github commit unchanged and already delivered, skipping")
		return ctrl.Result{}, nil
	}

	source, err := NewSource(r.Client, decofile)
	if err != nil {
		log.Error(err, "s3: failed to create source")
		return ctrl.Result{}, err
	}
	jsonContent, err := source.Retrieve(ctx)
	if err != nil {
		log.Error(err, "s3: failed to retrieve source")
		return ctrl.Result{}, err
	}

	hash := sha256hex(jsonContent)
	changed := hash != decofile.Status.ContentHash
	key := decofile.S3ObjectKey(r.S3.prefix)
	url := r.S3.URLFor(key)

	if changed {
		if err := r.S3.Upload(ctx, key, jsonContent); err != nil {
			log.Error(err, "s3: upload failed", "key", key)
			return ctrl.Result{}, err
		}
		log.Info("s3: uploaded decofile", "url", url, "bytes", len(jsonContent))
	} else {
		log.V(1).Info("s3: content unchanged, skipping upload", "url", url)
	}

	// Notify running pods on change (same push path as the ConfigMap target;
	// the mounted/URL source is only the cold-start read).
	podsNotified := true
	var notifyErr string
	if changed {
		ts := fmt.Sprintf("%d", time.Now().Unix())
		notifier := NewNotifier(r.Client, r.HTTPClient)
		if err := notifier.NotifyPodsForDecofile(ctx, decofile.Namespace, deploymentId, ts, jsonContent); err != nil {
			log.Error(err, "s3: failed to notify pods", "deploymentId", deploymentId)
			podsNotified = false
			notifyErr = err.Error()
		} else {
			log.Info("s3: notified pods", "deploymentId", deploymentId)
		}
	}

	// Update status on the freshest object to avoid conflicts.
	fresh := &decositesv1alpha1.Decofile{}
	if err := r.Get(ctx, req.NamespacedName, fresh); err != nil {
		log.Error(err, "s3: failed to re-fetch Decofile for status update")
		return ctrl.Result{}, err
	}
	fresh.Status.LastUpdated = metav1.Time{Time: time.Now()}
	fresh.Status.SourceType = source.SourceType()
	fresh.Status.ContentHash = hash
	fresh.Status.S3URL = url
	if fresh.Spec.Source == SourceTypeGitHub && fresh.Spec.GitHub != nil {
		fresh.Status.GitHubCommit = fresh.Spec.GitHub.Commit
	}
	updateCondition(fresh, metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionTrue,
		Reason:             "S3Uploaded",
		Message:            fmt.Sprintf("Decofile delivered to %s", url),
		LastTransitionTime: metav1.Now(),
	})
	if changed {
		cond := metav1.Condition{
			Type:               condTypePodsNotified,
			LastTransitionTime: metav1.Now(),
		}
		if podsNotified {
			cond.Status = metav1.ConditionTrue
			cond.Reason = "NotificationSucceeded"
			cond.Message = fmt.Sprintf("Notified pods for hash:%s", hash[:12])
		} else {
			cond.Status = metav1.ConditionFalse
			cond.Reason = "NotificationFailed"
			cond.Message = fmt.Sprintf("Failed to notify pods for hash:%s: %s", hash[:12], notifyErr)
		}
		updateCondition(fresh, cond)
	}
	if err := r.Status().Update(ctx, fresh); err != nil {
		log.Error(err, "s3: failed to update status")
		return ctrl.Result{}, err
	}

	if changed && !podsNotified {
		return ctrl.Result{}, fmt.Errorf("s3: failed to notify pods: %s", notifyErr)
	}
	return ctrl.Result{}, nil
}
