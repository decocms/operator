package controller

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// ensureServiceAccount creates or updates the named ServiceAccount in the given
// namespace, merging the provided annotations. It is idempotent.
func ensureServiceAccount(ctx context.Context, c client.Client, namespace, name string, annotations map[string]string) error {
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, c, sa, func() error {
		if len(annotations) > 0 {
			if sa.Annotations == nil {
				sa.Annotations = map[string]string{}
			}
			for k, v := range annotations {
				sa.Annotations[k] = v
			}
		}
		return nil
	})
	return err
}
