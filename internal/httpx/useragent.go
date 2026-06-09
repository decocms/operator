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

package httpx

import "net/http"

// UserAgent is sent on every outbound HTTP request made by the operator.
const UserAgent = "deco-operator"

type userAgentTransport struct {
	base http.RoundTripper
}

// RoundTrip injects UserAgent into the request when the caller did not set
// one. The request is cloned to honor the http.RoundTripper contract that
// implementations must not modify the original request.
func (t *userAgentTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Header.Get("User-Agent") != "" {
		return t.base.RoundTrip(req)
	}
	r := req.Clone(req.Context())
	r.Header.Set("User-Agent", UserAgent)
	return t.base.RoundTrip(r)
}

// WithUserAgent wraps base so every request carries the operator's User-Agent.
// When base is nil, http.DefaultTransport is used.
func WithUserAgent(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &userAgentTransport{base: base}
}
