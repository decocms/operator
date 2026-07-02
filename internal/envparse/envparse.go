// Package envparse holds small helpers for parsing JSON-encoded pod-scheduling
// config from environment variables, shared by the build and deploy Job
// builders so their behavior can't drift.
package envparse

import (
	"encoding/json"

	corev1 "k8s.io/api/core/v1"
)

// NodeSelector parses a JSON object string into a nodeSelector map. Returns nil
// on empty input or parse error.
func NodeSelector(s string) map[string]string {
	if s == "" {
		return nil
	}
	m := map[string]string{}
	if err := json.Unmarshal([]byte(s), &m); err != nil || len(m) == 0 {
		return nil
	}
	return m
}

// Tolerations parses a JSON array of Toleration objects. Returns nil on empty
// input or parse error.
func Tolerations(s string) []corev1.Toleration {
	if s == "" {
		return nil
	}
	var t []corev1.Toleration
	if err := json.Unmarshal([]byte(s), &t); err != nil {
		return nil
	}
	return t
}
