package apps

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
)

// fakeSource is a Source in memory.
type fakeSource struct {
	mu      sync.Mutex
	apps    map[string]App
	secrets map[string]map[string]string
	results []string
}

func newFakeSource(apps ...App) *fakeSource {
	s := &fakeSource{apps: map[string]App{}, secrets: map[string]map[string]string{}}
	for _, a := range apps {
		s.apps[a.Name] = a
	}
	return s
}

func (s *fakeSource) Apps() []App {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []App
	for _, a := range s.apps {
		out = append(out, a.clone())
	}
	return out
}

func (s *fakeSource) Get(name string) (App, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.apps[name]
	return a.clone(), ok
}

func (s *fakeSource) Finish(name string, number int, state, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.apps[name]
	for i := range a.Versions {
		if a.Versions[i].Number == number {
			a.Versions[i].State, a.Versions[i].Reason = state, reason
		}
	}
	if state == StateServing {
		a.Active = number
	}
	s.apps[name] = a
	s.results = append(s.results, state+": "+reason)
	return nil
}

func (s *fakeSource) Secrets(name string) map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.secrets[name]
}

func testController(t *testing.T, src Source) (*Controller, *fake.Clientset) {
	t.Helper()
	kube := fake.NewClientset()
	scheme := runtime.NewScheme()
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{routeGVR: "IngressRouteList"})
	c := New(src, kube, dyn, Options{
		Domain:          "nidaos.local",
		ProgressTimeout: 3 * time.Second,
		FailureGrace:    200 * time.Millisecond,
		PollInterval:    20 * time.Millisecond,
	})
	return c, kube
}

func port(n int) *int { return &n }

func webApp() App {
	return App{
		Name:   "blog",
		Copies: 1,
		Config: Config{Port: port(8080), HealthCheck: &HealthCheck{Path: "healthz"}},
		Versions: []Version{{Number: 1, Image: "localhost:5000/blog:1", State: StateDeploying,
			Config: Config{Port: port(8080), HealthCheck: &HealthCheck{Path: "healthz"}, Variables: map[string]string{"MODE": "prod"}}}},
	}
}

// markReady sets the status of the Deployment as Kubernetes does when all
// the copies pass the health check.
func markReady(t *testing.T, kube *fake.Clientset, app string, version int) {
	t.Helper()
	deps := kube.AppsV1().Deployments(Namespace(app))
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		d, err := deps.Get(context.Background(), WorkloadName, metav1.GetOptions{})
		if err == nil && d.Spec.Template.Annotations[AnnotationVersion] == strconv.Itoa(version) {
			n := *d.Spec.Replicas
			d.Status = appsv1.DeploymentStatus{ObservedGeneration: d.Generation, Replicas: n, UpdatedReplicas: n, ReadyReplicas: n, AvailableReplicas: n}
			if _, err := deps.UpdateStatus(context.Background(), d, metav1.UpdateOptions{}); err == nil {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("the Deployment of version %d did not appear", version)
}

func TestDeployPassesTheHealthCheck(t *testing.T) {
	src := newFakeSource(webApp())
	src.secrets["blog"] = map[string]string{"TOKEN": "s3cret"}
	c, kube := testController(t, src)
	app, _ := src.Get("blog")
	v, _ := app.Deploying()

	go markReady(t, kube, "blog", 1)
	c.Deploy(context.Background(), app, v)

	got, _ := src.Get("blog")
	if got.Active != 1 || got.Versions[0].State != StateServing {
		t.Fatalf("active %d, state %q: want version 1 serving (%v)", got.Active, got.Versions[0].State, src.results)
	}
	ctx := context.Background()
	ns, err := kube.CoreV1().Namespaces().Get(ctx, "nida-app-blog", metav1.GetOptions{})
	if err != nil || ns.Labels[LabelManaged] != "true" || ns.Labels["pod-security.kubernetes.io/enforce"] != "baseline" {
		t.Fatalf("namespace: %v %v", ns, err)
	}
	d, _ := kube.AppsV1().Deployments("nida-app-blog").Get(ctx, WorkloadName, metav1.GetOptions{})
	ct := d.Spec.Template.Spec.Containers[0]
	if ct.Image != "localhost:5000/blog:1" || ct.ReadinessProbe.HTTPGet.Path != "/healthz" {
		t.Errorf("container: image %q, probe %+v", ct.Image, ct.ReadinessProbe)
	}
	if len(ct.EnvFrom) != 1 || ct.EnvFrom[0].SecretRef.Name != secretName {
		t.Errorf("the container does not get the secrets: %+v", ct.EnvFrom)
	}
	env := map[string]string{}
	for _, e := range ct.Env {
		env[e.Name] = e.Value
	}
	if env["MODE"] != "prod" || env[EnvApp] != "blog" || env[EnvPort] != "8080" || env[EnvVersion] != "1" {
		t.Errorf("env: %v", env)
	}
	s, err := kube.CoreV1().Secrets("nida-app-blog").Get(ctx, secretName, metav1.GetOptions{})
	if err != nil || string(s.Data["TOKEN"]) != "s3cret" {
		t.Errorf("secret: %v %v", s, err)
	}
	svc, err := kube.CoreV1().Services("nida-app-blog").Get(ctx, WorkloadName, metav1.GetOptions{})
	if err != nil || len(svc.Spec.Ports) != 2 || svc.Spec.Ports[1].Port != 8080 {
		t.Errorf("service: %+v %v", svc, err)
	}
	r, err := c.dyn.Resource(routeGVR).Namespace("nida-app-blog").Get(ctx, RouteName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("route: %v", err)
	}
	routes, _, _ := unstructured.NestedSlice(r.Object, "spec", "routes")
	if !strings.Contains(routes[0].(map[string]any)["match"].(string), "blog.nidaos.local") {
		t.Errorf("route: %v", r.Object)
	}
}

func TestFailedVersionKeepsTheVersionBefore(t *testing.T) {
	app := webApp()
	app.Active = 1
	app.Versions[0].State = StateServing
	app.Versions = append(app.Versions, Version{Number: 2, Image: "localhost:5000/blog:2", State: StateDeploying, Config: app.Config})
	src := newFakeSource(app)
	c, kube := testController(t, src)
	ctx := context.Background()

	// Version 1 is in operation.
	go markReady(t, kube, "blog", 1)
	if r := c.rollout(ctx, app, app.Versions[0]); r != "" {
		t.Fatalf("version 1: %s", r)
	}

	// A pod of version 2 stops again and again.
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-time.After(20 * time.Millisecond):
			}
			d, err := kube.AppsV1().Deployments("nida-app-blog").Get(ctx, WorkloadName, metav1.GetOptions{})
			if err != nil || d.Spec.Template.Annotations[AnnotationVersion] != "2" {
				continue
			}
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "app-v2", Namespace: "nida-app-blog",
					Labels: selectorLabels("blog"), Annotations: map[string]string{AnnotationVersion: "2"}},
				Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{
					Name:                 WorkloadName,
					RestartCount:         3,
					State:                corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}},
					LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 1, Reason: "Error"}},
				}}},
			}
			_, _ = kube.CoreV1().Pods("nida-app-blog").Create(ctx, pod, metav1.CreateOptions{})
			// Kubernetes puts the old template back: the old copies are ready.
			markReady(t, kube, "blog", 1)
			return
		}
	}()
	defer close(stop)

	c.Deploy(ctx, app, app.Versions[1])
	got, _ := src.Get("blog")
	v2, _ := got.Version(2)
	if got.Active != 1 || v2.State != StateFailed || !strings.Contains(v2.Reason, "exit code 1") {
		t.Fatalf("active %d, version 2 %q %q", got.Active, v2.State, v2.Reason)
	}
	d, _ := kube.AppsV1().Deployments("nida-app-blog").Get(ctx, WorkloadName, metav1.GetOptions{})
	if d.Spec.Template.Annotations[AnnotationVersion] != "1" {
		t.Errorf("the Deployment has version %s, want 1 again", d.Spec.Template.Annotations[AnnotationVersion])
	}
}

func TestHealthCheckTimeout(t *testing.T) {
	src := newFakeSource(webApp())
	c, _ := testController(t, src)
	c.cfg.ProgressTimeout = 200 * time.Millisecond
	app, _ := src.Get("blog")
	v, _ := app.Deploying()
	c.Deploy(context.Background(), app, v)
	got, _ := src.Get("blog")
	if got.Active != 0 || got.Versions[0].State != StateFailed || !strings.Contains(got.Versions[0].Reason, "health check") {
		t.Fatalf("got %+v", got.Versions[0])
	}
}

func TestRemoveDeletesOnlyTheNamespaceOfTheApp(t *testing.T) {
	src := newFakeSource(webApp())
	c, kube := testController(t, src)
	ctx := context.Background()
	other := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "nida-app-x"}}
	_, _ = kube.CoreV1().Namespaces().Create(ctx, other, metav1.CreateOptions{})
	if err := c.Remove(ctx, "x"); err == nil {
		t.Error("Remove deleted a namespace without the labels of the controller")
	}
	if err := c.ensureNamespace(ctx, "blog"); err != nil {
		t.Fatal(err)
	}
	if err := c.Remove(ctx, "blog"); err != nil {
		t.Fatal(err)
	}
	if _, err := kube.CoreV1().Namespaces().Get(ctx, "nida-app-blog", metav1.GetOptions{}); err == nil {
		t.Error("the namespace of the app is still there")
	}
}

func TestSyncAppliesTheCopies(t *testing.T) {
	app := webApp()
	src := newFakeSource(app)
	c, kube := testController(t, src)
	ctx := context.Background()
	if err := c.apply(ctx, app, app.Versions[0]); err != nil {
		t.Fatal(err)
	}
	src.mu.Lock()
	a := src.apps["blog"]
	a.Copies = 0
	src.apps["blog"] = a
	src.mu.Unlock()
	c.Sync(ctx)
	d, _ := kube.AppsV1().Deployments("nida-app-blog").Get(ctx, WorkloadName, metav1.GetOptions{})
	if *d.Spec.Replicas != 0 {
		t.Errorf("copies %d, want 0", *d.Spec.Replicas)
	}
	if st, ok := c.StatusOf("blog"); !ok || st.Copies != 0 {
		t.Errorf("status %+v %v", st, ok)
	}
}
