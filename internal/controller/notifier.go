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
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	reloadEndpoint    = "/deco/.decofile/reload"
	reloadTimeout     = 90 * time.Second // Longer timeout to account for delay parameter
	maxRetries        = 5
	initialBackoff    = 1 * time.Second
	decofileLabel     = "deco.sites/decofile"
	configSyncDelayMS = 60000 // 60 seconds for kubelet to sync ConfigMap to pods
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
func (n *Notifier) NotifyPodsForDecofile(ctx context.Context, namespace, decofileName string) error {
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

		err := n.notifyPodWithRetry(ctx, pod.Name, pod.Status.PodIP)
		if err != nil {
			errMsg := fmt.Sprintf("failed to notify pod %s (IP: %s): %v", pod.Name, pod.Status.PodIP, err)
			log.Error(err, "Failed to notify pod after retries", "pod", pod.Name, "ip", pod.Status.PodIP)
			allErrors = append(allErrors, errMsg)
			failCount++
		} else {
			log.Info("Successfully notified pod", "pod", pod.Name, "ip", pod.Status.PodIP)
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
func (n *Notifier) notifyPodWithRetry(ctx context.Context, podName, podIP string) error {
	log := logf.FromContext(ctx)
	// Add delay parameter to give kubelet time to sync ConfigMap to pod
	url := fmt.Sprintf("http://%s:8080%s?delay=%d", podIP, reloadEndpoint, configSyncDelayMS)

	backoff := initialBackoff

	for attempt := 1; attempt <= maxRetries; attempt++ {
		log.V(1).Info("Attempting to notify pod", "pod", podName, "attempt", attempt, "url", url)

		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
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
