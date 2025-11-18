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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	reloadEndpoint        = "/.decofile/reload"
	reloadTimeout         = 30 * time.Second // 30s per pod (simple POST, no long-polling)
	maxRetries            = 3                // 3 attempts per pod
	initialBackoff        = 2 * time.Second
	decofileLabel         = "deco.sites/decofile"
	maxNotificationTime   = 2 * time.Minute // 2 min for entire batch
	notificationBatchSize = 10              // Parallel notification batch size (reduced to save memory)
	appContainerName      = "app"
	reloadTokenEnvVar     = "DECO_RELEASE_RELOAD_TOKEN"
)

// Notifier handles notifying pods about ConfigMap changes
type Notifier struct {
	Client     client.Client
	HTTPClient *http.Client
}

// NewNotifier creates a new Notifier instance
func NewNotifier(k8sClient client.Client) *Notifier {
	return &Notifier{
		Client: k8sClient,
		HTTPClient: &http.Client{
			Timeout: reloadTimeout,
		},
	}
}

// extractReloadToken extracts the reload token from the "app" container's environment variables
func extractReloadToken(pod *corev1.Pod) string {
	for _, container := range pod.Spec.Containers {
		if container.Name == appContainerName {
			for _, env := range container.Env {
				if env.Name == reloadTokenEnvVar {
					return env.Value
				}
			}
		}
	}
	return ""
}

// NotifyPodsForDecofile notifies all pods using the given Decofile
// that the ConfigMap has changed and they should reload.
// Uses parallel batch processing with 2-minute timeout.
func (n *Notifier) NotifyPodsForDecofile(ctx context.Context, namespace, decofileName, timestamp, decofileContent string) error {
	log := logf.FromContext(ctx)

	log.Info("Notifying pods for Decofile", "decofile", decofileName, "namespace", namespace)

	// Create timeout context for entire operation
	notifyCtx, cancel := context.WithTimeout(ctx, maxNotificationTime)
	defer cancel()

	// List pods with the decofile label
	podList := &corev1.PodList{}
	err := n.Client.List(notifyCtx, podList,
		client.InNamespace(namespace),
		client.MatchingLabels{decofileLabel: decofileName})

	if err != nil {
		return fmt.Errorf("failed to list pods for decofile %s: %w", decofileName, err)
	}

	if len(podList.Items) == 0 {
		log.V(1).Info("No pods found for Decofile", "decofile", decofileName)
		return nil
	}

	// Collect pod names to notify
	podNames := make([]string, 0, len(podList.Items))
	for _, pod := range podList.Items {
		podNames = append(podNames, pod.Name)
	}

	log.Info("Starting parallel pod notifications", "totalPods", len(podNames), "batchSize", notificationBatchSize)

	// Prepare JSON payload once (reused across all pods to avoid memory duplication)
	payload := map[string]interface{}{
		"timestamp": timestamp,
		"source":    "operator",
		"decofile":  json.RawMessage(decofileContent),
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}
	log.V(1).Info("Marshaled notification payload", "size", len(payloadBytes))

	// Notify pods in parallel batches
	type notifyResult struct {
		podName string
		err     error
	}

	resultChan := make(chan notifyResult, len(podNames))
	semaphore := make(chan struct{}, notificationBatchSize) // Limit concurrent notifications

	// Launch goroutines for each pod
	for _, podName := range podNames {
		go func(name string) {
			// Acquire semaphore
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			// Get fresh pod data (avoids stale data)
			pod := &corev1.Pod{}
			err := n.Client.Get(notifyCtx, client.ObjectKey{Name: name, Namespace: namespace}, pod)
			if err != nil {
				resultChan <- notifyResult{name, fmt.Errorf("failed to get pod: %w", err)}
				return
			}

			// Skip if not running
			if pod.Status.Phase != corev1.PodRunning {
				log.V(1).Info("Skipping non-running pod", "pod", name, "phase", pod.Status.Phase)
				resultChan <- notifyResult{name, nil} // Not an error, just skip
				return
			}

			// Skip if no IP
			if pod.Status.PodIP == "" {
				log.V(1).Info("Skipping pod without IP", "pod", name)
				resultChan <- notifyResult{name, nil}
				return
			}

			// Notify pod
			err = n.notifyPodWithRetry(notifyCtx, pod, timestamp, payloadBytes)
			resultChan <- notifyResult{name, err}
		}(podName)
	}

	// Collect results
	var allErrors []string
	successCount := 0
	failCount := 0
	skippedCount := 0

	for i := 0; i < len(podNames); i++ {
		select {
		case result := <-resultChan:
			if result.err != nil {
				if strings.Contains(result.err.Error(), "failed to get pod") {
					skippedCount++
					log.V(1).Info("Pod no longer exists", "pod", result.podName)
				} else {
					failCount++
					allErrors = append(allErrors, fmt.Sprintf("%s: %v", result.podName, result.err))
					log.Error(result.err, "Failed to notify pod", "pod", result.podName)
				}
			} else {
				successCount++
				log.Info("Successfully notified pod", "pod", result.podName)
			}
		case <-notifyCtx.Done():
			return fmt.Errorf("notification timeout after %v: notified %d/%d pods", maxNotificationTime, successCount, len(podNames))
		}
	}

	log.Info("Notification summary", "success", successCount, "failed", failCount, "skipped", skippedCount, "total", len(podNames))

	if len(allErrors) > 0 {
		return fmt.Errorf("failed to notify %d pod(s): %s", failCount, strings.Join(allErrors, "; "))
	}

	return nil
}

// notifyPodWithRetry attempts to notify a single pod with exponential backoff retry
// POSTs JSON payload containing the decofile content
func (n *Notifier) notifyPodWithRetry(ctx context.Context, pod *corev1.Pod, timestamp string, payloadBytes []byte) error {
	log := logf.FromContext(ctx)

	// Get port from container
	port := int32(8000)
	if len(pod.Spec.Containers) > 0 && len(pod.Spec.Containers[0].Ports) > 0 {
		port = pod.Spec.Containers[0].Ports[0].ContainerPort
	}

	requestURL := fmt.Sprintf("http://%s:%d%s", pod.Status.PodIP, port, reloadEndpoint)

	// Extract reload token from pod
	token := extractReloadToken(pod)
	if token == "" {
		log.V(1).Info("No reload token found in pod, skipping authorization", "pod", pod.Name)
	}

	backoff := initialBackoff

	for attempt := 1; attempt <= maxRetries; attempt++ {
		log.V(1).Info("Attempting to notify pod", "pod", pod.Name, "attempt", attempt, "timestamp", timestamp)

		req, err := http.NewRequestWithContext(ctx, "POST", requestURL, bytes.NewReader(payloadBytes))
		if err != nil {
			return fmt.Errorf("failed to create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")

		// Add authorization header if token exists
		if token != "" {
			req.Header.Set("Authorization", fmt.Sprintf("Token %s", token))
		}

		resp, err := n.HTTPClient.Do(req)
		if err == nil {
			defer func() {
				if closeErr := resp.Body.Close(); closeErr != nil {
					log.Error(closeErr, "Failed to close response body", "pod", pod.Name)
				}
			}()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				log.V(1).Info("Pod notified successfully", "pod", pod.Name, "status", resp.StatusCode)
				return nil
			}
			log.V(1).Info("Pod returned non-success status", "pod", pod.Name, "status", resp.StatusCode)
			err = fmt.Errorf("pod returned status %d", resp.StatusCode)
		}

		// If this was the last attempt, return the error
		if attempt == maxRetries {
			return fmt.Errorf("max retries reached: %w", err)
		}

		// Wait before retrying with exponential backoff
		log.V(1).Info("Retrying after backoff", "pod", pod.Name, "backoff", backoff, "error", err)
		select {
		case <-time.After(backoff):
			backoff *= 2
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return fmt.Errorf("unexpected: loop ended without return")
}
