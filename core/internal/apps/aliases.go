package apps

import (
	"context"
	"sort"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/util/retry"
)

// reservedAliases are names that have a meaning in the namespace of an
// app already.
var reservedAliases = map[string]bool{WorkloadName: true, "kubernetes": true}

// BuildAliases gives the internal names in the namespace of app: one
// Service of the type ExternalName for each other app with a port. From
// app, "http://api" goes to the Service of the app "api". The alias points
// to the Service, and the Service does not change at a deploy, a rollback
// or a restart. Thus the alias stays valid.
func BuildAliases(app string, all []App) []*corev1.Service {
	var out []*corev1.Service
	for _, o := range all {
		if o.Name == app || reservedAliases[o.Name] || o.Config.Port == nil {
			continue
		}
		l := objectLabels(app)
		l[LabelAlias] = "true"
		out = append(out, &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: o.Name, Namespace: Namespace(app), Labels: l},
			Spec: corev1.ServiceSpec{
				Type:         corev1.ServiceTypeExternalName,
				ExternalName: WorkloadName + "." + Namespace(o.Name) + ".svc.cluster.local",
			},
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ensureAliases makes, changes and deletes the internal names in the
// namespace of app. It changes only the Services with the alias label.
func (c *Controller) ensureAliases(ctx context.Context, app string, all []App) error {
	services := c.kube.CoreV1().Services(Namespace(app))
	want := map[string]*corev1.Service{}
	for _, s := range BuildAliases(app, all) {
		want[s.Name] = s
	}
	cur, err := services.List(ctx, metav1.ListOptions{LabelSelector: labels.Set{LabelAlias: "true"}.String()})
	if err != nil {
		return err
	}
	for _, s := range cur.Items {
		if _, ok := want[s.Name]; !ok {
			if err := services.Delete(ctx, s.Name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
				return err
			}
		}
	}
	for _, w := range want {
		err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			s, err := services.Get(ctx, w.Name, metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				_, err = services.Create(ctx, w.DeepCopy(), metav1.CreateOptions{})
				return err
			}
			if err != nil {
				return err
			}
			if s.Labels[LabelAlias] != "true" {
				return nil // not an object of the controller: do not change it
			}
			if s.Spec.Type == w.Spec.Type && s.Spec.ExternalName == w.Spec.ExternalName {
				return nil
			}
			s.Spec.Type, s.Spec.ExternalName, s.Spec.Ports, s.Spec.Selector = w.Spec.Type, w.Spec.ExternalName, nil, nil
			_, err = services.Update(ctx, s, metav1.UpdateOptions{})
			return err
		})
		if err != nil {
			return err
		}
	}
	return nil
}
