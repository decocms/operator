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

var _ = Describe("RedirectDomain Controller", func() {
	Context("When reconciling a RedirectDomain", func() {
		const (
			rdName     = "test-redirect"
			rdNS       = "default"
			fromDomain = "client.com"
			toDomain   = "https://www.client.com"
		)

		ctx := context.Background()
		nn := types.NamespacedName{Name: rdName, Namespace: rdNS}

		newReconciler := func() *RedirectDomainReconciler {
			return &RedirectDomainReconciler{
				Client:        k8sClient,
				Scheme:        k8sClient.Scheme(),
				IngressClass:  "nginx",
				ClusterIssuer: "letsencrypt",
				DummyBackend:  "redirect-dummy-backend",
			}
		}

		BeforeEach(func() {
			rd := &decositesv1alpha1.RedirectDomain{}
			err := k8sClient.Get(ctx, nn, rd)
			if err != nil && errors.IsNotFound(err) {
				Expect(k8sClient.Create(ctx, &decositesv1alpha1.RedirectDomain{
					ObjectMeta: metav1.ObjectMeta{Name: rdName, Namespace: rdNS},
					Spec: decositesv1alpha1.RedirectDomainSpec{
						From: fromDomain,
						To:   toDomain,
					},
				})).To(Succeed())
			}
		})

		AfterEach(func() {
			rd := &decositesv1alpha1.RedirectDomain{}
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

		It("should create an Ingress with permanent-redirect annotation", func() {
			_, err := newReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())

			ing := &networkingv1.Ingress{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: "redirect-client-com", Namespace: rdNS,
			}, ing)).To(Succeed())
			Expect(*ing.Spec.IngressClassName).To(Equal("nginx"))
			Expect(ing.Annotations["nginx.ingress.kubernetes.io/permanent-redirect"]).To(Equal(toDomain))
			Expect(ing.Spec.TLS[0].Hosts).To(ContainElement(fromDomain))
			Expect(ing.Spec.TLS[0].SecretName).To(Equal("tls-client-com"))
			Expect(ing.Spec.Rules[0].Host).To(Equal(fromDomain))
			Expect(ing.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Name).To(Equal("redirect-dummy-backend"))
		})

		It("should set CertificateReady=False when cert is not yet issued", func() {
			_, err := newReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())

			rd := &decositesv1alpha1.RedirectDomain{}
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

		It("should reject a RedirectDomain whose 'to' is outside the 'from' domain", func() {
			err := k8sClient.Create(ctx, &decositesv1alpha1.RedirectDomain{
				ObjectMeta: metav1.ObjectMeta{Name: "invalid-redirect", Namespace: rdNS},
				Spec: decositesv1alpha1.RedirectDomainSpec{
					From: "client.com",
					To:   "https://www.other.com",
				},
			})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("redirect target must be within the same domain"))
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
	})
})
