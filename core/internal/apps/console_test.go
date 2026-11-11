package apps

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestEnsureConsole(t *testing.T) {
	c, kube := testController(t, &fakeSource{})
	c.cfg.ConsoleUpstream = "10.42.0.1:8470"
	ctx := context.Background()
	for i := 0; i < 2; i++ { // the second call updates the objects
		if err := c.EnsureConsole(ctx); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	svc, err := kube.CoreV1().Services(IngressNamespace).Get(ctx, ConsoleName, metav1.GetOptions{})
	if err != nil || len(svc.Spec.Selector) != 0 || svc.Spec.Ports[0].Port != 8470 {
		t.Fatalf("service: %v %+v", err, svc)
	}
	es, err := kube.DiscoveryV1().EndpointSlices(IngressNamespace).Get(ctx, ConsoleName, metav1.GetOptions{})
	if err != nil || es.Endpoints[0].Addresses[0] != "10.42.0.1" || es.Labels["kubernetes.io/service-name"] != ConsoleName {
		t.Fatalf("endpoints: %v %+v", err, es)
	}
	route, err := c.dyn.Resource(routeGVR).Namespace(IngressNamespace).Get(ctx, ConsoleName, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	routes := route.Object["spec"].(map[string]any)["routes"].([]any)
	if m := routes[0].(map[string]any)["match"]; m != "Host(`nidaos.local`)" {
		t.Errorf("match = %v", m)
	}
	if _, err := c.dyn.Resource(routeGVR).Namespace(IngressNamespace).Get(ctx, ConsoleName+"-http", metav1.GetOptions{}); err != nil {
		t.Errorf("http route: %v", err)
	}
	if _, err := c.dyn.Resource(middlewareGVR).Namespace(IngressNamespace).Get(ctx, ConsoleRedirect, metav1.GetOptions{}); err != nil {
		t.Errorf("middleware: %v", err)
	}
}

func TestSplitUpstream(t *testing.T) {
	if _, _, err := SplitUpstream("nidaos.local:8470"); err == nil {
		t.Error("a name: no error")
	}
	if _, _, err := SplitUpstream("10.42.0.1:0"); err == nil {
		t.Error("port 0: no error")
	}
	if a, p, err := SplitUpstream("10.42.0.1:8470"); err != nil || a != "10.42.0.1" || p != 8470 {
		t.Errorf("good address: %s %d %v", a, p, err)
	}
}
