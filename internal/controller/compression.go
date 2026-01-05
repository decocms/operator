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
	"time"

	"github.com/andybalholm/brotli"
	ctrl "sigs.k8s.io/controller-runtime"
)

const (
	// brotliCompressionLevel is set to 5 for a good balance of speed and compression.
	// Level 11 (BestCompression) is 10-50x slower with only ~10-15% better compression.
	brotliCompressionLevel = 5
	// compressionWarningThreshold logs a warning if compression takes longer than this
	compressionWarningThreshold = 30 * time.Second
)

var compressionLog = ctrl.Log.WithName("compression")

// compressBrotli compresses data using Brotli compression at a balanced level.
// Uses level 5 instead of 11 (BestCompression) for much faster compression.
// Logs a warning if compression takes longer than 30 seconds.
func compressBrotli(data []byte) ([]byte, error) {
	start := time.Now()

	var buf bytes.Buffer
	writer := brotli.NewWriterLevel(&buf, brotliCompressionLevel)

	_, err := writer.Write(data)
	if err != nil {
		_ = writer.Close()
		return nil, err
	}

	err = writer.Close()
	if err != nil {
		return nil, err
	}

	duration := time.Since(start)
	if duration > compressionWarningThreshold {
		compressionLog.Info("WARNING: Brotli compression took longer than expected",
			"duration", duration,
			"inputSize", len(data),
			"outputSize", buf.Len(),
			"threshold", compressionWarningThreshold)
	}

	return buf.Bytes(), nil
}
