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
	"encoding/json"
	"fmt"

	decositesv1alpha1 "github.com/deco-sites/decofile-operator/api/v1alpha1"
)

// InlineSource handles retrieval of configuration data from inline JSON values
type InlineSource struct {
	config *decositesv1alpha1.InlineSource
}

// NewInlineSource creates a new InlineSource with the given configuration
func NewInlineSource(config *decositesv1alpha1.InlineSource) *InlineSource {
	return &InlineSource{config: config}
}

// Retrieve converts inline JSON values to a single JSON string
func (s *InlineSource) Retrieve(ctx context.Context) (string, error) {
	// Build a map of filename to JSON content using RawMessage to avoid double-encoding
	filesJSON := make(map[string]json.RawMessage)
	for key, rawExt := range s.config.Value {
		// RawExtension.Raw is already JSON bytes
		if len(rawExt.Raw) == 0 {
			return "", fmt.Errorf("empty value for key %s", key)
		}
		filesJSON[key] = json.RawMessage(rawExt.Raw)
	}

	// Marshal to single JSON string
	jsonBytes, err := json.Marshal(filesJSON)
	if err != nil {
		return "", fmt.Errorf("failed to marshal files to JSON: %w", err)
	}

	return string(jsonBytes), nil
}

// SourceType returns the source type identifier
func (s *InlineSource) SourceType() string {
	return SourceTypeInline
}
