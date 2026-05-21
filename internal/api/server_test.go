package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	decositesv1alpha1 "github.com/deco-sites/decofile-operator/api/v1alpha1"
	"github.com/deco-sites/decofile-operator/internal/api"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestBasicAuth_Unauthorized(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = decositesv1alpha1.AddToScheme(scheme)
	h := api.NewHandlers(fake.NewClientBuilder().WithScheme(scheme).Build(), "deco-redirect-system")
	srv := api.NewServer(":0", "user", "pass", h)
	_ = srv // server registered

	// Build handler directly to test without starting the TCP listener
	req := httptest.NewRequest(http.MethodGet, "/redirects", nil)
	rec := httptest.NewRecorder()
	// server wraps mux in basicAuth — exercise via exported Handler
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestCreate_HappyPath(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = decositesv1alpha1.AddToScheme(scheme)
	fc := fake.NewClientBuilder().WithScheme(scheme).Build()
	h := api.NewHandlers(fc, "deco-redirect-system")
	srv := api.NewServer(":0", "user", "pass", h)

	body, _ := json.Marshal(map[string]string{"from": "example.com", "to": "https://www.example.com"})
	req := httptest.NewRequest(http.MethodPost, "/redirects", bytes.NewReader(body))
	req.SetBasicAuth("user", "pass")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	list := &decositesv1alpha1.RedirectDomainList{}
	_ = fc.List(context.Background(), list)
	if len(list.Items) != 1 {
		t.Fatalf("expected 1 RedirectDomain, got %d", len(list.Items))
	}
}

func TestDelete_HappyPath(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = decositesv1alpha1.AddToScheme(scheme)
	fc := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&decositesv1alpha1.RedirectDomain{
		ObjectMeta: metav1.ObjectMeta{Name: "example-com", Namespace: "deco-redirect-system"},
		Spec:       decositesv1alpha1.RedirectDomainSpec{From: "example.com", To: "https://www.example.com"},
	}).Build()
	h := api.NewHandlers(fc, "deco-redirect-system")
	srv := api.NewServer(":0", "user", "pass", h)

	req := httptest.NewRequest(http.MethodDelete, "/redirects/example.com", nil)
	req.SetBasicAuth("user", "pass")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestGet_HappyPath(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = decositesv1alpha1.AddToScheme(scheme)
	fc := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&decositesv1alpha1.RedirectDomain{
		ObjectMeta: metav1.ObjectMeta{Name: "example-com", Namespace: "deco-redirect-system"},
		Spec:       decositesv1alpha1.RedirectDomainSpec{From: "example.com", To: "https://www.example.com"},
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
	var item decositesv1alpha1.RedirectDomain
	_ = json.NewDecoder(rec.Body).Decode(&item)
	if item.Spec.From != "example.com" {
		t.Fatalf("expected from=example.com, got %s", item.Spec.From)
	}
}

func TestGet_NotFound(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = decositesv1alpha1.AddToScheme(scheme)
	fc := fake.NewClientBuilder().WithScheme(scheme).Build()
	h := api.NewHandlers(fc, "deco-redirect-system")
	srv := api.NewServer(":0", "user", "pass", h)

	req := httptest.NewRequest(http.MethodGet, "/redirects/notfound.com", nil)
	req.SetBasicAuth("user", "pass")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestList_HappyPath(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = decositesv1alpha1.AddToScheme(scheme)
	fc := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&decositesv1alpha1.RedirectDomain{
		ObjectMeta: metav1.ObjectMeta{Name: "example-com", Namespace: "deco-redirect-system"},
		Spec:       decositesv1alpha1.RedirectDomainSpec{From: "example.com", To: "https://www.example.com"},
	}).Build()
	h := api.NewHandlers(fc, "deco-redirect-system")
	srv := api.NewServer(":0", "user", "pass", h)

	req := httptest.NewRequest(http.MethodGet, "/redirects", nil)
	req.SetBasicAuth("user", "pass")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var items []decositesv1alpha1.RedirectDomain
	_ = json.NewDecoder(rec.Body).Decode(&items)
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
}
