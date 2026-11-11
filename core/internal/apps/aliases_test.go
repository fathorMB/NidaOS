package apps

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestAliasesFollowTheApps(t *testing.T) {
	web := webApp()
	api := App{Name: "api", Copies: 1, Config: Config{Port: port(5000)}}
	worker := App{Name: "worker", Copies: 1} // no port: no internal name
	src := newFakeSource(web, api, worker)
	c, kube := testController(t, src)
	ctx := context.Background()
	if err := c.apply(ctx, web, web.Versions[0]); err != nil {
		t.Fatal(err)
	}
	svcs := kube.CoreV1().Services(Namespace("blog"))
	s, err := svcs.Get(ctx, "api", metav1.GetOptions{})
	if err != nil || s.Spec.Type != corev1.ServiceTypeExternalName || s.Spec.ExternalName != "app.nida-app-api.svc.cluster.local" {
		t.Fatalf("alias api: %+v %v", s, err)
	}
	if _, err := svcs.Get(ctx, "worker", metav1.GetOptions{}); err == nil {
		t.Error("an app without a port has an internal name")
	}
	if _, err := svcs.Get(ctx, "blog", metav1.GetOptions{}); err == nil {
		t.Error("an app has an internal name for itself")
	}

	// The owner removes the app api: the next sync removes its name.
	src.mu.Lock()
	delete(src.apps, "api")
	src.mu.Unlock()
	c.Sync(ctx)
	if _, err := svcs.Get(ctx, "api", metav1.GetOptions{}); err == nil {
		t.Error("the alias of a removed app is still there")
	}
	if _, err := svcs.Get(ctx, WorkloadName, metav1.GetOptions{}); err != nil {
		t.Error("the sync removed the Service of the app")
	}
}

func TestAliasDoesNotReplaceAnOtherService(t *testing.T) {
	src := newFakeSource(webApp(), App{Name: "api", Config: Config{Port: port(80)}})
	c, kube := testController(t, src)
	ctx := context.Background()
	_ = c.ensureNamespace(ctx, "blog")
	own := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: Namespace("blog")},
		Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{{Port: 1}}}}
	_, _ = kube.CoreV1().Services(Namespace("blog")).Create(ctx, own, metav1.CreateOptions{})
	if err := c.ensureAliases(ctx, "blog", src.Apps()); err != nil {
		t.Fatal(err)
	}
	s, _ := kube.CoreV1().Services(Namespace("blog")).Get(ctx, "api", metav1.GetOptions{})
	if s.Spec.Type == corev1.ServiceTypeExternalName {
		t.Error("the controller changed a Service that is not its alias")
	}
}
