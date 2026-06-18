package controller

import (
	"context"

	cmv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	decositesv1alpha1 "github.com/deco-sites/decofile-operator/api/v1alpha1"
)

var _ = Describe("DecoRedirect Controller", func() {
	Context("When reconciling a DecoRedirect", func() {
		const (
			rdName     = "test-redirect"
			rdNS       = "default"
			fromDomain = "client.com"
			toDomain   = "https://www.client.com"
		)

		ctx := context.Background()
		nn := types.NamespacedName{Name: rdName, Namespace: rdNS}

		newReconciler := func() *DecoRedirectReconciler {
			return &DecoRedirectReconciler{
				Client:        k8sClient,
				Scheme:        k8sClient.Scheme(),
				IngressClass:  "nginx",
				ClusterIssuer: "letsencrypt",
			}
		}

		BeforeEach(func() {
			rd := &decositesv1alpha1.DecoRedirect{}
			err := k8sClient.Get(ctx, nn, rd)
			if err != nil && errors.IsNotFound(err) {
				Expect(k8sClient.Create(ctx, &decositesv1alpha1.DecoRedirect{
					ObjectMeta: metav1.ObjectMeta{Name: rdName, Namespace: rdNS},
					Spec: decositesv1alpha1.DecoRedirectSpec{
						From: fromDomain,
						To:   toDomain,
					},
				})).To(Succeed())
			}
		})

		AfterEach(func() {
			rd := &decositesv1alpha1.DecoRedirect{}
			Expect(k8sClient.Get(ctx, nn, rd)).To(Succeed())
			Expect(k8sClient.Delete(ctx, rd)).To(Succeed())
		})

		It("should create a Certificate for the domain", func() {
			_, err := newReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())

			cert := &cmv1.Certificate{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: "redirect-client-com", Namespace: rdNS,
			}, cert)).To(Succeed())
			Expect(cert.Spec.DNSNames).To(ContainElement(fromDomain))
			Expect(cert.Spec.IssuerRef.Name).To(Equal("letsencrypt"))
			Expect(cert.Spec.IssuerRef.Kind).To(Equal("ClusterIssuer"))
			Expect(cert.Spec.SecretName).To(Equal("tls-client-com"))
		})

		It("should create an Ingress with server-snippet redirect preserving path", func() {
			_, err := newReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())

			ing := &networkingv1.Ingress{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: "redirect-client-com", Namespace: rdNS,
			}, ing)).To(Succeed())
			Expect(*ing.Spec.IngressClassName).To(Equal("nginx"))
			Expect(ing.Annotations["nginx.ingress.kubernetes.io/server-snippet"]).To(Equal("return 307 " + toDomain + "$request_uri;"))
			Expect(ing.Spec.TLS[0].Hosts).To(ContainElement(fromDomain))
			Expect(ing.Spec.TLS[0].SecretName).To(Equal("tls-client-com"))
			Expect(ing.Spec.Rules[0].Host).To(Equal(fromDomain))
			Expect(ing.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Name).To(Equal("redirect-dummy-backend"))
		})

		It("should set CertificateReady=False when cert is not yet issued", func() {
			_, err := newReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())

			rd := &decositesv1alpha1.DecoRedirect{}
			Expect(k8sClient.Get(ctx, nn, rd)).To(Succeed())

			found := false
			for _, c := range rd.Status.Conditions {
				if c.Type == decositesv1alpha1.ConditionCertificateReady {
					Expect(c.Status).To(Equal(metav1.ConditionFalse))
					Expect(c.Reason).To(Equal("Provisioning"))
					found = true
				}
			}
			Expect(found).To(BeTrue(), "CertificateReady condition should be present")
		})

		It("should reject a DecoRedirect whose 'to' is outside the 'from' domain", func() {
			err := k8sClient.Create(ctx, &decositesv1alpha1.DecoRedirect{
				ObjectMeta: metav1.ObjectMeta{Name: "invalid-redirect", Namespace: rdNS},
				Spec: decositesv1alpha1.DecoRedirectSpec{
					From: "client.com",
					To:   "https://www.other.com",
				},
			})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("redirect target must be within the same domain"))
		})

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

		It("should not create duplicate Certificate on repeated reconcile", func() {
			_, err := newReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())

			_, err = newReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())

			certList := &cmv1.CertificateList{}
			Expect(k8sClient.List(ctx, certList, client.InNamespace(rdNS))).To(Succeed())
			count := 0
			for _, c := range certList.Items {
				if c.Name == "redirect-client-com" {
					count++
				}
			}
			Expect(count).To(Equal(1))
		})

		It("should use return 307 in server-snippet by default", func() {
			_, err := newReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())

			ing := &networkingv1.Ingress{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: "redirect-client-com", Namespace: rdNS,
			}, ing)).To(Succeed())
			Expect(ing.Annotations["nginx.ingress.kubernetes.io/server-snippet"]).To(ContainSubstring("return 307 "))
		})

		It("should use return 301 in server-snippet when redirectCode is 301", func() {
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
			Expect(ing.Annotations["nginx.ingress.kubernetes.io/server-snippet"]).To(ContainSubstring("return 301 "))
		})
	})

	Context("Auto-healing: maybeHealCertificate", func() {
		const healNS = "default"
		ctx := context.Background()

		newReconciler := func(dnsReady bool) *DecoRedirectReconciler {
			return &DecoRedirectReconciler{
				Client:        k8sClient,
				Scheme:        k8sClient.Scheme(),
				IngressClass:  "nginx",
				ClusterIssuer: "letsencrypt",
				DNSReadyFunc:  func(_ context.Context, _ string) bool { return dnsReady },
			}
		}

		// Each test uses a unique name to avoid state sharing between tests.
		setup := func(suffix string) (nn, certNN types.NamespacedName, cleanup func()) {
			name := "heal-" + suffix
			domain := name + ".com"
			nn = types.NamespacedName{Name: name + "-com", Namespace: healNS}
			certNN = types.NamespacedName{Name: "redirect-" + name + "-com", Namespace: healNS}

			rd := &decositesv1alpha1.DecoRedirect{
				ObjectMeta: metav1.ObjectMeta{Name: name + "-com", Namespace: healNS},
				Spec: decositesv1alpha1.DecoRedirectSpec{
					From: domain,
					To:   "https://www." + domain,
				},
			}
			Expect(k8sClient.Create(ctx, rd)).To(Succeed())

			cleanup = func() {
				r := &decositesv1alpha1.DecoRedirect{}
				if err := k8sClient.Get(ctx, nn, r); err == nil {
					_ = k8sClient.Delete(ctx, r)
				}
				c := &cmv1.Certificate{}
				if err := k8sClient.Get(ctx, certNN, c); err == nil {
					_ = k8sClient.Delete(ctx, c)
				}
			}
			return nn, certNN, cleanup
		}

		patchCertFailed := func(certNN types.NamespacedName) {
			cert := &cmv1.Certificate{}
			Expect(k8sClient.Get(ctx, certNN, cert)).To(Succeed())
			patch := cert.DeepCopy()
			patch.Status.Conditions = []cmv1.CertificateCondition{
				{Type: cmv1.CertificateConditionReady, Status: "False", Reason: "DoesNotExist", Message: "secret not found", LastTransitionTime: &[]metav1.Time{metav1.Now()}[0]},
				{Type: cmv1.CertificateConditionIssuing, Status: "False", Reason: "Failed", Message: "cert request failed", LastTransitionTime: &[]metav1.Time{metav1.Now()}[0]},
			}
			Expect(k8sClient.Status().Patch(ctx, patch, client.MergeFrom(cert))).To(Succeed())
		}

		It("should delete the Certificate when it is in Failed backoff and DNS is ready", func() {
			nn, certNN, cleanup := setup("delete")
			DeferCleanup(cleanup)

			_, err := newReconciler(true).Reconcile(ctx, reconcile.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())

			patchCertFailed(certNN)

			rd := &decositesv1alpha1.DecoRedirect{}
			Expect(k8sClient.Get(ctx, nn, rd)).To(Succeed())

			healed, err := newReconciler(true).maybeHealCertificate(ctx, rd)
			Expect(err).NotTo(HaveOccurred())
			Expect(healed).To(BeTrue())

			cert := &cmv1.Certificate{}
			Expect(k8sClient.Get(ctx, certNN, cert)).To(MatchError(ContainSubstring("not found")))
		})

		It("should NOT delete the Certificate when DNS is not ready", func() {
			nn, certNN, cleanup := setup("dns-wrong")
			DeferCleanup(cleanup)

			_, err := newReconciler(false).Reconcile(ctx, reconcile.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())

			patchCertFailed(certNN)

			rd := &decositesv1alpha1.DecoRedirect{}
			Expect(k8sClient.Get(ctx, nn, rd)).To(Succeed())

			healed, err := newReconciler(false).maybeHealCertificate(ctx, rd)
			Expect(err).NotTo(HaveOccurred())
			Expect(healed).To(BeFalse())

			cert := &cmv1.Certificate{}
			Expect(k8sClient.Get(ctx, certNN, cert)).To(Succeed())
		})

		It("should NOT delete the Certificate when it is Issuing (actively trying)", func() {
			nn, certNN, cleanup := setup("issuing")
			DeferCleanup(cleanup)

			_, err := newReconciler(true).Reconcile(ctx, reconcile.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())

			cert := &cmv1.Certificate{}
			Expect(k8sClient.Get(ctx, certNN, cert)).To(Succeed())
			patch := cert.DeepCopy()
			patch.Status.Conditions = []cmv1.CertificateCondition{
				{Type: cmv1.CertificateConditionIssuing, Status: "True", Reason: "Issuing", LastTransitionTime: &[]metav1.Time{metav1.Now()}[0]},
			}
			Expect(k8sClient.Status().Patch(ctx, patch, client.MergeFrom(cert))).To(Succeed())

			rd := &decositesv1alpha1.DecoRedirect{}
			Expect(k8sClient.Get(ctx, nn, rd)).To(Succeed())

			healed, err := newReconciler(true).maybeHealCertificate(ctx, rd)
			Expect(err).NotTo(HaveOccurred())
			Expect(healed).To(BeFalse())

			Expect(k8sClient.Get(ctx, certNN, cert)).To(Succeed())
		})

		It("should NOT delete the Certificate when it is Ready", func() {
			nn, certNN, cleanup := setup("ready")
			DeferCleanup(cleanup)

			_, err := newReconciler(true).Reconcile(ctx, reconcile.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())

			cert := &cmv1.Certificate{}
			Expect(k8sClient.Get(ctx, certNN, cert)).To(Succeed())
			patch := cert.DeepCopy()
			patch.Status.Conditions = []cmv1.CertificateCondition{
				{Type: cmv1.CertificateConditionReady, Status: "True", Reason: "Ready", LastTransitionTime: &[]metav1.Time{metav1.Now()}[0]},
			}
			Expect(k8sClient.Status().Patch(ctx, patch, client.MergeFrom(cert))).To(Succeed())

			rd := &decositesv1alpha1.DecoRedirect{}
			Expect(k8sClient.Get(ctx, nn, rd)).To(Succeed())

			healed, err := newReconciler(true).maybeHealCertificate(ctx, rd)
			Expect(err).NotTo(HaveOccurred())
			Expect(healed).To(BeFalse())

			Expect(k8sClient.Get(ctx, certNN, cert)).To(Succeed())
		})

		It("should do nothing when the Certificate does not exist yet", func() {
			nn, _, cleanup := setup("no-cert")
			DeferCleanup(cleanup)

			rd := &decositesv1alpha1.DecoRedirect{}
			Expect(k8sClient.Get(ctx, nn, rd)).To(Succeed())

			healed, err := newReconciler(true).maybeHealCertificate(ctx, rd)
			Expect(err).NotTo(HaveOccurred())
			Expect(healed).To(BeFalse())
		})
	})
})
