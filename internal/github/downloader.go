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

package github

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
)

// Downloader handles downloading and extracting files from GitHub repositories
type Downloader struct {
	Token string
}

// BuildZipURL creates the codeload URL for downloading repository as ZIP
func BuildZipURL(org, repo, commit string) string {
	return fmt.Sprintf("https://codeload.github.com/%s/%s/zip/%s", org, repo, commit)
}

// DownloadAndExtract downloads ZIP from GitHub and extracts files from specified path
func (d *Downloader) DownloadAndExtract(org, repo, commit, path string) (map[string][]byte, error) {
	url := BuildZipURL(org, repo, commit)

	// Create HTTP request
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Add authorization header if token exists
	if d.Token != "" {
		req.Header.Set("Authorization", fmt.Sprintf("token %s", d.Token))
	}

	// Download ZIP
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to download: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			err = fmt.Errorf("failed to close response body: %w", closeErr)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to download: status %d", resp.StatusCode)
	}

	// Read ZIP into memory
	zipData, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// Extract files
	return extractFiles(zipData, path)
}

func extractFiles(zipData []byte, targetPath string) (map[string][]byte, error) {
	reader, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return nil, fmt.Errorf("failed to read zip: %w", err)
	}

	files := make(map[string][]byte)
	var rootDir string

	for i, file := range reader.File {
		// First entry is typically the root directory
		if i == 0 {
			if file.FileInfo().IsDir() {
				rootDir = file.Name
			}
			continue
		}

		// Skip directories
		if file.FileInfo().IsDir() {
			continue
		}

		// Remove root directory prefix and check if in target path
		relativePath := strings.TrimPrefix(file.Name, rootDir)

		// Normalize path separators
		relativePath = filepath.ToSlash(relativePath)
		targetPath = filepath.ToSlash(targetPath)

		// Check if file is within target path
		if !strings.HasPrefix(relativePath, targetPath) {
			continue
		}

		// Read file content
		rc, err := file.Open()
		if err != nil {
			return nil, fmt.Errorf("failed to open file %s: %w", file.Name, err)
		}

		content, err := io.ReadAll(rc)
		if closeErr := rc.Close(); closeErr != nil {
			return nil, fmt.Errorf("failed to close file %s: %w", file.Name, closeErr)
		}
		if err != nil {
			return nil, fmt.Errorf("failed to read file %s: %w", file.Name, err)
		}

		// Use filename without full path as key (just the basename)
		filename := filepath.Base(file.Name)
		files[filename] = content
	}

	return files, nil
}
