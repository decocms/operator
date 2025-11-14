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
	reloadEndpoint   = "/.decofile/reload"
	reloadTimeout    = 90 * time.Second // Longer timeout for long-polling
	maxRetries       = 3                // Fewer retries since we're doing long-poll
	initialBackoff   = 5 * time.Second  // Longer backoff for long-poll
	decofileLabel    = "deco.sites/decofile"
	defaultMountPath = "/app/decofile"
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
// that the ConfigMap has changed and they should reload
func (n *Notifier) NotifyPodsForDecofile(ctx context.Context, namespace, decofileName, timestamp string) error {
	log := logf.FromContext(ctx)

	log.Info("Notifying pods for Decofile", "decofile", decofileName, "namespace", namespace)

	// List pods with the decofile label
	podList := &corev1.PodList{}
	err := n.Client.List(ctx, podList,
		client.InNamespace(namespace),
		client.MatchingLabels{decofileLabel: decofileName})

	if err != nil {
		return fmt.Errorf("failed to list pods for decofile %s: %w", decofileName, err)
	}

	if len(podList.Items) == 0 {
		log.V(1).Info("No pods found for Decofile", "decofile", decofileName)
		return nil
	}

	var allErrors []string
	successCount := 0
	failCount := 0

	// Notify each pod
	for _, pod := range podList.Items {
		// Skip pods that are not running
		if pod.Status.Phase != corev1.PodRunning {
			log.V(1).Info("Skipping non-running pod", "pod", pod.Name, "phase", pod.Status.Phase)
			continue
		}

		// Skip pods without an IP
		if pod.Status.PodIP == "" {
			log.V(1).Info("Skipping pod without IP", "pod", pod.Name)
			continue
		}

		// Get port from pod's first container (usually the app container)
		port := int32(8000) // default
		if len(pod.Spec.Containers) > 0 && len(pod.Spec.Containers[0].Ports) > 0 {
			port = pod.Spec.Containers[0].Ports[0].ContainerPort
		}

		err := n.notifyPodWithRetry(ctx, pod.Name, pod.Status.PodIP, port, timestamp)
		if err != nil {
			errMsg := fmt.Sprintf("failed to notify pod %s (IP: %s:%d): %v", pod.Name, pod.Status.PodIP, port, err)
			log.Error(err, "Failed to notify pod after retries", "pod", pod.Name, "ip", pod.Status.PodIP, "port", port)
			allErrors = append(allErrors, errMsg)
			failCount++
		} else {
			log.Info("Successfully notified pod", "pod", pod.Name, "ip", pod.Status.PodIP, "port", port, "timestamp", timestamp)
			successCount++
		}
	}

	log.Info("Notification summary", "success", successCount, "failed", failCount, "decofile", decofileName)

	if len(allErrors) > 0 {
		return fmt.Errorf("failed to notify %d pod(s): %s", failCount, strings.Join(allErrors, "; "))
	}

	return nil
}

// notifyPodWithRetry attempts to notify a single pod with exponential backoff retry
// Uses long-polling with timestamp to ensure pod has received the update
func (n *Notifier) notifyPodWithRetry(ctx context.Context, podName, podIP string, port int32, timestamp string) error {
	log := logf.FromContext(ctx)
	// Long-poll endpoint: pod will wait until timestamp file >= expected timestamp
	tsFilePath := fmt.Sprintf("%s/timestamp.txt", defaultMountPath)
	requestURL := fmt.Sprintf("http://%s:%d%s?timestamp=%s&tsFile=%s",
		podIP, port, reloadEndpoint, url.QueryEscape(timestamp), url.QueryEscape(tsFilePath))

	backoff := initialBackoff

	for attempt := 1; attempt <= maxRetries; attempt++ {
		log.V(1).Info("Attempting to notify pod", "pod", podName, "attempt", attempt, "url", requestURL)

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
