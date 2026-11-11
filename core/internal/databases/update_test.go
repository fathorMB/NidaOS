package databases

import (
	"context"
	"errors"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestMinorUpdate(t *testing.T) {
	r := newRig(t)
	r.c.opts.BackupDir = t.TempDir()
	r.pod.output = "PGDMP"
	d, _ := r.store.Create(Database{Name: "shop-db", Version: 18, Image: "docker.io/library/postgres:18.0-alpine", Tier: TierStandard})
	go r.markReady(t, "shop-db")
	r.c.operate(context.Background(), d)

	if _, err := r.c.StartUpdate("shop-db"); err != nil {
		t.Fatal(err)
	}
	d, _ = r.store.Get("shop-db")
	if d.State != StateUpdating || d.UpdateTo != testImages()[18] {
		t.Fatalf("%+v", d)
	}
	r.c.operate(context.Background(), d)
	got, _ := r.store.Get("shop-db")
	if got.State != StateReady || got.Image != testImages()[18] || got.UpdateTo != "" {
		t.Fatalf("after the update: %+v", got)
	}
	sts, _ := r.kube.AppsV1().StatefulSets("nida-db-shop-db").Get(context.Background(), WorkloadName, metav1.GetOptions{})
	if sts.Spec.Template.Spec.Containers[0].Image != testImages()[18] {
		t.Errorf("image %s", sts.Spec.Template.Spec.Containers[0].Image)
	}
	list, _ := r.c.Backups("shop-db")
	if len(list) != 1 || list[0].Kind != KindUpdate {
		t.Errorf("backups %+v", list)
	}
	if _, err := r.c.StartUpdate("shop-db"); !errors.Is(err, ErrNoUpdate) {
		t.Errorf("second update: %v", err)
	}
}

func TestUpdateStopsWhenTheBackupFails(t *testing.T) {
	r := newRig(t)
	r.c.opts.BackupDir = t.TempDir()
	d, _ := r.store.Create(Database{Name: "shop-db", Version: 18, Image: "docker.io/library/postgres:18.0-alpine", Tier: TierStandard})
	go r.markReady(t, "shop-db")
	r.c.operate(context.Background(), d)
	r.pod.fail = "pg_dump"
	d, _ = r.c.StartUpdate("shop-db")
	r.c.operate(context.Background(), d)
	got, _ := r.store.Get("shop-db")
	if got.State != StateReady || got.Image != "docker.io/library/postgres:18.0-alpine" || !strings.Contains(got.Reason, "did not start") {
		t.Errorf("after a failed backup: %+v", got)
	}
}
