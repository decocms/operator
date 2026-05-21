package api

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

	decositesv1alpha1 "github.com/deco-sites/decofile-operator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const defaultNamespace = "deco-redirect-system"

var domainRe = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?)*$`)

type Handlers struct {
	client client.Client
}

func NewHandlers(c client.Client) *Handlers {
	return &Handlers{client: c}
}

type redirectRequest struct {
	From      string `json:"from"`
	To        string `json:"to"`
	Namespace string `json:"namespace,omitempty"`
}

func (h *Handlers) create(w http.ResponseWriter, r *http.Request) {
	var req redirectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	req.From = sanitizeDomain(req.From)
	if !domainRe.MatchString(req.From) {
		http.Error(w, "invalid domain in 'from'", http.StatusBadRequest)
		return
	}
	if req.To == "" {
		http.Error(w, "'to' is required", http.StatusBadRequest)
		return
	}
	ns := nsOrDefault(req.Namespace)

	rd := &decositesv1alpha1.RedirectDomain{
		ObjectMeta: metav1.ObjectMeta{
			Name:      sanitizeDomain(req.From),
			Namespace: ns,
		},
		Spec: decositesv1alpha1.RedirectDomainSpec{
			From: req.From,
			To:   req.To,
		},
	}
	if err := h.client.Create(r.Context(), rd); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (h *Handlers) delete(w http.ResponseWriter, r *http.Request) {
	domain := sanitizeDomain(r.PathValue("domain"))
	ns := nsOrDefault(r.URL.Query().Get("namespace"))

	rd := &decositesv1alpha1.RedirectDomain{
		ObjectMeta: metav1.ObjectMeta{Name: domain, Namespace: ns},
	}
	if err := h.client.Delete(r.Context(), rd); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) list(w http.ResponseWriter, r *http.Request) {
	ns := nsOrDefault(r.URL.Query().Get("namespace"))

	list := &decositesv1alpha1.RedirectDomainList{}
	if err := h.client.List(r.Context(), list, client.InNamespace(ns)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(list.Items)
}

func sanitizeDomain(d string) string {
	d = strings.ToLower(strings.TrimSpace(d))
	return strings.ReplaceAll(d, ".", "-")
}

func nsOrDefault(ns string) string {
	if ns == "" {
		return defaultNamespace
	}
	return ns
}
