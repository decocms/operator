package controller

import (
	"context"
	"strings"
	"time"

	cmv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	cmmeta "github.com/cert-manager/cert-manager/pkg/apis/meta/v1"
	networkingv1 "k8s.io/api/networking/v1"
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

// RedirectDomainReconciler reconciles a RedirectDomain object.
type RedirectDomainReconciler struct {
	client.Client
	Scheme        *runtime.Scheme
	IngressClass  string // nginx ingress class name, e.g. "nginx"
	ClusterIssuer string // cert-manager ClusterIssuer name, e.g. "letsencrypt"
	DummyBackend  string // shared dummy Service name, e.g. "redirect-dummy-backend"
}

// +kubebuilder:rbac:groups=deco.sites,resources=redirectdomains,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=deco.sites,resources=redirectdomains/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=deco.sites,resources=redirectdomains/finalizers,verbs=update
// +kubebuilder:rbac:groups=cert-manager.io,resources=certificates,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses,verbs=get;list;watch;create;update;patch;delete

func (r *RedirectDomainReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	rd := &decositesv1alpha1.RedirectDomain{}
	if err := r.Get(ctx, req.NamespacedName, rd); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
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

func (r *RedirectDomainReconciler) reconcileCertificate(ctx context.Context, rd *decositesv1alpha1.RedirectDomain) error {
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
		cert.Spec = cmv1.CertificateSpec{
			SecretName: tlsSecretName(rd.Spec.From),
			DNSNames:   []string{rd.Spec.From},
			IssuerRef: cmmeta.ObjectReference{
				Name: r.ClusterIssuer,
				Kind: "ClusterIssuer",
			},
		}
		return nil
	})
	return err
}

func (r *RedirectDomainReconciler) reconcileIngress(ctx context.Context, rd *decositesv1alpha1.RedirectDomain) error {
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

	// nginx returns 301 directly via this annotation — no traffic reaches the dummy backend.
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, ingress, func() error {
		ingress.Annotations = map[string]string{
			"nginx.ingress.kubernetes.io/permanent-redirect": rd.Spec.To,
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
											Name: r.DummyBackend,
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

func (r *RedirectDomainReconciler) updateStatus(ctx context.Context, rd *decositesv1alpha1.RedirectDomain) (bool, error) {
	certReady := false
	cert := &cmv1.Certificate{}
	if err := r.Get(ctx, types.NamespacedName{Name: resourceName(rd.Spec.From), Namespace: rd.Namespace}, cert); err == nil {
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

// resourceName returns a deterministic k8s-safe name for a domain.
// "client.com" → "redirect-client-com"
func resourceName(domain string) string {
	return "redirect-" + sanitizeDomain(domain)
}

// tlsSecretName returns the TLS Secret name for a domain.
func tlsSecretName(domain string) string {
	return "tls-" + sanitizeDomain(domain)
}

// sanitizeDomain replaces dots and underscores with dashes.
func sanitizeDomain(domain string) string {
	return strings.NewReplacer(".", "-", "_", "-").Replace(domain)
}

func (r *RedirectDomainReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&decositesv1alpha1.RedirectDomain{}).
		Owns(&cmv1.Certificate{}).
		Owns(&networkingv1.Ingress{}).
		Complete(r)
}
