package runners

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/fathorMB/NidaOS/core/internal/auth"
	"github.com/fathorMB/NidaOS/core/internal/github"
	"github.com/fathorMB/NidaOS/core/internal/github/githubtest"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenStore(filepath.Join(t.TempDir(), "runners.json"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestStoreConnections(t *testing.T) {
	s := newStore(t)
	if err := s.Connect(Connection{App: "shop", Owner: "me", Repo: "shop", Branch: "main"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Connect(Connection{App: "shop", Owner: "me", Repo: "other", Branch: "main"}); !errors.Is(err, ErrConnected) {
		t.Fatalf("second connection of the app: %v", err)
	}
	if err := s.Connect(Connection{App: "blog", Owner: "Me", Repo: "Shop", Branch: "main"}); !errors.Is(err, ErrRepoInUse) {
		t.Fatalf("repository of an other app: %v", err)
	}
	s2, err := OpenStore(s.path)
	if err != nil {
		t.Fatal(err)
	}
	if c, ok := s2.Connection("shop"); !ok || c.FullName() != "me/shop" {
		t.Fatalf("connection after open = %+v", c)
	}
	if runtime.GOOS != "windows" {
		st, _ := os.Stat(s.path)
		if st.Mode().Perm() != 0o600 {
			t.Fatalf("mode = %v", st.Mode())
		}
	}
	if err := s.Disconnect("shop"); err != nil {
		t.Fatal(err)
	}
	if err := s.Disconnect("shop"); !errors.Is(err, ErrNotConnected) {
		t.Fatal(err)
	}
}

func TestStoreKeepsTwentyFinalJobs(t *testing.T) {
	s := newStore(t)
	base := time.Now()
	for i := range 25 {
		j := Job{ID: int64(i + 1), App: "shop", State: JobSucceeded, Created: base.Add(time.Duration(i) * time.Minute)}
		if i == 0 {
			j.State = JobRunning // not final: it stays
		}
		if err := s.AddJob(j); err != nil {
			t.Fatal(err)
		}
		_ = s.WriteLog(j.ID, []byte("log"))
	}
	jobs := s.Jobs("shop")
	if len(jobs) != KeepJobs+1 {
		t.Fatalf("jobs = %d", len(jobs))
	}
	if jobs[0].ID != 25 || jobs[len(jobs)-1].ID != 1 {
		t.Fatalf("order: first %d, last %d", jobs[0].ID, jobs[len(jobs)-1].ID)
	}
	if _, err := os.Stat(s.LogPath(2)); !os.IsNotExist(err) {
		t.Fatalf("the log of a removed job stays: %v", err)
	}
}

func TestSetupKeyExpires(t *testing.T) {
	s := newStore(t)
	now := time.Now()
	s.now = func() time.Time { return now }
	st, err := s.StartSetup("abcdef0123456789")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CheckSetup(st.Key); err != nil {
		t.Fatal(err)
	}
	if err := s.CheckSetup("abcdef0123456780"); !errors.Is(err, ErrSetupExpired) {
		t.Fatalf("wrong key: %v", err)
	}
	now = now.Add(31 * time.Minute)
	if err := s.CheckSetup(st.Key); !errors.Is(err, ErrSetupExpired) {
		t.Fatalf("old key: %v", err)
	}
}

func TestTokens(t *testing.T) {
	tk := NewTokens()
	now := time.Now()
	tk.Now = func() time.Time { return now }
	a := tk.Issue("shop", "run-shop-1", time.Hour)
	b := tk.Issue("blog", "run-blog-1", time.Hour)
	if id, ok := tk.Check(a); !ok || id.App != "shop" {
		t.Fatalf("check = %+v %v", id, ok)
	}
	if _, ok := tk.Check("nidt_" + a[5:]); ok {
		t.Fatal("an other prefix passed")
	}
	tk.RevokePod("run-shop-1")
	if _, ok := tk.Check(a); ok {
		t.Fatal("a revoked token passed")
	}
	now = now.Add(2 * time.Hour)
	if _, ok := tk.Check(b); ok {
		t.Fatal("an old token passed")
	}
}

// jobAPI gives a job endpoint with a fake registry and a fake API.
func jobAPI(t *testing.T) (*JobAPI, string, *[]string) {
	t.Helper()
	var seen []string
	reg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, "registry "+r.Method+" "+r.URL.Path+" auth="+r.Header.Get("Authorization")+" host="+r.Host)
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(reg.Close)
	api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, _ := r.Context().Value(identityKey{}).(auth.Identity)
		b, _ := io.ReadAll(r.Body)
		seen = append(seen, "api "+r.Method+" "+r.URL.Path+" level="+id.Level+" body="+string(b))
		w.WriteHeader(http.StatusAccepted)
	})
	u, _ := url.Parse(reg.URL)
	bin := filepath.Join(t.TempDir(), "nida")
	_ = os.WriteFile(bin, []byte("#!nida"), 0o755)
	tk := NewTokens()
	j := &JobAPI{Tokens: tk, API: api, Registry: u, RegistryName: "localhost:5000", NidaBinary: bin,
		WithIdentity: func(h http.Handler, id auth.Identity) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				h.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), identityKey{}, id)))
			})
		}}
	return j, tk.Issue("shop", "run-shop-1", time.Hour), &seen
}

type identityKey struct{}

func call(h http.Handler, method, path, tok string, basic bool, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "http://10.42.0.1:8471"+path, strings.NewReader(body))
	if tok != "" {
		if basic {
			req.SetBasicAuth("job", tok)
		} else {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestJobAPIGivesOnlyTheAppOfTheToken(t *testing.T) {
	j, tok, seen := jobAPI(t)

	if r := call(j, "GET", "/v2/", "", false, ""); r.Code != 401 || !strings.Contains(r.Header().Get("WWW-Authenticate"), "Basic") {
		t.Fatalf("no token: %d %v", r.Code, r.Header())
	}
	if r := call(j, "GET", "/v2/", tok, true, ""); r.Code != 202 {
		t.Fatalf("/v2/: %d", r.Code)
	}
	if r := call(j, "POST", "/v2/shop/blobs/uploads/", tok, true, ""); r.Code != 202 {
		t.Fatalf("push of the app: %d", r.Code)
	}
	for _, p := range []string{"/v2/blog/blobs/uploads/", "/v2/_catalog", "/v2/shop/blobs/uploads/?mount=sha256:1&from=blog"} {
		if r := call(j, "POST", p, tok, true, ""); r.Code != 403 {
			t.Fatalf("%s: %d", p, r.Code)
		}
	}
	if r := call(j, "POST", "/api/v1/apps/shop/deploys", tok, false, `{"image":"localhost:5000/shop:abc"}`); r.Code != 202 {
		t.Fatalf("deploy: %d", r.Code)
	}
	for _, c := range []struct{ method, path, body string }{
		{"POST", "/api/v1/apps/blog/deploys", `{"image":"localhost:5000/blog:abc"}`},
		{"POST", "/api/v1/apps/shop/deploys", `{"image":"localhost:5000/blog:abc"}`},
		{"POST", "/api/v1/apps/shop/deploys", `{"image":"docker.io/evil/shop:1"}`},
		{"GET", "/api/v1/apps/blog", ""},
		{"GET", "/api/v1/apps", ""},
		{"POST", "/api/v1/apps/shop/rollback", `{"version":1}`},
		{"GET", "/api/v1/tokens", ""},
	} {
		if r := call(j, c.method, c.path, tok, false, c.body); r.Code != 403 {
			t.Fatalf("%s %s %s: %d", c.method, c.path, c.body, r.Code)
		}
	}
	if r := call(j, "GET", "/api/v1/apps/shop", tok, false, ""); r.Code != 202 {
		t.Fatalf("state of the app: %d", r.Code)
	}
	if r := call(j, "POST", "/api/v1/apps/shop/deploys", "nidj_0000", false, `{"image":"localhost:5000/shop:abc"}`); r.Code != 401 {
		t.Fatalf("bad token: %d", r.Code)
	}

	want := []string{
		"registry GET /v2/ auth= host=10.42.0.1:8471",
		"registry POST /v2/shop/blobs/uploads/ auth= host=10.42.0.1:8471",
		`api POST /api/v1/apps/shop/deploys level=admin body={"image":"localhost:5000/shop:abc"}`,
		"api GET /api/v1/apps/shop level=admin body=",
	}
	if strings.Join(*seen, "\n") != strings.Join(want, "\n") {
		t.Fatalf("seen:\n%s", strings.Join(*seen, "\n"))
	}
	if r := call(j, "GET", "/tools/nida", "", false, ""); r.Code != 200 || r.Body.String() != "#!nida" {
		t.Fatalf("tools: %d %q", r.Code, r.Body.String())
	}
}

func TestBuildPodIsolatesTheJob(t *testing.T) {
	p := BuildPod(PodSpec{Name: "run-shop-abc", App: "shop", JobID: 9, Secret: "run-shop-abc",
		JobURL: "http://10.42.0.1:8471", Registry: "10.42.0.1:8471",
		Images: Images{Runner: "ghcr.io/actions/actions-runner:2.330.0", BuildKit: DefaultBuildKit},
		Limits: Limits{CPUMillis: 2000, MemoryBytes: 4 << 30, DiskBytes: 10 << 30}, Deadline: 7200})
	if *p.Spec.AutomountServiceAccountToken || p.Spec.RestartPolicy != corev1.RestartPolicyNever || *p.Spec.ActiveDeadlineSeconds != 7200 {
		t.Fatal("pod spec")
	}
	r := p.Spec.Containers[0]
	if *r.SecurityContext.AllowPrivilegeEscalation || !*r.SecurityContext.RunAsNonRoot || r.SecurityContext.Privileged != nil {
		t.Fatal("the runner container is not restricted")
	}
	bk := p.Spec.InitContainers[1]
	if bk.Name != "buildkitd" || *bk.RestartPolicy != corev1.ContainerRestartPolicyAlways || *bk.SecurityContext.RunAsUser != 1000 || bk.SecurityContext.Privileged != nil {
		t.Fatal("the BuildKit sidecar")
	}
	cpu := r.Resources.Limits.Cpu().MilliValue() + bk.Resources.Limits.Cpu().MilliValue()
	mem := r.Resources.Limits.Memory().Value() + bk.Resources.Limits.Memory().Value()
	if cpu != 2000 || mem != 4<<30 {
		t.Fatalf("limits: cpu %d, memory %d", cpu, mem)
	}
	env := map[string]string{}
	for _, e := range r.Env {
		if e.ValueFrom != nil {
			env[e.Name] = "secret:" + e.ValueFrom.SecretKeyRef.Key
		} else {
			env[e.Name] = e.Value
		}
	}
	if env["NIDA_TOKEN"] != "secret:token" || env["ACTIONS_RUNNER_INPUT_JITCONFIG"] != "secret:jitconfig" || env["NIDA_APP"] != "shop" {
		t.Fatalf("env = %v", env)
	}
	np := BuildNetworkPolicy("10.42.0.1", 8471)
	if len(np.Spec.Ingress) != 0 || len(np.Spec.PolicyTypes) != 2 {
		t.Fatal("the network policy must stop all the connections that come in")
	}
	if ex := np.Spec.Egress[2].To[0].IPBlock.Except; !strings.Contains(strings.Join(ex, ","), "192.168.0.0/16") {
		t.Fatalf("the home network is open: %v", ex)
	}
}

type rig struct {
	gh    *githubtest.Server
	store *Store
	kube  *fake.Clientset
	ctl   *Controller
	svc   *Service
	now   time.Time
}

func newRig(t *testing.T) *rig {
	t.Helper()
	gh := githubtest.New()
	t.Cleanup(gh.Close)
	r := &rig{gh: gh, store: newStore(t), kube: fake.NewClientset(), now: time.Now()}
	if err := r.store.SetApp(gh.App); err != nil {
		t.Fatal(err)
	}
	client := github.New(gh.URL, gh.URL, r.store.App)
	r.ctl = NewController(r.store, client, r.kube, NewTokens(), Options{
		JobURL: "http://10.42.0.1:8471", JobHost: "10.42.0.1", JobPort: 8471, RegistryHost: "10.42.0.1:8471",
		Now: func() time.Time { return r.now },
	})
	r.svc = &Service{Store: r.store, GitHub: client, AppExists: func(a string) bool { return a == "shop" },
		Controller: func() *Controller { return r.ctl }}
	return r
}

func (r *rig) pods(t *testing.T) []corev1.Pod {
	t.Helper()
	l, err := r.kube.CoreV1().Pods(Namespace).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return l.Items
}

func (r *rig) queue(repo string, jobID int64, labels ...string) {
	d, unlock := r.gh.Lock(repo)
	defer unlock()
	d.Runs = append(d.Runs, github.Run{ID: jobID * 10, Status: "queued", HeadSHA: "abc123"})
	d.Jobs[jobID*10] = []github.Job{{ID: jobID, RunID: jobID * 10, Name: "deploy", Status: "queued", HeadSHA: "abc123", Labels: labels}}
}

func (r *rig) setJob(repo string, jobID int64, f func(*github.Job)) {
	d, unlock := r.gh.Lock(repo)
	defer unlock()
	f(&d.Jobs[jobID*10][0])
	if d.Jobs[jobID*10][0].Status != "queued" {
		for i := range d.Runs {
			if d.Runs[i].ID == jobID*10 {
				d.Runs[i].Status = "in_progress"
			}
		}
	}
}

func TestConnectNeedsAPrivateRepositoryAndOpensThePullRequest(t *testing.T) {
	r := newRig(t)
	r.gh.Repo("me/shop", true)
	r.gh.Repo("me/open", false)
	ctx := context.Background()

	if _, err := r.svc.Connect(ctx, ConnectRequest{App: "shop", Repository: "me/open"}); !errors.Is(err, ErrNotPrivate) {
		t.Fatalf("public repository: %v", err)
	}
	if _, err := r.svc.Connect(ctx, ConnectRequest{App: "nothing", Repository: "me/shop"}); !errors.Is(err, ErrNoAppHere) {
		t.Fatalf("no app: %v", err)
	}
	if _, err := r.svc.Connect(ctx, ConnectRequest{App: "shop", Repository: "me/missing"}); !errors.Is(err, github.ErrNotInstalled) {
		t.Fatalf("not installed: %v", err)
	}
	c, err := r.svc.Connect(ctx, ConnectRequest{App: "shop", Repository: "me/shop"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Branch != "main" || !strings.HasSuffix(c.PullRequest, "/me/shop/pull/1") {
		t.Fatalf("connection = %+v", c)
	}
	d, unlock := r.gh.Lock("me/shop")
	wf := d.Files[WorkflowBranch("shop")+":"+WorkflowPath("shop")]
	unlock()
	for _, want := range []string{"runs-on: [self-hosted, nidaos, nidaos-shop]", `branches: ["main"]`, "nida app deploy shop --build . --wait"} {
		if !strings.Contains(wf, want) {
			t.Fatalf("the workflow has no %q:\n%s", want, wf)
		}
	}
	if err := r.svc.Disconnect(ctx, "shop"); err != nil {
		t.Fatal(err)
	}
}

func TestAQueuedJobGetsOnePodAndEndsWithItsLog(t *testing.T) {
	r := newRig(t)
	r.gh.Repo("me/shop", true)
	ctx := context.Background()
	if _, err := r.svc.Connect(ctx, ConnectRequest{App: "shop", Repository: "me/shop", NoPullRequest: true}); err != nil {
		t.Fatal(err)
	}
	r.queue("me/shop", 7, "self-hosted", "nidaos", "nidaos-shop")
	r.queue("me/shop", 8, "ubuntu-latest") // not for NidaOS

	if err := r.ctl.ensureBase(ctx); err != nil {
		t.Fatal(err)
	}
	r.ctl.Sync(ctx)
	r.ctl.Sync(ctx)
	pods := r.pods(t)
	if len(pods) != 1 || pods[0].Labels[LabelJob] != "7" {
		t.Fatalf("pods = %d", len(pods))
	}
	runners := r.gh.Runners()
	if len(runners) != 1 || strings.Join(runners[0].Labels, ",") != "self-hosted,nidaos,nidaos-shop" || runners[0].Name != pods[0].Name {
		t.Fatalf("runners = %+v", runners)
	}
	if pods[0].Spec.Containers[0].Image != "ghcr.io/actions/actions-runner:2.330.0" {
		t.Fatalf("runner image = %s", pods[0].Spec.Containers[0].Image)
	}
	if _, err := r.kube.CoreV1().Secrets(Namespace).Get(ctx, pods[0].Name, metav1.GetOptions{}); err != nil {
		t.Fatalf("secret: %v", err)
	}
	if j, _ := r.store.Job(7); j.State != JobQueued || j.Pod != pods[0].Name {
		t.Fatalf("job = %+v", j)
	}

	started := r.now
	r.setJob("me/shop", 7, func(j *github.Job) { j.Status, j.RunnerName, j.StartedAt = "in_progress", pods[0].Name, &started })
	r.ctl.Sync(ctx)
	if j, _ := r.store.Job(7); j.State != JobRunning || j.Runner != pods[0].Name {
		t.Fatalf("job = %+v", j)
	}

	r.setJob("me/shop", 7, func(j *github.Job) { j.Status, j.Conclusion = "completed", "success" })
	d, unlock := r.gh.Lock("me/shop")
	d.Logs[7] = "Build and deploy on NidaOS\nThe version 1 is in operation.\n"
	unlock()
	r.ctl.Sync(ctx)
	j, _ := r.store.Job(7)
	if j.State != JobSucceeded || !j.HasLog || j.Finished == nil {
		t.Fatalf("job = %+v", j)
	}
	if b, _ := os.ReadFile(r.store.LogPath(7)); !bytes.Contains(b, []byte("in operation")) {
		t.Fatalf("log = %q", b)
	}
	if n := len(r.pods(t)); n != 0 {
		t.Fatalf("the pod stays: %d", n)
	}
	if n := len(r.gh.Runners()); n != 0 {
		t.Fatalf("the runner stays: %d", n)
	}
}

func TestAFailedPodStartsAgainThreeTimes(t *testing.T) {
	r := newRig(t)
	r.gh.Repo("me/shop", true)
	ctx := context.Background()
	if _, err := r.svc.Connect(ctx, ConnectRequest{App: "shop", Repository: "me/shop", NoPullRequest: true}); err != nil {
		t.Fatal(err)
	}
	r.queue("me/shop", 7, "self-hosted", "nidaos", "nidaos-shop")
	r.ctl.Sync(ctx)
	for i := range MaxPodFailures {
		pods := r.pods(t)
		if len(pods) != 1 {
			t.Fatalf("round %d: pods = %d", i, len(pods))
		}
		p := pods[0]
		p.Status.Phase = corev1.PodFailed
		p.Status.Reason = "Evicted"
		if _, err := r.kube.CoreV1().Pods(Namespace).UpdateStatus(ctx, &p, metav1.UpdateOptions{}); err != nil {
			t.Fatal(err)
		}
		r.ctl.Sync(ctx)
	}
	j, _ := r.store.Job(7)
	if j.State != JobFailed || !strings.Contains(j.Message, "3 times") {
		t.Fatalf("job = %+v", j)
	}
	if n := len(r.pods(t)); n != 0 {
		t.Fatalf("pods = %d", n)
	}
}

func TestAPublicRepositoryGetsNoPod(t *testing.T) {
	r := newRig(t)
	r.gh.Repo("me/shop", true)
	ctx := context.Background()
	if _, err := r.svc.Connect(ctx, ConnectRequest{App: "shop", Repository: "me/shop", NoPullRequest: true}); err != nil {
		t.Fatal(err)
	}
	d, unlock := r.gh.Lock("me/shop")
	d.Private = false
	unlock()
	r.queue("me/shop", 7, "self-hosted", "nidaos", "nidaos-shop")
	r.ctl.Sync(ctx)
	if n := len(r.pods(t)); n != 0 {
		t.Fatalf("pods = %d", n)
	}
	if c, _ := r.store.Connection("shop"); !strings.Contains(c.Problem, "D27") {
		t.Fatalf("problem = %q", c.Problem)
	}
}

func TestSetupMakesTheApp(t *testing.T) {
	r := newRig(t)
	_ = r.store.RemoveApp()
	st, err := r.svc.StartSetup()
	if err != nil {
		t.Fatal(err)
	}
	var page bytes.Buffer
	if err := r.svc.SetupPage(&page, st.Key, "http://nidaos.local:8470", ""); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"/settings/apps/new?state=" + st.Key, "http://nidaos.local:8470/api/v1/github/callback", "administration"} {
		if !strings.Contains(page.String(), want) && !strings.Contains(page.String(), strings.ReplaceAll(want, "/", "\\/")) {
			t.Fatalf("the page has no %q:\n%s", want, page.String())
		}
	}
	if err := r.svc.SetupPage(&page, "bad", "http://x", ""); !errors.Is(err, ErrSetupExpired) {
		t.Fatalf("bad key: %v", err)
	}
	r.gh.Code("c0de")
	if _, err := r.svc.Callback(context.Background(), "c0de", "bad"); !errors.Is(err, ErrSetupExpired) {
		t.Fatalf("callback with a bad key: %v", err)
	}
	install, err := r.svc.Callback(context.Background(), "c0de", st.Key)
	if err != nil || !strings.HasSuffix(install, "/apps/nidaos-test/installations/new") {
		t.Fatalf("install = %q, err = %v", install, err)
	}
	if v := r.svc.AppView(); !v.Configured || v.ID != 4242 {
		t.Fatalf("view = %+v", v)
	}
	if err := r.store.CheckSetup(st.Key); err == nil {
		t.Fatal("the setup key stays valid after the callback")
	}
}

func TestImportExaminesTheKey(t *testing.T) {
	r := newRig(t)
	_ = r.store.RemoveApp()
	if _, err := r.svc.Import(context.Background(), 4242, "not a key"); err == nil {
		t.Fatal("a bad key passed")
	}
	v, err := r.svc.Import(context.Background(), 4242, r.gh.App.PEM)
	if err != nil || v.Slug != "nidaos-test" {
		t.Fatalf("view = %+v, err = %v", v, err)
	}
}
