# DecoRedirect: redirectCode + X-Redirect-By Header — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a configurable `redirectCode` field (301 or 307, default 307) to the `DecoRedirect` CRD, and add an opt-in `X-Redirect-By` response header to all redirects via the nginx ingress controller.

**Architecture:** The `redirectCode` field is enforced at the CRD schema level via kubebuilder enum validation and rendered as `nginx.ingress.kubernetes.io/permanent-redirect-code` on each per-domain Ingress. The header is injected globally by the nginx ingress controller via a ConfigMap referenced by `add-headers`, gated by `redirect.decoHeader.enabled` in the Helm values.

**Tech Stack:** Go 1.23, kubebuilder v4, controller-runtime, ingress-nginx, Helm 3, envtest (Ginkgo/Gomega for controller tests, stdlib `testing` for API tests).

---

## File Map

| File | Action | Purpose |
|------|--------|---------|
| `api/v1alpha1/decoredirect_types.go` | Modify | Add `RedirectCode *int` field with kubebuilder markers |
| `config/crd/bases/deco.sites_decoredict.yaml` | Auto-regenerated | CRD schema with enum + default |
| `chart/templates/customresourcedefinition-decoredict.deco.sites.yaml` | Auto-regenerated | Helm-bundled CRD |
| `internal/controller/decoredirect_controller.go` | Modify | Set `permanent-redirect-code` annotation in `reconcileIngress` |
| `internal/controller/decoredirect_controller_test.go` | Modify | New tests: redirectCode annotation + invalid value rejection |
| `internal/api/handlers.go` | Modify | Add `redirectCode` to request/response structs and wire up |
| `internal/api/server_test.go` | Modify | New tests: redirectCode in POST and GET |
| `chart/templates/configmap-redirect-custom-headers.yaml` | Create | Conditional ConfigMap for `X-Redirect-By` header |
| `chart/values.yaml` | Modify | Add `redirect.decoHeader` block; document `ingress-nginx.controller.config.add-headers` |

---

## Task 1: Add `redirectCode` field to CRD types

**Files:**
- Modify: `api/v1alpha1/decoredirect_types.go`

- [ ] **Step 1: Write the failing controller test for invalid redirectCode**

Add this `It` block inside the existing `Context("When reconciling a DecoRedirect", ...)` in `internal/controller/decoredirect_controller_test.go`, after the last `It` block:

```go
It("should reject a DecoRedirect with an invalid redirectCode", func() {
    invalidCode := 302
    err := k8sClient.Create(ctx, &decositesv1alpha1.DecoRedirect{
        ObjectMeta: metav1.ObjectMeta{Name: "invalid-code", Namespace: rdNS},
        Spec: decositesv1alpha1.DecoRedirectSpec{
            From:         "invalid-code.com",
            To:           "https://www.invalid-code.com",
            RedirectCode: &invalidCode,
        },
    })
    Expect(err).To(HaveOccurred())
    Expect(err.Error()).To(ContainSubstring("redirectCode"))
})
```

- [ ] **Step 2: Run the test to confirm it fails to compile** (field doesn't exist yet)

```bash
cd /Users/igoramf/projects/deco/operator
go test ./internal/controller/... 2>&1 | head -20
```

Expected: compile error `unknown field RedirectCode in struct literal`

- [ ] **Step 3: Add `RedirectCode` to `DecoRedirectSpec`**

In `api/v1alpha1/decoredirect_types.go`, add the field after the `To` field:

```go
// DecoRedirectSpec defines the desired state of DecoRedirect.
// +kubebuilder:validation:XValidation:rule="(self.to+'/').contains('.'+self.from+'/') || (self.to+'/').contains('//'+self.from+'/')",message="redirect target must be within the same domain as 'from' (e.g. from: client.com → to: https://www.client.com)"
type DecoRedirectSpec struct {
	// From is the apex domain to redirect (e.g. "client.com").
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[a-zA-Z0-9]([a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?)*$`
	From string `json:"from"`

	// To is the full target URL within the same domain (e.g. "https://www.client.com").
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=2048
	// +kubebuilder:validation:Pattern=`^https?://`
	To string `json:"to"`

	// RedirectCode is the HTTP status code used for the redirect. Allowed values: 301, 307.
	// Defaults to 307 (Temporary Redirect) if not set.
	// +kubebuilder:validation:Enum=301;307
	// +kubebuilder:default=307
	// +optional
	RedirectCode *int `json:"redirectCode,omitempty"`
}
```

- [ ] **Step 4: Regenerate DeepCopy, CRD manifests, and Helm chart**

```bash
cd /Users/igoramf/projects/deco/operator
make generate
```

Expected: exits 0. This regenerates:
- `zz_generated.deepcopy.go` (adds `RedirectCode` pointer copy)
- `config/crd/bases/deco.sites_decoredict.yaml` (adds `redirectCode` with enum + default)
- `chart/templates/customresourcedefinition-decoredict.deco.sites.yaml` (same, Helm copy)

- [ ] **Step 5: Run the controller tests**

```bash
cd /Users/igoramf/projects/deco/operator
make test 2>&1 | tail -20
```

Expected: all existing tests pass. The new test for `invalid-code` should **fail** because the CRD schema doesn't enforce enum on the fake client yet — envtest uses the real API server, so it **should** fail with an error containing `redirectCode`. If the test passes (correctly rejects the invalid value), great — move on.

> Note: If envtest rejects the invalid value, the test passes. If it doesn't (unlikely), check that `make generate` correctly updated the CRD YAML with `enum: [301, 307]`.

- [ ] **Step 6: Commit**

```bash
git add api/v1alpha1/decoredirect_types.go \
        api/v1alpha1/zz_generated.deepcopy.go \
        config/crd/bases/ \
        chart/templates/customresourcedefinition-decoredict.deco.sites.yaml \
        internal/controller/decoredirect_controller_test.go
git commit -m "feat(crd): add redirectCode field (enum 301|307, default 307) to DecoRedirect"
```

---

## Task 2: Controller — set `permanent-redirect-code` annotation

**Files:**
- Modify: `internal/controller/decoredirect_controller.go`
- Modify: `internal/controller/decoredirect_controller_test.go`

- [ ] **Step 1: Write the failing test for redirectCode annotation**

Add two `It` blocks to `decoredirect_controller_test.go` inside the existing `Context`, after the test added in Task 1:

```go
It("should set permanent-redirect-code to 307 by default", func() {
    _, err := newReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: nn})
    Expect(err).NotTo(HaveOccurred())

    ing := &networkingv1.Ingress{}
    Expect(k8sClient.Get(ctx, types.NamespacedName{
        Name: "redirect-client-com", Namespace: rdNS,
    }, ing)).To(Succeed())
    Expect(ing.Annotations["nginx.ingress.kubernetes.io/permanent-redirect-code"]).To(Equal("307"))
})

It("should use redirectCode 301 in the Ingress annotation when specified", func() {
    code := 301
    rd301 := &decositesv1alpha1.DecoRedirect{
        ObjectMeta: metav1.ObjectMeta{Name: "test-redirect-301", Namespace: rdNS},
        Spec: decositesv1alpha1.DecoRedirectSpec{
            From:         "redirect301.com",
            To:           "https://www.redirect301.com",
            RedirectCode: &code,
        },
    }
    Expect(k8sClient.Create(ctx, rd301)).To(Succeed())
    DeferCleanup(func() { _ = k8sClient.Delete(ctx, rd301) })

    nn301 := types.NamespacedName{Name: "test-redirect-301", Namespace: rdNS}
    _, err := newReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: nn301})
    Expect(err).NotTo(HaveOccurred())

    ing := &networkingv1.Ingress{}
    Expect(k8sClient.Get(ctx, types.NamespacedName{
        Name: "redirect-redirect301-com", Namespace: rdNS,
    }, ing)).To(Succeed())
    Expect(ing.Annotations["nginx.ingress.kubernetes.io/permanent-redirect-code"]).To(Equal("301"))
})
```

- [ ] **Step 2: Run the tests — expect the new ones to fail**

```bash
cd /Users/igoramf/projects/deco/operator
go test ./internal/controller/... -v -run "TestControllers" 2>&1 | grep -E "FAIL|PASS|should set permanent|should use redirectCode"
```

Expected: new tests FAIL (annotation not set yet).

- [ ] **Step 3: Update `reconcileIngress` in the controller**

In `internal/controller/decoredirect_controller.go`, add `"strconv"` to the imports, then replace the annotation map in `reconcileIngress`:

Full updated `reconcileIngress` function:

```go
func (r *DecoRedirectReconciler) reconcileIngress(ctx context.Context, rd *decositesv1alpha1.DecoRedirect) error {
	pathType := networkingv1.PathTypePrefix
	ingress := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      resourceName(rd.Spec.From),
			Namespace: rd.Namespace,
		},
	}
	if err := controllerutil.SetControllerReference(rd, ingress, r.Scheme); err != nil {
		return err
	}

	code := 307
	if rd.Spec.RedirectCode != nil {
		code = *rd.Spec.RedirectCode
	}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, ingress, func() error {
		ingress.Annotations = map[string]string{
			"nginx.ingress.kubernetes.io/permanent-redirect":      rd.Spec.To,
			"nginx.ingress.kubernetes.io/permanent-redirect-code": strconv.Itoa(code),
		}
		ingress.Spec = networkingv1.IngressSpec{
			IngressClassName: &r.IngressClass,
			TLS: []networkingv1.IngressTLS{
				{
					Hosts:      []string{rd.Spec.From},
					SecretName: tlsSecretName(rd.Spec.From),
				},
			},
			Rules: []networkingv1.IngressRule{
				{
					Host: rd.Spec.From,
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{
								{
									Path:     "/",
									PathType: &pathType,
									Backend: networkingv1.IngressBackend{
										Service: &networkingv1.IngressServiceBackend{
											Name: dummyBackendName,
											Port: networkingv1.ServiceBackendPort{Number: 80},
										},
									},
								},
							},
						},
					},
				},
			},
		}
		return nil
	})
	return err
}
```

Add `"strconv"` to the import block at the top of the file.

- [ ] **Step 4: Run the tests — expect all to pass**

```bash
cd /Users/igoramf/projects/deco/operator
go test ./internal/controller/... -v 2>&1 | tail -30
```

Expected: all tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/decoredirect_controller.go \
        internal/controller/decoredirect_controller_test.go
git commit -m "feat(controller): set permanent-redirect-code annotation from spec.redirectCode"
```

---

## Task 3: HTTP API — expose `redirectCode` in request and response

**Files:**
- Modify: `internal/api/handlers.go`
- Modify: `internal/api/server_test.go`

- [ ] **Step 1: Write failing tests for `redirectCode` in POST and GET**

Add these test functions to `internal/api/server_test.go`:

```go
func TestCreate_WithRedirectCode(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = decositesv1alpha1.AddToScheme(scheme)
	fc := fake.NewClientBuilder().WithScheme(scheme).Build()
	h := api.NewHandlers(fc, "deco-redirect-system")
	srv := api.NewServer(":0", "user", "pass", h)

	code := 301
	body, _ := json.Marshal(map[string]interface{}{"from": "example.com", "to": "https://www.example.com", "redirectCode": code})
	req := httptest.NewRequest(http.MethodPost, "/redirects", bytes.NewReader(body))
	req.SetBasicAuth("user", "pass")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	list := &decositesv1alpha1.DecoRedirectList{}
	_ = fc.List(context.Background(), list)
	if len(list.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(list.Items))
	}
	if list.Items[0].Spec.RedirectCode == nil || *list.Items[0].Spec.RedirectCode != 301 {
		t.Fatalf("expected redirectCode=301, got %v", list.Items[0].Spec.RedirectCode)
	}
}

func TestGet_IncludesRedirectCode(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = decositesv1alpha1.AddToScheme(scheme)
	code := 301
	fc := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&decositesv1alpha1.DecoRedirect{
		ObjectMeta: metav1.ObjectMeta{Name: "example-com", Namespace: "deco-redirect-system"},
		Spec: decositesv1alpha1.DecoRedirectSpec{
			From:         "example.com",
			To:           "https://www.example.com",
			RedirectCode: &code,
		},
	}).Build()
	h := api.NewHandlers(fc, "deco-redirect-system")
	srv := api.NewServer(":0", "user", "pass", h)

	req := httptest.NewRequest(http.MethodGet, "/redirects/example.com", nil)
	req.SetBasicAuth("user", "pass")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var item struct {
		From         string `json:"from"`
		RedirectCode *int   `json:"redirectCode"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&item)
	if item.RedirectCode == nil || *item.RedirectCode != 301 {
		t.Fatalf("expected redirectCode=301 in response, got %v", item.RedirectCode)
	}
}
```

- [ ] **Step 2: Run the new tests — expect them to fail**

```bash
cd /Users/igoramf/projects/deco/operator
go test ./internal/api/... -v -run "TestCreate_WithRedirectCode|TestGet_IncludesRedirectCode" 2>&1
```

Expected: FAIL — `RedirectCode` field unknown in request struct.

- [ ] **Step 3: Update `handlers.go`**

Replace the full content of `internal/api/handlers.go` with:

```go
package api

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

	decositesv1alpha1 "github.com/deco-sites/decofile-operator/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var domainRe = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?)*$`)

type Handlers struct {
	client           client.Client
	defaultNamespace string
}

func NewHandlers(c client.Client, defaultNamespace string) *Handlers {
	if defaultNamespace == "" {
		defaultNamespace = "deco-redirect-system"
	}
	return &Handlers{client: c, defaultNamespace: defaultNamespace}
}

type redirectRequest struct {
	From         string `json:"from"`
	To           string `json:"to"`
	Namespace    string `json:"namespace,omitempty"`
	RedirectCode *int   `json:"redirectCode,omitempty"`
}

type redirectResponse struct {
	From             string `json:"from"`
	To               string `json:"to"`
	RedirectCode     *int   `json:"redirectCode,omitempty"`
	CertificateReady bool   `json:"certificateReady"`
	Message          string `json:"message,omitempty"`
	CreatedAt        string `json:"createdAt"`
}

func toResponse(rd *decositesv1alpha1.DecoRedirect) redirectResponse {
	resp := redirectResponse{
		From:         rd.Spec.From,
		To:           rd.Spec.To,
		RedirectCode: rd.Spec.RedirectCode,
		CreatedAt:    rd.CreationTimestamp.UTC().Format("2006-01-02T15:04:05Z"),
	}
	for _, c := range rd.Status.Conditions {
		if c.Type == "CertificateReady" {
			resp.CertificateReady = c.Status == "True"
			if c.Status != "True" {
				resp.Message = c.Message
			}
			break
		}
	}
	return resp
}

func (h *Handlers) create(w http.ResponseWriter, r *http.Request) {
	var req redirectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	from := strings.ToLower(strings.TrimSpace(req.From))
	if !domainRe.MatchString(from) {
		http.Error(w, "invalid domain in 'from'", http.StatusBadRequest)
		return
	}
	to := strings.TrimSpace(req.To)
	if to == "" {
		http.Error(w, "'to' is required", http.StatusBadRequest)
		return
	}
	if !strings.HasPrefix(to, "http://") && !strings.HasPrefix(to, "https://") {
		to = "https://" + to
	}
	ns := h.nsOrDefault(req.Namespace)

	rd := &decositesv1alpha1.DecoRedirect{
		ObjectMeta: metav1.ObjectMeta{
			Name:      domainToName(from),
			Namespace: ns,
		},
		Spec: decositesv1alpha1.DecoRedirectSpec{
			From:         from,
			To:           to,
			RedirectCode: req.RedirectCode,
		},
	}
	if err := h.client.Create(r.Context(), rd); err != nil {
		status := http.StatusInternalServerError
		if apierrors.IsInvalid(err) {
			status = http.StatusUnprocessableEntity
		} else if apierrors.IsAlreadyExists(err) {
			status = http.StatusConflict
		}
		http.Error(w, err.Error(), status)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (h *Handlers) get(w http.ResponseWriter, r *http.Request) {
	rawDomain := strings.ToLower(strings.TrimSpace(r.PathValue("domain")))
	if !domainRe.MatchString(rawDomain) {
		http.Error(w, "invalid domain", http.StatusBadRequest)
		return
	}
	domain := domainToName(rawDomain)
	ns := h.nsOrDefault(r.URL.Query().Get("namespace"))

	rd := &decositesv1alpha1.DecoRedirect{}
	if err := h.client.Get(r.Context(), client.ObjectKey{Name: domain, Namespace: ns}, rd); err != nil {
		status := http.StatusInternalServerError
		if apierrors.IsNotFound(err) {
			status = http.StatusNotFound
		}
		http.Error(w, err.Error(), status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(toResponse(rd))
}

func (h *Handlers) delete(w http.ResponseWriter, r *http.Request) {
	rawDomain := strings.ToLower(strings.TrimSpace(r.PathValue("domain")))
	if !domainRe.MatchString(rawDomain) {
		http.Error(w, "invalid domain", http.StatusBadRequest)
		return
	}
	domain := domainToName(rawDomain)
	ns := h.nsOrDefault(r.URL.Query().Get("namespace"))

	rd := &decositesv1alpha1.DecoRedirect{
		ObjectMeta: metav1.ObjectMeta{Name: domain, Namespace: ns},
	}
	if err := h.client.Delete(r.Context(), rd); err != nil {
		status := http.StatusInternalServerError
		if apierrors.IsNotFound(err) {
			status = http.StatusNotFound
		}
		http.Error(w, err.Error(), status)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) list(w http.ResponseWriter, r *http.Request) {
	ns := h.nsOrDefault(r.URL.Query().Get("namespace"))

	list := &decositesv1alpha1.DecoRedirectList{}
	if err := h.client.List(r.Context(), list, client.InNamespace(ns)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	items := make([]redirectResponse, len(list.Items))
	for i := range list.Items {
		items[i] = toResponse(&list.Items[i])
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(items)
}

// domainToName converts a domain to a valid k8s resource name (dots → dashes).
func domainToName(d string) string {
	return strings.ReplaceAll(d, ".", "-")
}

func (h *Handlers) nsOrDefault(ns string) string {
	if ns == "" {
		return h.defaultNamespace
	}
	return ns
}
```

- [ ] **Step 4: Run all API tests**

```bash
cd /Users/igoramf/projects/deco/operator
go test ./internal/api/... -v 2>&1 | tail -20
```

Expected: all tests PASS.

- [ ] **Step 5: Run the full test suite**

```bash
cd /Users/igoramf/projects/deco/operator
make test 2>&1 | tail -10
```

Expected: PASS, no failures.

- [ ] **Step 6: Commit**

```bash
git add internal/api/handlers.go internal/api/server_test.go
git commit -m "feat(api): add redirectCode to DecoRedirect request and response"
```

---

## Task 4: Helm chart — opt-in `X-Redirect-By` header

**Files:**
- Create: `chart/templates/configmap-redirect-custom-headers.yaml`
- Modify: `chart/values.yaml`

- [ ] **Step 1: Add `redirect.decoHeader` to `values.yaml` and wire up `ingress-nginx.controller.config`**

**Change 1:** In `chart/values.yaml`, add the `decoHeader` block inside the `redirect:` section, after the `clusterIssuer:` block. The full `redirect:` block after the edit:

```yaml
redirect:
  namespace: "deco-redirect-system"
  ingressClass: ""          # set to enable DecoRedirect controller (e.g. "redirect-nginx")
  clusterIssuer:
    enabled: false          # set true to create the Let's Encrypt ClusterIssuer
    name: ""                # ClusterIssuer name (e.g. "letsencrypt")
    email: ""               # required by Let's Encrypt ACME
    staging: false          # set true to use Let's Encrypt staging (avoids rate limits when testing)
    solverAnnotations: {}   # extra annotations on the HTTP-01 challenge Ingress
  decoHeader:
    enabled: false  # set true to add X-Redirect-By response header to all redirects served by this nginx instance
    value: "deco"   # value for the X-Redirect-By header; override to use your own identifier
```

**Change 2:** In the `ingress-nginx:` section of `chart/values.yaml`, add `add-headers` to `controller.config`. The full updated `ingress-nginx:` block:

```yaml
ingress-nginx:
  enabled: false
  namespaceOverride: "deco-redirect-system"
  controller:
    ingressClass: redirect-nginx
    ingressClassResource:
      name: redirect-nginx
      controllerValue: "k8s.io/redirect-nginx"
    service:
      annotations: {}
    config:
      # Set to "<redirect.namespace>/deco-custom-headers" when redirect.decoHeader.enabled=true.
      # The ConfigMap is only created when redirect.decoHeader.enabled=true.
      add-headers: ""
```

- [ ] **Step 2: Create the conditional ConfigMap template**

Create file `chart/templates/configmap-redirect-custom-headers.yaml`:

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

- [ ] **Step 3: Verify the template renders correctly when enabled**

```bash
cd /Users/igoramf/projects/deco/operator
helm template test chart/ --set redirect.decoHeader.enabled=true --set redirect.decoHeader.value=deco 2>&1 | grep -A 8 "deco-custom-headers"
```

Expected output:
```yaml
# Source: deco-operator/templates/configmap-redirect-custom-headers.yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: deco-custom-headers
  namespace: deco-redirect-system
data:
  X-Redirect-By: "deco"
```

- [ ] **Step 4: Verify the template renders nothing when disabled (default)**

```bash
cd /Users/igoramf/projects/deco/operator
helm template test chart/ 2>&1 | grep -c "deco-custom-headers"
```

Expected output: `0` (ConfigMap not present in rendered output)

- [ ] **Step 5: Lint the chart**

```bash
cd /Users/igoramf/projects/deco/operator
helm lint chart/
```

Expected: `1 chart(s) linted, 0 chart(s) failed`

- [ ] **Step 6: Commit**

```bash
git add chart/templates/configmap-redirect-custom-headers.yaml chart/values.yaml
git commit -m "feat(chart): add opt-in X-Redirect-By header via redirect.decoHeader"
```

---

## Final check

- [ ] **Run full test suite one last time**

```bash
cd /Users/igoramf/projects/deco/operator
make test 2>&1 | tail -10
```

Expected: all tests pass, coverage written to `cover.out`.

- [ ] **Verify the branch is clean**

```bash
git log --oneline main..HEAD
```

Expected (4 commits on top of main):
```
<hash> feat(chart): add opt-in X-Redirect-By header via redirect.decoHeader
<hash> feat(api): add redirectCode to DecoRedirect request and response
<hash> feat(controller): set permanent-redirect-code annotation from spec.redirectCode
<hash> feat(crd): add redirectCode field (enum 301|307, default 307) to DecoRedirect
```
