package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

const (
	ConditionCertificateReady = "CertificateReady"
)

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

// DecoRedirectStatus defines the observed state of DecoRedirect.
type DecoRedirectStatus struct {
	// Conditions represent the latest observations of the DecoRedirect's state.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=decoredict,singular=decoredirect
// +kubebuilder:printcolumn:name="From",type="string",JSONPath=".spec.from"
// +kubebuilder:printcolumn:name="To",type="string",JSONPath=".spec.to"
// +kubebuilder:printcolumn:name="CertReady",type="string",JSONPath=".status.conditions[?(@.type=='CertificateReady')].status"

// DecoRedirect manages a TLS-terminated apex redirect via cert-manager and nginx Ingress.
type DecoRedirect struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DecoRedirectSpec   `json:"spec,omitempty"`
	Status DecoRedirectStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// DecoRedirectList contains a list of DecoRedirect.
type DecoRedirectList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DecoRedirect `json:"items"`
}

func init() {
	SchemeBuilder.Register(&DecoRedirect{}, &DecoRedirectList{})
}
