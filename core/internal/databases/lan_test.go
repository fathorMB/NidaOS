package databases

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestLANSwitch(t *testing.T) {
	r := newRig(t)
	r.create(t, "a")
	r.create(t, "b")
	ctx := context.Background()
	d, err := r.c.SetLAN(ctx, "a", true)
	if err != nil || !d.LAN || d.LANPort != LANPortFirst {
		t.Fatalf("a: %+v %v", d, err)
	}
	d, _ = r.c.SetLAN(ctx, "b", true)
	if d.LANPort != LANPortFirst+1 {
		t.Errorf("b port %d", d.LANPort)
	}
	svc, err := r.kube.CoreV1().Services("nida-db-a").Get(ctx, LANServiceName, metav1.GetOptions{})
	if err != nil || svc.Spec.Ports[0].Port != int32(LANPortFirst) {
		t.Fatalf("LAN service %v %v", svc, err)
	}
	if _, err := r.kube.NetworkingV1().NetworkPolicies("nida-db-a").Get(ctx, LANNetworkPolicyName, metav1.GetOptions{}); err != nil {
		t.Errorf("LAN policy: %v", err)
	}
	if d, err = r.c.SetLAN(ctx, "a", false); err != nil || d.LAN || d.LANPort != 0 {
		t.Fatalf("off: %+v %v", d, err)
	}
	if _, err := r.kube.CoreV1().Services("nida-db-a").Get(ctx, LANServiceName, metav1.GetOptions{}); err == nil {
		t.Error("the LAN service is still there")
	}
	// The free port goes to the next database.
	r.create(t, "c")
	if d, _ = r.c.SetLAN(ctx, "c", true); d.LANPort != LANPortFirst {
		t.Errorf("c port %d", d.LANPort)
	}
}
