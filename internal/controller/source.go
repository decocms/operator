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

	"sigs.k8s.io/controller-runtime/pkg/client"

	decositesv1alpha1 "github.com/deco-sites/decofile-operator/api/v1alpha1"
)

const (
	SourceTypeInline = "inline"
	SourceTypeGitHub = "github"
)

// DecofileSource is an interface for retrieving configuration data from different sources
type DecofileSource interface {
	// Retrieve fetches the configuration data and returns it as a map of filename to content
	Retrieve(ctx context.Context) (map[string]string, error)
	// SourceType returns the type of source (inline, github, etc.)
	SourceType() string
}

// NewSource creates the appropriate DecofileSource implementation based on the Decofile spec
func NewSource(k8sClient client.Client, decofile *decositesv1alpha1.Decofile) (DecofileSource, error) {
	switch decofile.Spec.Source {
	case SourceTypeInline:
		if decofile.Spec.Inline == nil {
			return nil, fmt.Errorf("inline source specified but no inline data provided")
		}
		return NewInlineSource(decofile.Spec.Inline), nil
	case SourceTypeGitHub:
		if decofile.Spec.GitHub == nil {
			return nil, fmt.Errorf("github source specified but no github config provided")
		}
		return NewGitHubSource(k8sClient, decofile.Spec.GitHub, decofile.Namespace), nil
	default:
		return nil, fmt.Errorf("unknown source type: %s (must be '%s' or '%s')",
			decofile.Spec.Source, SourceTypeInline, SourceTypeGitHub)
	}
}
