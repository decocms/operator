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
	"net/url"
	"os"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	decositesv1alpha1 "github.com/deco-sites/decofile-operator/api/v1alpha1"
	"github.com/deco-sites/decofile-operator/internal/github"
)

// GitHubSource handles retrieval of configuration data from GitHub repositories
type GitHubSource struct {
	client    client.Client
	config    *decositesv1alpha1.GitHubSource
	namespace string
}

// NewGitHubSource creates a new GitHubSource with the given configuration
func NewGitHubSource(k8sClient client.Client, config *decositesv1alpha1.GitHubSource, namespace string) *GitHubSource {
	return &GitHubSource{
		client:    k8sClient,
		config:    config,
		namespace: namespace,
	}
}

// Retrieve downloads files from GitHub and returns them as a single JSON string
func (s *GitHubSource) Retrieve(ctx context.Context) (string, error) {
	log := logf.FromContext(ctx)

	var token string

	// Get GitHub token from secret or environment variable
	if s.config.Secret != "" {
		// Fetch GitHub token from Kubernetes secret
		secret := &corev1.Secret{}
		err := s.client.Get(ctx, types.NamespacedName{
			Name:      s.config.Secret,
			Namespace: s.namespace,
		}, secret)
		if err != nil {
			return "", fmt.Errorf("failed to get secret %s: %w", s.config.Secret, err)
		}

		token = string(secret.Data["token"])
		if token == "" {
			return "", fmt.Errorf("secret %s does not contain 'token' key", s.config.Secret)
		}
		log.V(1).Info("Using GitHub token from secret", "secret", s.config.Secret)
	} else {
		// Fall back to environment variable
		token = os.Getenv("GITHUB_TOKEN")
		log.V(1).Info("Using GitHub token from GITHUB_TOKEN environment variable")
	}

	// Download and extract from GitHub
	log.Info("Downloading from GitHub",
		"org", s.config.Org,
		"repo", s.config.Repo,
		"commit", s.config.Commit,
		"path", s.config.Path)

	downloader := &github.Downloader{Token: token}
	files, err := downloader.DownloadAndExtract(
		s.config.Org,
		s.config.Repo,
		s.config.Commit,
		s.config.Path,
	)
	if err != nil {
		return "", fmt.Errorf("failed to download from github: %w", err)
	}

	// Store all files as a single JSON object to preserve original filenames
	// (ConfigMap keys have strict character restrictions)
	// Parse each file as JSON to avoid double-stringification
	filesJSON := make(map[string]json.RawMessage)
	for filename, content := range files {
		// URL decode filename (e.g., %20 -> space, %2F -> /)
		decodedFilename, err := url.QueryUnescape(filename)
		if err != nil {
			// If decode fails, use original
			log.V(1).Info("Failed to decode filename, using original", "filename", filename, "error", err)
			decodedFilename = filename
		}

		// Strip .json extension from filename
		cleanFilename := strings.TrimSuffix(decodedFilename, ".json")
		filesJSON[cleanFilename] = json.RawMessage(content)
	}

	// Marshal to JSON without HTML escaping (preserves &, <, > characters)
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	err = encoder.Encode(filesJSON)
	if err != nil {
		return "", fmt.Errorf("failed to marshal files to JSON: %w", err)
	}

	log.Info("Successfully downloaded from GitHub", "files", len(files))

	return strings.TrimSpace(buf.String()), nil
}

// SourceType returns the source type identifier
func (s *GitHubSource) SourceType() string {
	return SourceTypeGitHub
}
