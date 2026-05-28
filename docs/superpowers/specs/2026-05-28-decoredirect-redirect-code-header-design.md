# DecoRedirect: redirectCode field + X-Redirect-By header

**Date:** 2026-05-28  
**Status:** Approved

---

## Problem

The `DecoRedirect` CRD currently hard-codes a 301 redirect via the `permanent-redirect` nginx annotation. There is no way to configure the redirect code per client, and no response header to identify that a redirect was served by Deco.

---

## Goals

1. Add a `redirectCode` field to `DecoRedirectSpec` accepting `301` or `307`, defaulting to `307`.
2. Validate the field at the CRD level (kubebuilder enum) so invalid values are rejected by the API server.
3. Add `X-Redirect-By: deco` to all redirect responses via nginx `add-headers`.

---

## Design

### 1. CRD — `redirectCode` field

Add to `DecoRedirectSpec`:

```go
// RedirectCode is the HTTP status code used for the redirect. Must be 301 or 307.
// Defaults to 307 if not set.
// +kubebuilder:validation:Enum=301;307
// +kubebuilder:default=307
// +optional
RedirectCode *int `json:"redirectCode,omitempty"`
```

- Use a pointer (`*int`) so the controller can distinguish "not set" (nil) from an explicit value.
- `+kubebuilder:default=307` makes the API server inject 307 on new CREATE requests when the field is omitted.
- Existing CRs (created before this change) will have `nil` at read time — the controller treats `nil` as 307.
- Validation is enforced by the Kubernetes API server via the generated CRD schema — no webhook needed.
- The HTTP API (`redirectRequest` / `redirectResponse`) exposes `redirectCode` as an optional `*int`; the handler passes it through to the CR spec.

### 2. Controller — `reconcileIngress` change

In `reconcileIngress`, set both annotations per Ingress:

```go
code := 307
if rd.Spec.RedirectCode != nil {
    code = *rd.Spec.RedirectCode
}
ingress.Annotations = map[string]string{
    "nginx.ingress.kubernetes.io/permanent-redirect":      rd.Spec.To,
    "nginx.ingress.kubernetes.io/permanent-redirect-code": strconv.Itoa(code),
}
```

`permanent-redirect-code` is a per-Ingress annotation — each client's Ingress carries its own value, so 301 and 307 clients coexist without conflict.

### 3. Header — Helm chart changes (opt-in)

This is an open-source chart. The header feature must be opt-in, parallel to `ingress-nginx.enabled`. A new value gates both the ConfigMap and the nginx config entry:

```yaml
# values.yaml
redirect:
  decoHeader:
    enabled: true   # set to false to disable X-Redirect-By header entirely
    value: "deco"   # value for the X-Redirect-By header
```

**New ConfigMap** (rendered conditionally in the chart):

```yaml
{{- if .Values.redirect.decoHeader.enabled }}
apiVersion: v1
kind: ConfigMap
metadata:
  name: deco-custom-headers
  namespace: {{ .Values.redirect.namespace }}
data:
  X-Redirect-By: {{ .Values.redirect.decoHeader.value | quote }}
{{- end }}
```

**nginx values** — the `add-headers` key is only injected when the feature is enabled. This is done by merging it into `ingress-nginx.controller.config` conditionally in the chart templates, not in `values.yaml`, so that consumers who set `redirect.decoHeader.enabled: false` are not affected.

The nginx ingress controller reads the ConfigMap at startup and appends the headers to every response. Since this nginx instance is exclusively used for Deco redirects, a global header is correct behavior.

---

## HTTP API changes

`POST /redirects` request body gains an optional field:

```json
{
  "from": "client.com",
  "to": "https://www.client.com",
  "redirectCode": 307
}
```

`GET /redirects` and `GET /redirects/{domain}` response gains:

```json
{
  "from": "client.com",
  "to": "https://www.client.com",
  "redirectCode": 307,
  "certificateReady": true,
  "createdAt": "2026-05-28T00:00:00Z"
}
```

Omitting `redirectCode` in POST defaults to `307` (kubebuilder default).  
Invalid values (`302`, `308`, etc.) return `422 Unprocessable Entity` from the API server.

---

## Files to change

| File | Change |
|------|--------|
| `api/v1alpha1/decoredirect_types.go` | Add `RedirectCode int` field with kubebuilder markers |
| `internal/controller/decoredirect_controller.go` | Set `permanent-redirect-code` annotation in `reconcileIngress` |
| `internal/api/handlers.go` | Add `redirectCode` to `redirectRequest` and `redirectResponse`; pass through in `create`; render in `toResponse` |
| `internal/controller/decoredirect_controller_test.go` | Update/add tests for redirect code annotation |
| `internal/api/server_test.go` | Update/add tests for redirectCode in request/response |
| `chart/` | Add conditional ConfigMap template for `deco-custom-headers`; add `redirect.decoHeader.enabled` value; conditionally inject `add-headers` into nginx controller config |
| `config/crd/bases/` | Regenerate via `make generate manifests` |

---

## Out of scope

- Supporting redirect codes other than 301 and 307.
- Per-client header values.
- Migrating existing CRs — existing CRs will have `nil` for `redirectCode`; the controller treats `nil` as 307. No explicit migration needed.
