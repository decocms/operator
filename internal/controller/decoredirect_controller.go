package controller

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	cmv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	cmmeta "github.com/cert-manager/cert-manager/pkg/apis/meta/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	decositesv1alpha1 "github.com/deco-sites/decofile-operator/api/v1alpha1"
)

// DecoRedirectReconciler reconciles a DecoRedirect object.
type DecoRedirectReconciler struct {
	client.Client
	Scheme        *runtime.Scheme
	IngressClass  string // nginx ingress class name, e.g. "nginx"
	ClusterIssuer string // cert-manager ClusterIssuer name, e.g. "letsencrypt"
	// BlockedIPv6CIDRs is a list of IPv6 CIDR ranges that, if present in a domain's
	// AAAA records, indicate DNS is not ready for cert issuance. Typically legacy
	// infrastructure addresses that intercept Let's Encrypt validation incorrectly.
	// When empty, no AAAA check is performed.
	BlockedIPv6CIDRs []*net.IPNet
	// DNSReadyFunc checks if the domain DNS is correctly pointing to the redirect infrastructure.
	// Defaults to isDNSReady. Injectable for testing.
	DNSReadyFunc func(ctx context.Context, domain string) bool
}

// dummyBackendName satisfies the k8s Ingress API requirement for a backend on every path.
// nginx never routes to it because permanent-redirect intercepts first.
const dummyBackendName = "redirect-dummy-backend"
const DecoRedirectControllerName = "decoredirect"

// +kubebuilder:rbac:groups=deco.sites,resources=decoredict,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=deco.sites,resources=decoredict/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=deco.sites,resources=decoredict/finalizers,verbs=update
// +kubebuilder:rbac:groups=cert-manager.io,resources=certificates,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses,verbs=get;list;watch;create;update;patch;delete

func (r *DecoRedirectReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	rd := &decositesv1alpha1.DecoRedirect{}
	if err := r.Get(ctx, req.NamespacedName, rd); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Auto-heal: if Certificate is stuck in Failed backoff and DNS is now correct, delete it
	// so reconcileCertificate recreates it fresh and cert-manager retries without backoff.
	if healed, err := r.maybeHealCertificate(ctx, rd); err != nil {
		log.Error(err, "failed to heal Certificate")
		return ctrl.Result{}, err
	} else if healed {
		return ctrl.Result{RequeueAfter: 2 * time.Second}, nil
	}

	if err := r.reconcileCertificate(ctx, rd); err != nil {
		log.Error(err, "failed to reconcile Certificate")
		return ctrl.Result{}, err
	}

	if err := r.reconcileIngress(ctx, rd); err != nil {
		log.Error(err, "failed to reconcile Ingress")
		return ctrl.Result{}, err
	}

	certReady, err := r.updateStatus(ctx, rd)
	if err != nil {
		log.Error(err, "failed to update status")
		return ctrl.Result{}, err
	}

	// Only requeue while cert is still provisioning; once ready, Watch events drive reconciliation.
	if !certReady {
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}
	return ctrl.Result{}, nil
}

func (r *DecoRedirectReconciler) reconcileCertificate(ctx context.Context, rd *decositesv1alpha1.DecoRedirect) error {
	cert := &cmv1.Certificate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      resourceName(rd.Spec.From),
			Namespace: rd.Namespace,
		},
	}
	if err := controllerutil.SetControllerReference(rd, cert, r.Scheme); err != nil {
		return err
	}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, cert, func() error {
		// Skip mutation while the object is being deleted — the Watch will re-trigger once gone.
		if cert.DeletionTimestamp != nil {
			return nil
		}
		cert.Spec.SecretName = tlsSecretName(rd.Spec.From)
		cert.Spec.DNSNames = []string{rd.Spec.From}
		cert.Spec.IssuerRef = cmmeta.ObjectReference{
			Name: r.ClusterIssuer,
			Kind: "ClusterIssuer",
		}
		return nil
	})
	return err
}

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

	// server-snippet injects a raw nginx return directive that preserves the full request path and
	// query string via $request_uri. The permanent-redirect annotation cannot be used here because
	// the nginx admission webhook rejects values containing nginx variables (e.g. $request_uri).
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, ingress, func() error {
		ingress.Annotations = map[string]string{
			"nginx.ingress.kubernetes.io/server-snippet": fmt.Sprintf("return %d %s$request_uri;", code, rd.Spec.To),
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

func (r *DecoRedirectReconciler) updateStatus(ctx context.Context, rd *decositesv1alpha1.DecoRedirect) (bool, error) {
	certReady := false
	cert := &cmv1.Certificate{}
	if err := r.Get(ctx, types.NamespacedName{Name: resourceName(rd.Spec.From), Namespace: rd.Namespace}, cert); err != nil {
		if !apierrors.IsNotFound(err) {
			return false, err
		}
	} else {
		for _, c := range cert.Status.Conditions {
			if c.Type == cmv1.CertificateConditionReady && c.Status == cmmeta.ConditionTrue {
				certReady = true
			}
		}
	}

	status := metav1.ConditionFalse
	reason := "Provisioning"
	message := "Certificate is being provisioned by cert-manager"
	if certReady {
		status = metav1.ConditionTrue
		reason = "Issued"
		message = "Certificate has been issued"
	}

	patch := rd.DeepCopy()
	meta.SetStatusCondition(&patch.Status.Conditions, metav1.Condition{
		Type:               decositesv1alpha1.ConditionCertificateReady,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: rd.Generation,
	})

	return certReady, r.Status().Patch(ctx, patch, client.MergeFrom(rd))
}

// maybeHealCertificate deletes a Certificate that is stuck in Failed backoff when DNS
// is already pointing correctly to the Deco redirect infrastructure. Returning true
// means the Certificate was deleted and the caller should requeue before recreating it.
func (r *DecoRedirectReconciler) maybeHealCertificate(ctx context.Context, rd *decositesv1alpha1.DecoRedirect) (bool, error) {
	log := logf.FromContext(ctx)

	cert := &cmv1.Certificate{}
	if err := r.Get(ctx, types.NamespacedName{Name: resourceName(rd.Spec.From), Namespace: rd.Namespace}, cert); err != nil {
		return false, client.IgnoreNotFound(err)
	}

	// Skip if already being deleted or not in the Failed backoff state.
	if cert.DeletionTimestamp != nil || !isCertFailed(cert) {
		return false, nil
	}

	dnsReady := r.DNSReadyFunc
	if dnsReady == nil {
		dnsReady = r.isDNSReady
	}
	if !dnsReady(ctx, rd.Spec.From) {
		log.Info("certificate in Failed backoff but DNS not ready yet", "domain", rd.Spec.From)
		return false, nil
	}

	log.Info("certificate in Failed backoff and DNS is ready — deleting to trigger retry", "domain", rd.Spec.From)
	if err := r.Delete(ctx, cert); err != nil {
		return false, client.IgnoreNotFound(err)
	}
	return true, nil
}

// isCertFailed reports whether the Certificate is stuck in cert-manager's exponential
// backoff after a failed issuance attempt (Issuing=False, Reason=Failed).
func isCertFailed(cert *cmv1.Certificate) bool {
	for _, c := range cert.Status.Conditions {
		if c.Type == cmv1.CertificateConditionIssuing {
			return c.Status == cmmeta.ConditionFalse && c.Reason == "Failed"
		}
	}
	return false
}

// isDNSReady checks that the domain is correctly pointing to the redirect infrastructure:
//  1. An HTTP request returns a redirect served by the nginx (X-Redirect-By: deco header).
//  2. No AAAA record falls within any BlockedIPv6CIDRs range, which would cause
//     Let's Encrypt's IPv6 validation to reach the wrong server and fail the challenge.
func (r *DecoRedirectReconciler) isDNSReady(ctx context.Context, domain string) bool {
	httpClient := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Timeout:       5 * time.Second,
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+domain+"/", nil)
	if err != nil {
		return false
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	if resp.Header.Get("X-Redirect-By") != "deco" {
		return false
	}

	if len(r.BlockedIPv6CIDRs) == 0 {
		return true
	}

	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, domain)
	if err != nil {
		return false
	}
	for _, a := range addrs {
		ip := a.IP
		if ip.To4() != nil {
			continue
		}
		for _, blocked := range r.BlockedIPv6CIDRs {
			if blocked.Contains(ip) {
				return false
			}
		}
	}
	return true
}

// resourceName returns a deterministic k8s-safe name for a domain, capped at 253 chars.
// "client.com" → "redirect-client-com"
func resourceName(domain string) string {
	return boundedName("redirect-", domain)
}

// tlsSecretName returns the TLS Secret name for a domain, capped at 253 chars.
func tlsSecretName(domain string) string {
	return boundedName("tls-", domain)
}

// boundedName builds "<prefix><sanitized-domain>", truncating to 253 chars by replacing the
// suffix with an 8-hex-char hash when the full name would exceed the Kubernetes limit.
func boundedName(prefix, domain string) string {
	full := prefix + sanitizeDomain(domain)
	if len(full) <= 253 {
		return full
	}
	h := fmt.Sprintf("%x", sha256.Sum256([]byte(domain)))[:8]
	// keep as many chars of the sanitized domain as fit, then append the hash
	max := 253 - len(prefix) - 1 - 8 // 1 for the dash separator
	return prefix + sanitizeDomain(domain)[:max] + "-" + h
}

// sanitizeDomain replaces dots and underscores with dashes.
func sanitizeDomain(domain string) string {
	return strings.NewReplacer(".", "-", "_", "-").Replace(domain)
}

func (r *DecoRedirectReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&decositesv1alpha1.DecoRedirect{}).
		Owns(&cmv1.Certificate{}).
		Owns(&networkingv1.Ingress{}).
		Complete(r)
}
