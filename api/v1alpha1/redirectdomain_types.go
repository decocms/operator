package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

const (
	ConditionCertificateReady = "CertificateReady"
)

// RedirectDomainSpec defines the desired state of RedirectDomain.
// +kubebuilder:validation:XValidation:rule="self.to.contains('.'+self.from) || self.to.contains('//'+self.from)",message="redirect target must be within the same domain as 'from' (e.g. from: client.com → to: https://www.client.com)"
type RedirectDomainSpec struct {
	// From is the apex domain to redirect (e.g. "client.com").
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	From string `json:"from"`

	// To is the full target URL within the same domain (e.g. "https://www.client.com").
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	To string `json:"to"`
}

// RedirectDomainStatus defines the observed state of RedirectDomain.
type RedirectDomainStatus struct {
	// Conditions represent the latest observations of the RedirectDomain's state.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="From",type="string",JSONPath=".spec.from"
// +kubebuilder:printcolumn:name="To",type="string",JSONPath=".spec.to"
// +kubebuilder:printcolumn:name="CertReady",type="string",JSONPath=".status.conditions[?(@.type=='CertificateReady')].status"

// RedirectDomain manages a TLS-terminated apex redirect via cert-manager and nginx Ingress.
type RedirectDomain struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RedirectDomainSpec   `json:"spec,omitempty"`
	Status RedirectDomainStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// RedirectDomainList contains a list of RedirectDomain.
type RedirectDomainList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []RedirectDomain `json:"items"`
}

func init() {
	SchemeBuilder.Register(&RedirectDomain{}, &RedirectDomainList{})
}
