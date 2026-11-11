package databases

import (
	"context"
	"errors"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// fakeApps is an AppLinker in memory.
type fakeApps struct {
	apps     map[string]map[string]string // app -> used names
	linked   map[string]map[string]string // app -> values
	restarts []string
}

func (f *fakeApps) AppExists(app string) bool { _, ok := f.apps[app]; return ok }
func (f *fakeApps) UsedNames(app string) map[string]string {
	out := map[string]string{}
	for k, v := range f.apps[app] {
		out[k] = v
	}
	for k := range f.linked[app] {
		out[k] = "database"
	}
	return out
}
func (f *fakeApps) SetLinked(app, _ string, values map[string]string) error {
	f.linked[app] = values
	return nil
}
func (f *fakeApps) RemoveLinked(app, _ string) error { delete(f.linked, app); return nil }
func (f *fakeApps) Namespace(app string) string      { return "nida-app-" + app }
func (f *fakeApps) Restart(_ context.Context, app string) error {
	f.restarts = append(f.restarts, app)
	return nil
}

func TestLinkAndUnlink(t *testing.T) {
	r := newRig(t)
	apps := &fakeApps{apps: map[string]map[string]string{"shop": {}, "blog": {"PGHOST": "variable"}}, linked: map[string]map[string]string{}}
	r.c.SetApps(apps)
	r.create(t, "shop-db")
	ctx := context.Background()
	_, _ = r.kube.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "nida-app-shop"}}, metav1.CreateOptions{})

	d, err := r.c.Link(ctx, "shop-db", "shop", "")
	if err != nil {
		t.Fatal(err)
	}
	if l, ok := d.Link("shop"); !ok || l.Mode != ModeRW || l.Role != "l_shop" {
		t.Errorf("link %+v", d.Links)
	}
	v := apps.linked["shop"]
	pw := r.store.Secret("shop-db", "link/shop")
	if !strings.HasPrefix(v["DATABASE_URL"], "postgresql://l_shop:"+pw+"@pg.nida-db-shop-db.svc.cluster.local:5432/shop_db?sslmode=require") || v["PGPASSWORD"] != pw {
		t.Errorf("connection %v", v)
	}
	ns, _ := r.kube.CoreV1().Namespaces().Get(ctx, "nida-app-shop", metav1.GetOptions{})
	if ns.Labels["nidaos.io/db-link-shop-db"] != "true" {
		t.Errorf("namespace labels %v", ns.Labels)
	}
	if len(apps.restarts) != 1 {
		t.Errorf("restarts %v", apps.restarts)
	}
	if _, err := r.c.Link(ctx, "shop-db", "shop", ModeRO); !errors.Is(err, ErrLinked) {
		t.Errorf("second link: %v", err)
	}
	if _, err := r.c.Link(ctx, "shop-db", "blog", ModeRO); !errors.Is(err, ErrNameInUse) {
		t.Errorf("link over a variable: %v", err)
	}
	if _, err := r.c.Link(ctx, "shop-db", "nobody", ModeRO); !errors.Is(err, ErrAppNotFound) {
		t.Errorf("link of an unknown app: %v", err)
	}

	r.sql.rows = func(sql string) []string {
		if strings.Contains(sql, "count(*)") {
			return []string{"0"}
		}
		return []string{"1"}
	}
	d, err = r.c.Unlink(ctx, "shop-db", "shop")
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Links) != 0 || apps.linked["shop"] != nil || r.store.Secret("shop-db", "link/shop") != "" {
		t.Errorf("after unlink: %+v %v", d.Links, apps.linked)
	}
	ns, _ = r.kube.CoreV1().Namespaces().Get(ctx, "nida-app-shop", metav1.GetOptions{})
	if _, ok := ns.Labels["nidaos.io/db-link-shop-db"]; ok {
		t.Error("the label is still on the namespace")
	}
	if !strings.Contains(r.sql.all(), `DROP ROLE "l_shop"`) {
		t.Error("the role of the link is still there")
	}
}

func TestSyncRemovesTheLinkOfARemovedApp(t *testing.T) {
	r := newRig(t)
	apps := &fakeApps{apps: map[string]map[string]string{"shop": {}}, linked: map[string]map[string]string{}}
	r.c.SetApps(apps)
	r.create(t, "shop-db")
	ctx := context.Background()
	if _, err := r.c.Link(ctx, "shop-db", "shop", ModeRO); err != nil {
		t.Fatal(err)
	}
	delete(apps.apps, "shop")
	r.c.Sync(ctx)
	if d, _ := r.store.Get("shop-db"); len(d.Links) != 0 {
		t.Errorf("links %+v", d.Links)
	}
}

func TestRemoveDatabaseDisconnectsTheApps(t *testing.T) {
	r := newRig(t)
	apps := &fakeApps{apps: map[string]map[string]string{"shop": {}}, linked: map[string]map[string]string{}}
	r.c.SetApps(apps)
	r.create(t, "shop-db")
	ctx := context.Background()
	_, _ = r.c.Link(ctx, "shop-db", "shop", ModeRW)
	if err := r.c.RemoveDatabase(ctx, "shop-db"); err != nil {
		t.Fatal(err)
	}
	if apps.linked["shop"] != nil {
		t.Error("the app keeps the connection of a removed database")
	}
	if _, ok := r.store.Get("shop-db"); ok {
		t.Error("the database is still in the store")
	}
	if _, err := r.kube.CoreV1().Namespaces().Get(ctx, "nida-db-shop-db", metav1.GetOptions{}); err == nil {
		t.Error("the namespace is still there")
	}
}
