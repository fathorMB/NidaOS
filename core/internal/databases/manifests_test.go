package databases

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func testDB() Database {
	return Database{Name: "shop-db", Version: 18, Image: "docker.io/library/postgres:18.1-alpine", Tier: TierPerformance,
		SizeBytes: 1 << 30, CPUMillis: 100, MemoryBytes: 256 << 20}
}

func TestStatefulSetRunsAsUser70(t *testing.T) {
	sts := BuildStatefulSet(testDB())
	pod := sts.Spec.Template.Spec
	c := pod.Containers[0]
	if *pod.SecurityContext.RunAsUser != 70 || !*pod.SecurityContext.RunAsNonRoot || !*c.SecurityContext.ReadOnlyRootFilesystem {
		t.Errorf("security context %+v %+v", pod.SecurityContext, c.SecurityContext)
	}
	if c.Image != "docker.io/library/postgres:18.1-alpine" || *sts.Spec.VolumeClaimTemplates[0].Spec.StorageClassName != "nida-performance" {
		t.Errorf("image %s, class %s", c.Image, *sts.Spec.VolumeClaimTemplates[0].Spec.StorageClassName)
	}
	if !strings.Contains(strings.Join(c.Args, " "), "ssl=on") {
		t.Errorf("args %v", c.Args)
	}
	if sts.Annotations[annotationSpecHash] == "" {
		t.Error("no spec hash")
	}
	other := testDB()
	other.Image = "docker.io/library/postgres:18.2-alpine"
	if BuildStatefulSet(other).Annotations[annotationSpecHash] == sts.Annotations[annotationSpecHash] {
		t.Error("a new image does not change the spec hash")
	}
}

func TestHBAAcceptsOnlyTLS(t *testing.T) {
	for _, want := range []string{"hostnossl all   all                0.0.0.0/0   reject", "local     all   nida_admin   trust", "hostssl   all   nida_admin   0.0.0.0/0   reject"} {
		if !strings.Contains(HBA, want) {
			t.Errorf("pg_hba.conf has no %q", want)
		}
	}
	// The first line for the network that matches all the users is the
	// TLS line with a password.
	for _, l := range strings.Split(HBA, "\n") {
		f := strings.Fields(l)
		if len(f) > 2 && f[0] == "host" {
			t.Errorf("pg_hba.conf accepts plain text: %q", l)
		}
	}
}

func TestNetworkPolicies(t *testing.T) {
	np := BuildNetworkPolicy("shop-db")
	sel := np.Spec.Ingress[0].From[0].NamespaceSelector.MatchLabels
	if sel["nidaos.io/db-link-shop-db"] != "true" || len(np.Spec.PolicyTypes) != 2 || len(np.Spec.Egress) != 0 {
		t.Errorf("isolation policy %+v", np.Spec)
	}
	lan := BuildLANNetworkPolicy("shop-db")
	if lan.Spec.Ingress[0].From[0].IPBlock.Except[0] != PodCIDR {
		t.Errorf("LAN policy %+v", lan.Spec)
	}
	if s := BuildLANService("shop-db", 15433); s.Spec.Ports[0].Port != 15433 || s.Spec.Type != "LoadBalancer" {
		t.Errorf("LAN service %+v", s.Spec)
	}
}

// fakeSQL records the SQL and gives the rows of the first matching rule.
type fakeSQL struct {
	mu    sync.Mutex
	execs []string
	rows  func(sql string) []string
	fail  string
}

func (f *fakeSQL) Exec(_ context.Context, _, database, sql string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.execs = append(f.execs, database+": "+sql)
	if f.fail != "" && strings.Contains(sql, f.fail) {
		return errors.New("failed: " + f.fail)
	}
	return nil
}

func (f *fakeSQL) Query(_ context.Context, _, database, sql string) ([]string, error) {
	f.mu.Lock()
	f.execs = append(f.execs, database+": "+sql)
	f.mu.Unlock()
	if f.rows != nil {
		return f.rows(sql), nil
	}
	return nil, nil
}

func (f *fakeSQL) all() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.execs, "\n")
}

func TestSetupRoles(t *testing.T) {
	r := &fakeSQL{}
	if err := SetupRoles(context.Background(), r, "shop-db", "abc123"); err != nil {
		t.Fatal(err)
	}
	sql := r.all()
	for _, want := range []string{`CREATE ROLE "shop_db_owner" LOGIN NOSUPERUSER`, `PASSWORD 'abc123'`, `CREATE DATABASE "shop_db" OWNER "shop_db_owner"`,
		`REVOKE ALL ON DATABASE "shop_db" FROM PUBLIC`, `shop_db: ` + "\n" + `ALTER SCHEMA public OWNER TO "shop_db_owner"`} {
		if !strings.Contains(sql, want) {
			t.Errorf("no %q in:\n%s", want, sql)
		}
	}
}

func TestLinkRoles(t *testing.T) {
	r := &fakeSQL{}
	ctx := context.Background()
	if err := CreateLinkRole(ctx, r, "shop-db", "shop-api", ModeRW, "pw"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.all(), `GRANT "shop_db_owner" TO "l_shop_api"`) || !strings.Contains(r.all(), `SET role = 'shop_db_owner'`) {
		t.Errorf("rw link:\n%s", r.all())
	}
	if err := CreateLinkRole(ctx, r, "shop-db", "x", "admin", "pw"); err == nil {
		t.Error("an unknown mode was accepted")
	}

	// The drop closes the sessions first, then drops the role.
	sessions := 2
	r = &fakeSQL{rows: func(sql string) []string {
		switch {
		case strings.Contains(sql, "FROM pg_roles"):
			return []string{"1"}
		case strings.Contains(sql, "count(*)"):
			sessions--
			return []string{strings.Repeat("1", 0) + string(rune('0'+max(sessions, 0)))}
		}
		return nil
	}}
	if err := DropLinkRole(ctx, r, "shop-db", "shop-api", time.Second); err != nil {
		t.Fatal(err)
	}
	all := r.all()
	if i, j, k := strings.Index(all, "NOLOGIN"), strings.Index(all, "pg_terminate_backend"), strings.Index(all, "DROP ROLE"); !(i >= 0 && i < j && j < k) {
		t.Errorf("wrong order:\n%s", all)
	}

	// Sessions that stay after the timeout keep the role.
	r = &fakeSQL{rows: func(sql string) []string {
		if strings.Contains(sql, "count(*)") {
			return []string{"1"}
		}
		return []string{"1"}
	}}
	if err := DropLinkRole(ctx, r, "shop-db", "shop-api", 300*time.Millisecond); !errors.Is(err, ErrSessionsRemain) {
		t.Errorf("err %v, want ErrSessionsRemain", err)
	}
	if strings.Contains(r.all(), "DROP ROLE") {
		t.Error("the role was dropped with open sessions")
	}
}

func TestReadStats(t *testing.T) {
	r := &fakeSQL{rows: func(string) []string { return []string{"8192000 3 4521"} }}
	s, err := ReadStats(context.Background(), r, "shop-db")
	if err != nil || s.SizeBytes != 8192000 || s.Connections != 3 || s.Transactions != 4521 {
		t.Errorf("stats %+v %v", s, err)
	}
}

func TestRedact(t *testing.T) {
	p := &Psql{Redact: func() []string { return []string{"s3cret"} }}
	if got := p.scrub("password s3cret is wrong"); strings.Contains(got, "s3cret") {
		t.Errorf("scrub: %q", got)
	}
}
