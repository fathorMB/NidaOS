package apps

import (
	"bytes"
	"context"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/util/retry"
)

// TLSSecretName is the Secret with the certificate of the app addresses,
// in the namespace of Traefik.
const TLSSecretName = "nida-apps-tls"

var tlsStoreGVR = schema.GroupVersionResource{Group: "traefik.io", Version: "v1alpha1", Resource: "tlsstores"}

// BuildTLSStore gives the default TLS store of Traefik. Each route with
// "tls: {}" and no own certificate uses its default certificate.
func BuildTLSStore() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "traefik.io/v1alpha1",
		"kind":       "TLSStore",
		"metadata": map[string]any{
			"name":      "default",
			"namespace": IngressNamespace,
			"labels":    map[string]any{"app.kubernetes.io/managed-by": "nida-core"},
		},
		"spec": map[string]any{
			"defaultCertificate": map[string]any{"secretName": TLSSecretName},
		},
	}}
}

// EnsureTLS puts the current server certificate in the Secret of Traefik
// and makes the default TLS store. The certificate renews itself before
// its end, thus the controller calls EnsureTLS each minute.
func (c *Controller) EnsureTLS(ctx context.Context) error {
	certPEM, keyPEM, err := c.cfg.TLS()
	if err != nil {
		return err
	}
	want := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: TLSSecretName, Namespace: IngressNamespace,
			Labels: map[string]string{"app.kubernetes.io/managed-by": "nida-core"}},
		Type: corev1.SecretTypeTLS,
		Data: map[string][]byte{corev1.TLSCertKey: certPEM, corev1.TLSPrivateKeyKey: keyPEM},
	}
	secrets := c.kube.CoreV1().Secrets(IngressNamespace)
	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cur, err := secrets.Get(ctx, TLSSecretName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			_, err = secrets.Create(ctx, want.DeepCopy(), metav1.CreateOptions{})
			return err
		}
		if err != nil {
			return err
		}
		if bytes.Equal(cur.Data[corev1.TLSCertKey], certPEM) && bytes.Equal(cur.Data[corev1.TLSPrivateKeyKey], keyPEM) {
			return nil
		}
		cur.Data, cur.Type = want.Data, want.Type
		_, err = secrets.Update(ctx, cur, metav1.UpdateOptions{})
		return err
	})
	if err != nil || c.dyn == nil {
		return err
	}
	return upsert(ctx, c.dyn.Resource(tlsStoreGVR).Namespace(IngressNamespace), BuildTLSStore())
}
