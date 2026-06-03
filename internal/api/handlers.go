package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	cmv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	cmmeta "github.com/cert-manager/cert-manager/pkg/apis/meta/v1"
	decositesv1alpha1 "github.com/deco-sites/decofile-operator/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var apiGCPIPv6Range = func() *net.IPNet {
	_, n, _ := net.ParseCIDR("2600:1901::/32")
	return n
}()

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
			Name:      domainToName(from), // dots → dashes for k8s name
			Namespace: ns,
		},
		Spec: decositesv1alpha1.DecoRedirectSpec{
			From:         from, // original domain preserved for CEL validation
			To:           to,
			RedirectCode: req.RedirectCode,
		},
	}
	// redirectCode enum validation (301|307) is enforced by the CRD schema; invalid values return 422.
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

func (h *Handlers) retryCert(w http.ResponseWriter, r *http.Request) {
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

	if err := checkDNSReady(r.Context(), rawDomain); err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}

	cert := &cmv1.Certificate{}
	certName := "redirect-" + domain
	if err := h.client.Get(r.Context(), client.ObjectKey{Name: certName, Namespace: ns}, cert); err == nil {
		// noop: cert is already ready or actively being issued
		for _, c := range cert.Status.Conditions {
			if c.Type == cmv1.CertificateConditionReady && c.Status == cmmeta.ConditionTrue {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(toResponse(rd))
				return
			}
			if c.Type == cmv1.CertificateConditionIssuing && c.Status == cmmeta.ConditionTrue {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(toResponse(rd))
				return
			}
		}
		// stuck in backoff — delete so the controller recreates it fresh
		if err := h.client.Delete(r.Context(), cert); err != nil && !apierrors.IsNotFound(err) {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	} else if !apierrors.IsNotFound(err) {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(toResponse(rd))
}

// checkDNSReady verifica se o domínio está apontando para a infraestrutura Deco:
//  1. HTTP retorna redirect servido pelo nginx deco (X-Redirect-By: deco).
//  2. Sem AAAA no range GCP (2600:1901::/32) que faria o LE validar via Deno Deploy.
func checkDNSReady(ctx context.Context, domain string) error {
	httpClient := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Timeout:       5 * time.Second,
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+domain+"/", nil)
	if err != nil {
		return err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("domain not reachable: %w", err)
	}
	resp.Body.Close()
	if resp.Header.Get("X-Redirect-By") != "deco" {
		return fmt.Errorf("domain is not pointing to Deco redirect IPs yet")
	}

	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, domain)
	if err != nil {
		return fmt.Errorf("DNS lookup failed: %w", err)
	}
	for _, a := range addrs {
		ip := a.IP
		if ip.To4() != nil {
			continue
		}
		if apiGCPIPv6Range.Contains(ip) {
			return fmt.Errorf("AAAA record %s points to Deno Deploy (GCP) — remove it first", ip)
		}
	}
	return nil
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
