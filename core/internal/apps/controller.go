package apps

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"
)

// routeGVR is the resource of the Traefik routes.
var routeGVR = schema.GroupVersionResource{Group: "traefik.io", Version: "v1alpha1", Resource: "ingressroutes"}

// Source gives the apps to the controller and keeps the result of each
// deploy. *Store is the real source.
type Source interface {
	Apps() []App
	Get(name string) (App, bool)
	// Finish writes the result of a deploy: StateServing or StateFailed.
	Finish(name string, number int, state, reason string) error
	// Secrets gives the secret values of an app.
	Secrets(name string) map[string]string
	// FinishMove writes the result of a move. An empty reason is a
	// success.
	FinishMove(name, folder, reason string) error
}

// Options is the configuration of the controller.
type Options struct {
	// Domain gives the address "<app>.<Domain>" of each app (decision
	// D19). Empty: the apps have no route.
	Domain string
	// ProgressTimeout is the maximum time for a version to pass the health
	// check. The default is 120 seconds.
	ProgressTimeout time.Duration
	// FailureGrace is the time that a pod error (an image that does not
	// download, a container that stops) must stay before the deploy fails.
	// A short error is not enough. The default is 10 seconds.
	FailureGrace time.Duration
	// PollInterval is the time between two reads of a rollout. The default
	// is 1 second.
	PollInterval time.Duration
	// LoopInterval is the time between two searches for new deploys. The
	// default is 2 seconds.
	LoopInterval time.Duration
	// SyncEvery is the time between two reads of the state of the copies.
	// The default is 5 seconds.
	SyncEvery time.Duration
	// MoveTimeout is the maximum time of the copy of a folder. The default
	// is 6 hours.
	MoveTimeout time.Duration
	// TLS gives the server certificate (with its chain) and its key for
	// the addresses of the apps (decision D20). Nil: Traefik keeps its own
	// default certificate.
	TLS    func() (certPEM, keyPEM []byte, err error)
	Logger *slog.Logger
}

func (c *Options) defaults() {
	if c.ProgressTimeout <= 0 {
		c.ProgressTimeout = 120 * time.Second
	}
	if c.FailureGrace <= 0 {
		c.FailureGrace = 10 * time.Second
	}
	if c.PollInterval <= 0 {
		c.PollInterval = time.Second
	}
	if c.LoopInterval <= 0 {
		c.LoopInterval = 2 * time.Second
	}
	if c.SyncEvery <= 0 {
		c.SyncEvery = 5 * time.Second
	}
	if c.MoveTimeout <= 0 {
		c.MoveTimeout = 6 * time.Hour
	}
	if c.Logger == nil {
		c.Logger = slog.New(slog.DiscardHandler)
	}
}

// Status is the observed state of the copies of an app.
type Status struct {
	// Copies is the number of copies that the app must have.
	Copies int `json:"copies"`
	// Ready is the number of copies that pass the health check.
	Ready int `json:"ready"`
	// Version is the version of the ready copies. 0: unknown.
	Version int `json:"version,omitempty"`
	// Observed is the time of the last read.
	Observed time.Time `json:"observed"`
}

// Controller keeps the apps of the source in operation on k3s.
type Controller struct {
	src  Source
	kube kubernetes.Interface
	// dyn makes the Traefik routes. Nil: no routes.
	dyn dynamic.Interface
	cfg Options

	kick    chan struct{}
	mu      sync.Mutex
	running map[string]bool
	status  map[string]Status
}

// New makes a controller.
func New(src Source, kube kubernetes.Interface, dyn dynamic.Interface, cfg Options) *Controller {
	cfg.defaults()
	return &Controller{src: src, kube: kube, dyn: dyn, cfg: cfg, kick: make(chan struct{}, 1),
		running: map[string]bool{}, status: map[string]Status{}}
}

// Kick tells the loop that there is a new deploy or a change.
func (c *Controller) Kick() {
	select {
	case c.kick <- struct{}{}:
	default:
	}
}

// Run starts the deploys and reads the state until ctx stops. A deploy
// that was in progress when the service stopped starts again: each step is
// idempotent.
func (c *Controller) Run(ctx context.Context) {
	tick := time.NewTicker(c.cfg.LoopInterval)
	defer tick.Stop()
	var wg sync.WaitGroup
	defer wg.Wait()
	var lastSync, lastTLS time.Time
	for {
		c.startPending(ctx, &wg)
		if now := time.Now(); now.Sub(lastSync) >= c.cfg.SyncEvery {
			lastSync = now
			c.Sync(ctx)
		}
		if now := time.Now(); c.cfg.TLS != nil && now.Sub(lastTLS) >= time.Minute {
			if err := c.EnsureTLS(ctx); err != nil {
				c.cfg.Logger.Warn("the controller cannot give the certificate to Traefik", "err", err)
			} else {
				lastTLS = now
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-c.kick:
		case <-tick.C:
		}
	}
}

func (c *Controller) startPending(ctx context.Context, wg *sync.WaitGroup) {
	for _, app := range c.src.Apps() {
		v, deploying := app.Deploying()
		f, moving := app.Moving()
		if !deploying && !moving {
			continue
		}
		c.mu.Lock()
		busy := c.running[app.Name]
		if !busy {
			c.running[app.Name] = true
		}
		c.mu.Unlock()
		if busy {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				c.mu.Lock()
				delete(c.running, app.Name)
				c.mu.Unlock()
				c.Kick()
			}()
			if moving {
				c.Move(ctx, app, f)
			} else {
				c.Deploy(ctx, app, v)
			}
		}()
	}
}

// Busy tells if a deploy of the app is in progress.
func (c *Controller) Busy(app string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.running[app]
}

// Deploy puts the version in operation and writes the result. It waits
// until the version passes the health check, or fails. When the version
// fails, the version before stays in operation.
func (c *Controller) Deploy(ctx context.Context, app App, v Version) {
	log := c.cfg.Logger.With("app", app.Name, "version", v.Number)
	log.Info("the deploy starts", "image", v.Image)
	reason := c.rollout(ctx, app, v)
	if ctx.Err() != nil {
		return // the service stops: the deploy starts again at the next start
	}
	if reason == "" {
		if err := c.src.Finish(app.Name, v.Number, StateServing, ""); err != nil {
			log.Warn("the controller cannot write the result of the deploy", "err", err)
			return
		}
		log.Info("the version is in operation")
		c.Sync(ctx)
		return
	}
	log.Warn("the deploy failed", "reason", reason)
	if app.Active != 0 && app.Active != v.Number {
		if prev, ok := app.Version(app.Active); ok {
			if err := c.restore(ctx, app, prev); err != nil {
				log.Warn("the controller cannot start the version before again", "err", err)
				reason += " (the version before did not start again: " + err.Error() + ")"
			}
		}
	}
	if err := c.src.Finish(app.Name, v.Number, StateFailed, reason); err != nil {
		log.Warn("the controller cannot write the result of the deploy", "err", err)
	}
	c.Sync(ctx)
}

// restore applies the version before again. Kubernetes finds the same pod
// template and uses the old copies again.
func (c *Controller) restore(ctx context.Context, app App, prev Version) error {
	if err := c.apply(ctx, app, prev); err != nil {
		return err
	}
	if r := c.waitReady(ctx, app, prev); r != "" {
		return errors.New(r)
	}
	return nil
}

// rollout applies the version and waits for the health check. It gives ""
// when the version is in operation, or the reason of the failure.
func (c *Controller) rollout(ctx context.Context, app App, v Version) string {
	if err := c.apply(ctx, app, v); err != nil {
		return "Kubernetes did not accept the version: " + err.Error()
	}
	if app.Copies == 0 {
		return "" // the app is stopped: there is nothing to examine
	}
	return c.waitReady(ctx, app, v)
}

// apply makes or updates all the objects of the version.
func (c *Controller) apply(ctx context.Context, app App, v Version) error {
	ns := Namespace(app.Name)
	if err := c.ensureNamespace(ctx, app.Name); err != nil {
		return fmt.Errorf("namespace: %w", err)
	}
	if err := c.ensureNetworkPolicy(ctx, app.Name); err != nil {
		return fmt.Errorf("network policy: %w", err)
	}
	secrets := c.src.Secrets(app.Name)
	if err := c.ensureSecret(ctx, app.Name, secrets); err != nil {
		return fmt.Errorf("secrets: %w", err)
	}
	if err := c.ensureClaims(ctx, app); err != nil {
		return fmt.Errorf("folders: %w", err)
	}
	if err := c.ensureService(ctx, app.Name, v.Config); err != nil {
		return fmt.Errorf("service: %w", err)
	}
	if err := c.ensureAliases(ctx, app.Name, c.src.Apps()); err != nil {
		return fmt.Errorf("internal names: %w", err)
	}
	if err := c.ensureRoute(ctx, app.Name, v.Config); err != nil {
		// Without Traefik (or before Traefik is ready) the route is not
		// there, but the version can start. The next deploy or sync tries
		// again.
		c.cfg.Logger.Warn("the controller cannot apply the route", "app", app.Name, "err", err)
	}
	deps := c.kube.AppsV1().Deployments(ns)
	opts := BuildOptions{
		Copies:          int32(app.Copies),
		ProgressSeconds: int32(c.cfg.ProgressTimeout / time.Second),
		Secrets:         len(secrets) > 0,
		Folders:         app.Folders,
	}
	// Get, build and update at each attempt: the deployment controller of
	// Kubernetes also changes the object, and an update with an old
	// resource version gets a conflict.
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cur, err := deps.Get(ctx, WorkloadName, metav1.GetOptions{})
		switch {
		case apierrors.IsNotFound(err):
			_, err = deps.Create(ctx, BuildDeployment(app.Name, v, opts), metav1.CreateOptions{})
			return err
		case err != nil:
			return err
		}
		o := opts
		o.RestartedAt = cur.Spec.Template.Annotations[AnnotationRestartedAt]
		want := BuildDeployment(app.Name, v, o)
		want.ResourceVersion = cur.ResourceVersion
		_, err = deps.Update(ctx, want, metav1.UpdateOptions{})
		return err
	})
}

func (c *Controller) ensureNamespace(ctx context.Context, app string) error {
	want := BuildNamespace(app)
	nss := c.kube.CoreV1().Namespaces()
	cur, err := nss.Get(ctx, want.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = nss.Create(ctx, want, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	if cur.Labels[LabelManaged] != "true" || cur.Labels[LabelApp] != app {
		return fmt.Errorf("the namespace %s exists, but it is not of this app", want.Name)
	}
	if cur.DeletionTimestamp != nil {
		return fmt.Errorf("the namespace %s of a removed app is not deleted yet: try again in some seconds", want.Name)
	}
	return nil
}

func (c *Controller) ensureNetworkPolicy(ctx context.Context, app string) error {
	want := BuildNetworkPolicy(app)
	nps := c.kube.NetworkingV1().NetworkPolicies(want.Namespace)
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cur, err := nps.Get(ctx, want.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			_, err = nps.Create(ctx, want.DeepCopy(), metav1.CreateOptions{})
			return err
		}
		if err != nil {
			return err
		}
		cur.Spec = want.Spec
		_, err = nps.Update(ctx, cur, metav1.UpdateOptions{})
		return err
	})
}

func (c *Controller) ensureSecret(ctx context.Context, app string, values map[string]string) error {
	secrets := c.kube.CoreV1().Secrets(Namespace(app))
	if len(values) == 0 {
		err := secrets.Delete(ctx, secretName, metav1.DeleteOptions{})
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	want := BuildSecret(app, values)
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cur, err := secrets.Get(ctx, secretName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			_, err = secrets.Create(ctx, want.DeepCopy(), metav1.CreateOptions{})
			return err
		}
		if err != nil {
			return err
		}
		cur.Data = want.Data
		_, err = secrets.Update(ctx, cur, metav1.UpdateOptions{})
		return err
	})
}

// ensureClaims makes the volume claim of each folder. The controller does
// not change a claim after it makes it.
func (c *Controller) ensureClaims(ctx context.Context, app App) error {
	claims := c.kube.CoreV1().PersistentVolumeClaims(Namespace(app.Name))
	for _, f := range app.Folders {
		_, err := claims.Get(ctx, ClaimName(f.Name, f.Tier), metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			_, err = claims.Create(ctx, BuildClaim(app.Name, f), metav1.CreateOptions{})
			if apierrors.IsAlreadyExists(err) {
				err = nil
			}
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (c *Controller) ensureService(ctx context.Context, app string, cfg Config) error {
	want := BuildService(app, cfg)
	services := c.kube.CoreV1().Services(Namespace(app))
	if want == nil {
		err := services.Delete(ctx, WorkloadName, metav1.DeleteOptions{})
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cur, err := services.Get(ctx, WorkloadName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			_, err = services.Create(ctx, want.DeepCopy(), metav1.CreateOptions{})
			return err
		}
		if err != nil {
			return err
		}
		cur.Spec.Selector, cur.Spec.Ports, cur.Labels = want.Spec.Selector, want.Spec.Ports, want.Labels
		_, err = services.Update(ctx, cur, metav1.UpdateOptions{})
		return err
	})
}

// Host gives the address of an app, or "" when the controller has no
// domain.
func (c *Controller) Host(app string) string {
	if c.cfg.Domain == "" {
		return ""
	}
	return app + "." + c.cfg.Domain
}

func (c *Controller) ensureRoute(ctx context.Context, app string, cfg Config) error {
	if c.dyn == nil {
		return nil
	}
	routes := c.dyn.Resource(routeGVR).Namespace(Namespace(app))
	if cfg.Port == nil || c.cfg.Domain == "" {
		err := routes.Delete(ctx, RouteName, metav1.DeleteOptions{})
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	return upsert(ctx, routes, BuildRoute(app, c.Host(app)))
}

// upsert makes or replaces an object of the dynamic client.
func upsert(ctx context.Context, ri dynamic.ResourceInterface, want *unstructured.Unstructured) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cur, err := ri.Get(ctx, want.GetName(), metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			_, err = ri.Create(ctx, want.DeepCopy(), metav1.CreateOptions{})
			return err
		}
		if err != nil {
			return err
		}
		next := want.DeepCopy()
		next.SetResourceVersion(cur.GetResourceVersion())
		_, err = ri.Update(ctx, next, metav1.UpdateOptions{})
		return err
	})
}

// waitReady waits until all the copies of the version pass the health
// check. It gives "" or the reason of the failure.
func (c *Controller) waitReady(ctx context.Context, app App, v Version) string {
	ns := Namespace(app.Name)
	deadline := time.Now().Add(c.cfg.ProgressTimeout)
	var failSince time.Time
	var lastFailure string
	for {
		d, err := c.kube.AppsV1().Deployments(ns).Get(ctx, WorkloadName, metav1.GetOptions{})
		if ctx.Err() != nil {
			return ""
		}
		if err == nil && d.Status.ObservedGeneration >= d.Generation {
			if rolloutComplete(d.Spec.Replicas, d.Status.Replicas, d.Status.UpdatedReplicas, d.Status.ReadyReplicas, d.Status.AvailableReplicas) {
				return ""
			}
			if f := c.podFailure(ctx, ns, v.Number); f != "" {
				if lastFailure != f {
					lastFailure, failSince = f, time.Now()
				}
				if time.Since(failSince) >= c.cfg.FailureGrace {
					return f
				}
			} else {
				lastFailure = ""
			}
		}
		if time.Now().After(deadline) {
			if lastFailure != "" {
				return lastFailure
			}
			msg := fmt.Sprintf("the version did not pass the health check in %d seconds", int(c.cfg.ProgressTimeout/time.Second))
			if s := c.podSummary(ctx, ns, v.Number); s != "" {
				msg += ": " + s
			}
			return msg
		}
		select {
		case <-ctx.Done():
			return ""
		case <-time.After(c.cfg.PollInterval):
		}
	}
}

func rolloutComplete(replicas *int32, total, updated, ready, available int32) bool {
	want := int32(1)
	if replicas != nil {
		want = *replicas
	}
	return updated == want && total == want && ready == want && available == want
}

// fatalWaiting are the reasons of a container that does not start again
// by itself.
var fatalWaiting = map[string]bool{
	"ImagePullBackOff": true, "ErrImagePull": true, "InvalidImageName": true, "CreateContainerConfigError": true,
	"CreateContainerError": true, "RunContainerError": true, "CrashLoopBackOff": true,
}

func (c *Controller) versionPods(ctx context.Context, ns string, version int) []corev1.Pod {
	pods, err := c.kube.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{
		LabelSelector: labels.Set{"app.kubernetes.io/name": WorkloadName}.String()})
	if err != nil {
		return nil
	}
	var out []corev1.Pod
	for _, p := range pods.Items {
		if p.Annotations[AnnotationVersion] == strconv.Itoa(version) && p.DeletionTimestamp == nil {
			out = append(out, p)
		}
	}
	return out
}

// podFailure gives the reason when a pod of the version has an error that
// does not go away by itself.
func (c *Controller) podFailure(ctx context.Context, ns string, version int) string {
	for _, p := range c.versionPods(ctx, ns, version) {
		if r := containerFailure(p); r != "" {
			// A container that starts again because the health check fails
			// looks like a crash. The real cause is in the pod events.
			if h := c.probeFailure(ctx, ns, p.Name); h != "" && !strings.HasPrefix(r, "ImagePull") && !strings.HasPrefix(r, "ErrImage") {
				return h
			}
			return r
		}
	}
	return ""
}

// probeFailure gives the message of the last failed liveness check of the
// pod.
func (c *Controller) probeFailure(ctx context.Context, ns, pod string) string {
	evs, err := c.kube.CoreV1().Events(ns).List(ctx, metav1.ListOptions{FieldSelector: "involvedObject.name=" + pod + ",reason=Unhealthy"})
	if err != nil {
		return ""
	}
	var last *corev1.Event
	for i := range evs.Items {
		e := &evs.Items[i]
		if strings.Contains(e.Message, "Liveness probe failed") && (last == nil || e.LastTimestamp.After(last.LastTimestamp.Time)) {
			last = e
		}
	}
	if last == nil {
		return ""
	}
	return trim("the health check failed: " + last.Message)
}

func containerFailure(p corev1.Pod) string {
	for _, cs := range p.Status.ContainerStatuses {
		if w := cs.State.Waiting; w != nil && fatalWaiting[w.Reason] {
			if t := cs.LastTerminationState.Terminated; t != nil && (w.Reason == "CrashLoopBackOff" || cs.RestartCount >= 1) {
				return fmt.Sprintf("the container stops again and again (exit code %d, %s)", t.ExitCode, firstNonEmpty(t.Reason, "stopped"))
			}
			return trim(w.Reason + ": " + w.Message)
		}
		if t := cs.LastTerminationState.Terminated; t != nil && cs.RestartCount >= 2 {
			return fmt.Sprintf("the container stops again and again (exit code %d, %s)", t.ExitCode, firstNonEmpty(t.Reason, "stopped"))
		}
	}
	return ""
}

func (c *Controller) podSummary(ctx context.Context, ns string, version int) string {
	for _, p := range c.versionPods(ctx, ns, version) {
		if p.Status.Phase == corev1.PodPending && len(p.Status.ContainerStatuses) == 0 {
			for _, cond := range p.Status.Conditions {
				if cond.Type == corev1.PodScheduled && cond.Status == corev1.ConditionFalse {
					return trim("Kubernetes cannot put the pod on the computer: " + cond.Message)
				}
			}
			continue
		}
		for _, cs := range p.Status.ContainerStatuses {
			if !cs.Ready {
				return "the copy does not answer the health check"
			}
		}
	}
	return ""
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}

// trim makes a reason short enough for a list.
func trim(s string) string {
	const max = 300
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}

// Sync reads the state of the copies of each app. For an app without a
// deploy in progress, it also applies the number of copies.
func (c *Controller) Sync(ctx context.Context) {
	now := time.Now()
	seen := map[string]bool{}
	all := c.src.Apps()
	for _, app := range all {
		seen[app.Name] = true
		st := Status{Copies: app.Copies, Observed: now}
		d, err := c.kube.AppsV1().Deployments(Namespace(app.Name)).Get(ctx, WorkloadName, metav1.GetOptions{})
		if err == nil {
			// A new or removed app changes the internal names of the
			// other apps.
			if err := c.ensureAliases(ctx, app.Name, all); err != nil {
				c.cfg.Logger.Warn("the controller cannot update the internal names", "app", app.Name, "err", err)
			}
			st.Ready = int(d.Status.ReadyReplicas)
			if n, err := strconv.Atoi(d.Spec.Template.Annotations[AnnotationVersion]); err == nil && st.Ready > 0 {
				st.Version = n
			}
			if !c.Busy(app.Name) && d.Spec.Replicas != nil && int(*d.Spec.Replicas) != app.Copies {
				if err := c.scale(ctx, app); err != nil {
					c.cfg.Logger.Warn("the controller cannot change the number of copies", "app", app.Name, "err", err)
				}
			}
		}
		c.mu.Lock()
		c.status[app.Name] = st
		c.mu.Unlock()
	}
	c.mu.Lock()
	for name := range c.status {
		if !seen[name] {
			delete(c.status, name)
		}
	}
	c.mu.Unlock()
}

// StatusOf gives the last observed state of the copies of an app.
func (c *Controller) StatusOf(app string) (Status, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	st, ok := c.status[app]
	return st, ok
}

// scale sets the number of copies of the Deployment.
func (c *Controller) scale(ctx context.Context, app App) error {
	deps := c.kube.AppsV1().Deployments(Namespace(app.Name))
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		d, err := deps.Get(ctx, WorkloadName, metav1.GetOptions{})
		if err != nil {
			return err
		}
		n := int32(app.Copies)
		d.Spec.Replicas = &n
		_, err = deps.Update(ctx, d, metav1.UpdateOptions{})
		return err
	})
}

// Restart starts all the copies of the app again, as "kubectl rollout
// restart".
func (c *Controller) Restart(ctx context.Context, app string) error {
	deps := c.kube.AppsV1().Deployments(Namespace(app))
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		d, err := deps.Get(ctx, WorkloadName, metav1.GetOptions{})
		if err != nil {
			return err
		}
		if d.Spec.Template.Annotations == nil {
			d.Spec.Template.Annotations = map[string]string{}
		}
		d.Spec.Template.Annotations[AnnotationRestartedAt] = time.Now().UTC().Format(time.RFC3339)
		_, err = deps.Update(ctx, d, metav1.UpdateOptions{})
		return err
	})
}

// Remove deletes the namespace of the app, with all its objects and the
// data of its folders.
func (c *Controller) Remove(ctx context.Context, app string) error {
	nss := c.kube.CoreV1().Namespaces()
	cur, err := nss.Get(ctx, Namespace(app), metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if cur.Labels[LabelManaged] != "true" || cur.Labels[LabelApp] != app {
		return fmt.Errorf("the namespace %s is not of this app", cur.Name)
	}
	err = nss.Delete(ctx, cur.Name, metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}
