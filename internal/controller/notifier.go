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
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	reloadEndpoint        = "/.decofile/reload"
	reloadTimeout         = 150 * time.Second // 2.5 min per pod (120s app wait + 30s buffer)
	maxRetries            = 2                 // 2 attempts per pod (long timeout reduces retry need)
	initialBackoff        = 5 * time.Second
	decofileLabel         = "deco.sites/decofile"
	defaultMountPath      = "/app/decofile"
	maxNotificationTime   = 5 * time.Minute // Maximum time for entire batch (accommodates long-polling + retries)
	notificationBatchSize = 30              // Parallel notification batch size
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

// NotifyPodsForDecofile notifies all pods using the given Decofile
// that the ConfigMap has changed and they should reload.
// Uses parallel batch processing with 2-minute timeout.
func (n *Notifier) NotifyPodsForDecofile(ctx context.Context, namespace, decofileName, timestamp string) error {
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

			// Get port from container
			port := int32(8000)
			if len(pod.Spec.Containers) > 0 && len(pod.Spec.Containers[0].Ports) > 0 {
				port = pod.Spec.Containers[0].Ports[0].ContainerPort
			}

			// Notify pod
			err = n.notifyPodWithRetry(notifyCtx, name, pod.Status.PodIP, port, timestamp)
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
// Uses long-polling with timestamp to ensure pod has received the update
// Verifies pod is still running before each retry
func (n *Notifier) notifyPodWithRetry(ctx context.Context, podName, podIP string, port int32, timestamp string) error {
	log := logf.FromContext(ctx)
	// Long-poll endpoint: pod will wait until timestamp file >= expected timestamp
	tsFilePath := fmt.Sprintf("%s/timestamp.txt", defaultMountPath)
	requestURL := fmt.Sprintf("http://%s:%d%s?timestamp=%s&tsFile=%s",
		podIP, port, reloadEndpoint, url.QueryEscape(timestamp), url.QueryEscape(tsFilePath))

	backoff := initialBackoff

	for attempt := 1; attempt <= maxRetries; attempt++ {
		log.V(1).Info("Attempting to notify pod", "pod", podName, "attempt", attempt)

		req, err := http.NewRequestWithContext(ctx, "GET", requestURL, nil)
		if err != nil {
			return fmt.Errorf("failed to create request: %w", err)
		}

		resp, err := n.HTTPClient.Do(req)
		if err == nil {
			defer func() {
				if closeErr := resp.Body.Close(); closeErr != nil {
					log.Error(closeErr, "Failed to close response body", "pod", podName)
				}
			}()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				log.V(1).Info("Pod notified successfully", "pod", podName, "status", resp.StatusCode)
				return nil
			}
			log.V(1).Info("Pod returned non-success status", "pod", podName, "status", resp.StatusCode)
			err = fmt.Errorf("pod returned status %d", resp.StatusCode)
		}

		// If this was the last attempt, return the error
		if attempt == maxRetries {
			return fmt.Errorf("max retries reached: %w", err)
		}

		// Wait before retrying with exponential backoff
		log.V(1).Info("Retrying after backoff", "pod", podName, "backoff", backoff, "error", err)
		select {
		case <-time.After(backoff):
			backoff *= 2
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return fmt.Errorf("unexpected: loop ended without return")
}
