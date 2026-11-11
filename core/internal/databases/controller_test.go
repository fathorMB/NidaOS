package databases

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// fakePod records the commands in the pod and gives a fixed output.
type fakePod struct {
	mu     sync.Mutex
	cmds   [][]string
	stdins []string
	output string
	fail   string
}

func (f *fakePod) Exec(_ context.Context, _, _, _ string, cmd []string, stdin io.Reader, stdout io.Writer) error {
	var in string
	if stdin != nil {
		b, _ := io.ReadAll(stdin)
		in = string(b)
	}
	f.mu.Lock()
	f.cmds = append(f.cmds, cmd)
	f.stdins = append(f.stdins, in)
	f.mu.Unlock()
	if f.fail != "" && strings.Contains(strings.Join(cmd, " "), f.fail) {
		return errors.New("command failed: " + f.fail)
	}
	_, _ = io.WriteString(stdout, f.output)
	return nil
}

func testImages() map[int]string {
	return map[int]string{17: "docker.io/library/postgres:17.7-alpine", 18: "docker.io/library/postgres:18.1-alpine"}
}

type rig struct {
	store *Store
	kube  *fake.Clientset
	sql   *fakeSQL
	pod   *fakePod
	c     *Controller
}

func newRig(t *testing.T) *rig {
	t.Helper()
	s, err := OpenStore(filepath.Join(t.TempDir(), "databases.json"))
	if err != nil {
		t.Fatal(err)
	}
	r := &rig{store: s, kube: fake.NewClientset(), sql: &fakeSQL{}, pod: &fakePod{}}
	r.c = NewController(s, r.kube, r.pod, r.sql, Options{
		Images:       testImages(),
		TLS:          func() ([]byte, []byte, error) { return []byte("CERT"), []byte("KEY"), nil },
		ReadyTimeout: 2 * time.Second,
		PollInterval: 10 * time.Millisecond,
	})
	return r
}

// markReady sets the status of the StatefulSet as Kubernetes does when the
// server is ready.
func (r *rig) markReady(t *testing.T, db string) {
	t.Helper()
	sets := r.kube.AppsV1().StatefulSets(Namespace(db))
	for range 200 {
		s, err := sets.Get(context.Background(), WorkloadName, metav1.GetOptions{})
		if err == nil {
			s.Status.ObservedGeneration, s.Status.ReadyReplicas, s.Status.UpdatedReplicas = s.Generation, 1, 1
			if _, err := sets.UpdateStatus(context.Background(), s, metav1.UpdateOptions{}); err == nil {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Error("the StatefulSet did not appear")
}

func (r *rig) create(t *testing.T, name string) Database {
	t.Helper()
	d, err := r.store.Create(Database{Name: name, Version: 18, Image: testImages()[18], Tier: TierPerformance})
	if err != nil {
		t.Fatal(err)
	}
	go r.markReady(t, name)
	r.c.operate(context.Background(), d)
	got, _ := r.store.Get(name)
	if got.State != StateReady {
		t.Fatalf("state %s (%s), want ready", got.State, got.Reason)
	}
	return got
}

func TestCreateMakesTheServerAndTheRoles(t *testing.T) {
	r := newRig(t)
	d := r.create(t, "shop-db")
	ctx := context.Background()
	if d.SizeBytes != DefaultSizeBytes || d.MemoryBytes != DefaultMemoryBytes {
		t.Errorf("defaults %+v", d)
	}
	ns, err := r.kube.CoreV1().Namespaces().Get(ctx, "nida-db-shop-db", metav1.GetOptions{})
	if err != nil || ns.Labels["pod-security.kubernetes.io/enforce"] != "restricted" {
		t.Fatalf("namespace %v %v", ns, err)
	}
	tls, err := r.kube.CoreV1().Secrets("nida-db-shop-db").Get(ctx, SecretTLS, metav1.GetOptions{})
	if err != nil || string(tls.Data["tls.crt"]) != "CERT" {
		t.Errorf("TLS secret %v %v", tls, err)
	}
	if _, err := r.kube.NetworkingV1().NetworkPolicies("nida-db-shop-db").Get(ctx, NetworkPolicyName, metav1.GetOptions{}); err != nil {
		t.Errorf("network policy: %v", err)
	}
	pw := r.store.OwnerPassword("shop-db")
	if len(pw) != 32 || !strings.Contains(r.sql.all(), QuoteLiteral(pw)) {
		t.Errorf("the roles do not use the owner password %q", pw)
	}
}

func TestCreateFailsWhenTheServerDoesNotStart(t *testing.T) {
	r := newRig(t)
	r.c.opts.ReadyTimeout = 100 * time.Millisecond
	d, _ := r.store.Create(Database{Name: "slow", Version: 18, Image: testImages()[18], Tier: TierStandard})
	r.c.operate(context.Background(), d)
	got, _ := r.store.Get("slow")
	if got.State != StateFailed || !strings.Contains(got.Reason, "did not start") {
		t.Errorf("state %s %q", got.State, got.Reason)
	}
}

func TestSyncPutsBackADeletedObject(t *testing.T) {
	r := newRig(t)
	r.create(t, "shop-db")
	ctx := context.Background()
	_ = r.kube.NetworkingV1().NetworkPolicies("nida-db-shop-db").Delete(ctx, NetworkPolicyName, metav1.DeleteOptions{})
	r.c.Sync(ctx)
	if _, err := r.kube.NetworkingV1().NetworkPolicies("nida-db-shop-db").Get(ctx, NetworkPolicyName, metav1.GetOptions{}); err != nil {
		t.Error("the sync did not put the network policy back")
	}
}

func TestStoreRules(t *testing.T) {
	r := newRig(t)
	cases := []struct {
		d     Database
		field string
	}{
		{Database{Name: "Bad", Tier: TierStandard, Image: "x"}, "name"},
		{Database{Name: "ok", Tier: "fast", Image: "x"}, "tier"},
		{Database{Name: "ok", Tier: TierStandard}, "version"},
		{Database{Name: "ok", Tier: TierStandard, Image: "x", SizeBytes: 10}, "size_bytes"},
		{Database{Name: "ok", Tier: TierStandard, Image: "x", MemoryBytes: 10}, "memory_bytes"},
	}
	for _, c := range cases {
		_, err := r.store.Create(c.d)
		var fe *FieldError
		if !errors.As(err, &fe) || fe.Field != c.field {
			t.Errorf("%+v: %v, want a FieldError for %s", c.d, err, c.field)
		}
	}
	_, _ = r.store.Create(Database{Name: "a", Tier: TierStandard, Image: "x"})
	if _, err := r.store.Create(Database{Name: "a", Tier: TierStandard, Image: "x"}); !errors.Is(err, ErrExists) {
		t.Errorf("second create: %v", err)
	}
	s2, _ := OpenStore(r.store.path)
	if s2.OwnerPassword("a") == "" || s2.OwnerPassword("a") != r.store.OwnerPassword("a") {
		t.Error("the owner password is not in the file")
	}
	if err := r.store.Remove("a"); err != nil || r.store.OwnerPassword("a") != "" {
		t.Errorf("remove: %v", err)
	}
}
