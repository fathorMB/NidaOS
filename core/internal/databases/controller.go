package databases

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"
)

// Options is the configuration of the controller.
type Options struct {
	// Images gives the image of each major version (decision D23), for
	// example 18: "docker.io/library/postgres:18.1-alpine".
	Images map[int]string
	// TLS gives the certificate of the server and its key (decision D20).
	TLS func() (certPEM, keyPEM []byte, err error)
	// ReadyTimeout is the maximum time for a new server to accept
	// connections. The default is 5 minutes.
	ReadyTimeout time.Duration
	// PollInterval is the time between two reads of the server state.
	PollInterval time.Duration
	// LoopInterval and SyncEvery: the time between two searches for work,
	// and between two syncs of the ready databases.
	LoopInterval time.Duration
	SyncEvery    time.Duration
	// BackupDir keeps the backups (decision D24). Empty: no backups.
	BackupDir string
	// BackupHour is the hour (local time) of the daily backups. The
	// default is 3.
	BackupHour int
	// KeepDaily and KeepManual are the numbers of backups that stay. The
	// defaults are 7 and 5.
	KeepDaily  int
	KeepManual int
	// StatsEvery is the time between two reads of the statistics. The
	// default is 10 seconds.
	StatsEvery time.Duration
	Now        func() time.Time
	Logger     *slog.Logger
}

func (o *Options) defaults() {
	if o.ReadyTimeout <= 0 {
		o.ReadyTimeout = 5 * time.Minute
	}
	if o.PollInterval <= 0 {
		o.PollInterval = 2 * time.Second
	}
	if o.LoopInterval <= 0 {
		o.LoopInterval = 2 * time.Second
	}
	if o.SyncEvery <= 0 {
		o.SyncEvery = 15 * time.Second
	}
	if o.BackupHour <= 0 || o.BackupHour > 23 {
		o.BackupHour = 3
	}
	if o.KeepDaily <= 0 {
		o.KeepDaily = 7
	}
	if o.KeepManual <= 0 {
		o.KeepManual = 5
	}
	if o.StatsEvery <= 0 {
		o.StatsEvery = 10 * time.Second
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
}

// Controller operates the databases of the store on k3s.
type Controller struct {
	store *Store
	kube  kubernetes.Interface
	sql   SQLRunner
	pod   Executor
	apps  AppLinker
	opts  Options

	kick    chan struct{}
	wg      sync.WaitGroup
	mu      sync.Mutex
	running map[string]bool
	stats   map[string]statsEntry
}

// NewController makes a controller. sql runs the SQL in the pods, and pod
// runs pg_dump and pg_restore.
func NewController(store *Store, kube kubernetes.Interface, pod Executor, sql SQLRunner, opts Options) *Controller {
	opts.defaults()
	return &Controller{store: store, kube: kube, sql: sql, pod: pod, opts: opts,
		kick: make(chan struct{}, 1), running: map[string]bool{}, stats: map[string]statsEntry{}}
}

// Kick tells the loop that there is new work.
func (c *Controller) Kick() {
	select {
	case c.kick <- struct{}{}:
	default:
	}
}

// Busy tells if the controller works on a database now.
func (c *Controller) Busy(db string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.running[db]
}

// Run does the work of the databases until ctx stops.
func (c *Controller) Run(ctx context.Context) {
	tick := time.NewTicker(c.opts.LoopInterval)
	defer tick.Stop()
	defer c.wg.Wait()
	var lastSync, lastBackup, lastStats time.Time
	for {
		for _, d := range c.store.List() {
			if d.Busy() {
				c.start(ctx, &c.wg, d.Name, func(ctx context.Context) { c.operate(ctx, d) })
			}
		}
		if now := time.Now(); now.Sub(lastSync) >= c.opts.SyncEvery {
			lastSync = now
			c.Sync(ctx)
		}
		if now := time.Now(); now.Sub(lastStats) >= c.opts.StatsEvery {
			lastStats = now
			c.collectStats(ctx)
		}
		if now := time.Now(); now.Sub(lastBackup) >= time.Minute {
			lastBackup = now
			c.dailyBackups(ctx)
		}
		select {
		case <-ctx.Done():
			return
		case <-c.kick:
		case <-tick.C:
		}
	}
}

// start runs fn in a goroutine, when no other work on the database runs.
func (c *Controller) start(ctx context.Context, wg *sync.WaitGroup, db string, fn func(context.Context)) bool {
	c.mu.Lock()
	if c.running[db] {
		c.mu.Unlock()
		return false
	}
	c.running[db] = true
	c.mu.Unlock()
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer func() {
			c.mu.Lock()
			delete(c.running, db)
			c.mu.Unlock()
			c.Kick()
		}()
		fn(ctx)
	}()
	return true
}

// operate does the operation of the state of the database and writes the
// result.
func (c *Controller) operate(ctx context.Context, d Database) {
	log := c.opts.Logger.With("database", d.Name, "state", d.State)
	var err error
	switch d.State {
	case StateCreating:
		err = c.create(ctx, d)
	case StateRestoring:
		err = c.restore(ctx, d)
	case StateUpdating:
		err = c.update(ctx, d)
	default:
		return
	}
	if ctx.Err() != nil {
		return // the service stops: the operation starts again at the next start
	}
	if err != nil {
		log.Warn("the database operation failed", "err", err)
		state := StateFailed
		if d.State == StateUpdating {
			// The update failed, but the server runs with the old image.
			state = StateReady
		}
		_, _ = c.store.Change(d.Name, func(x *Database) error {
			x.State, x.Reason, x.UpdateTo, x.Restore = state, trim(err.Error()), "", nil
			return nil
		})
		return
	}
	log.Info("the database operation is complete")
}

// create makes the objects of the database, waits for the server and
// makes the roles.
func (c *Controller) create(ctx context.Context, d Database) error {
	if err := c.apply(ctx, d); err != nil {
		return err
	}
	if err := c.setupRoles(ctx, d); err != nil {
		return err
	}
	return c.store.SetState(d.Name, StateReady, "")
}

// setupRoles waits for the server and makes the roles. The server of a
// new volume starts two times (the first start makes the data), thus a
// failure can be short.
func (c *Controller) setupRoles(ctx context.Context, d Database) error {
	if err := c.waitReady(ctx, d.Name); err != nil {
		return err
	}
	deadline := time.Now().Add(c.opts.ReadyTimeout)
	for {
		err := SetupRoles(ctx, c.sql, d.Name, c.store.OwnerPassword(d.Name))
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(c.opts.PollInterval):
		}
	}
}

// apply makes or updates all the objects of a database.
func (c *Controller) apply(ctx context.Context, d Database) error {
	if err := c.ensureNamespace(ctx, d.Name); err != nil {
		return fmt.Errorf("namespace: %w", err)
	}
	ns := Namespace(d.Name)
	if err := c.ensureSecret(ctx, BuildConfigSecret(d.Name)); err != nil {
		return fmt.Errorf("pg_hba.conf: %w", err)
	}
	if c.opts.TLS == nil {
		return errors.New("the certificate of the server is not available")
	}
	certPEM, keyPEM, err := c.opts.TLS()
	if err != nil {
		return fmt.Errorf("the certificate of the server: %w", err)
	}
	if err := c.ensureSecret(ctx, BuildTLSSecret(d.Name, certPEM, keyPEM)); err != nil {
		return fmt.Errorf("the certificate of the server: %w", err)
	}
	if err := c.ensureService(ctx, BuildService(d.Name)); err != nil {
		return fmt.Errorf("service: %w", err)
	}
	if err := c.ensureNetworkPolicy(ctx, BuildNetworkPolicy(d.Name)); err != nil {
		return fmt.Errorf("network policy: %w", err)
	}
	want := BuildStatefulSet(d)
	sets := c.kube.AppsV1().StatefulSets(ns)
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cur, err := sets.Get(ctx, WorkloadName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			_, err = sets.Create(ctx, want, metav1.CreateOptions{})
			return err
		}
		if err != nil {
			return err
		}
		if cur.Annotations[annotationSpecHash] == want.Annotations[annotationSpecHash] {
			return nil
		}
		// The volume claim template cannot change: only the pod template.
		cur.Spec.Template = want.Spec.Template
		if cur.Annotations == nil {
			cur.Annotations = map[string]string{}
		}
		cur.Annotations[annotationSpecHash] = want.Annotations[annotationSpecHash]
		_, err = sets.Update(ctx, cur, metav1.UpdateOptions{})
		return err
	})
}

func (c *Controller) ensureNamespace(ctx context.Context, db string) error {
	want := BuildNamespace(db)
	nss := c.kube.CoreV1().Namespaces()
	cur, err := nss.Get(ctx, want.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = nss.Create(ctx, want, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	if cur.Labels[LabelManaged] != "true" || cur.Labels[LabelDB] != db {
		return fmt.Errorf("the namespace %s exists, but it is not of this database", want.Name)
	}
	if cur.DeletionTimestamp != nil {
		return fmt.Errorf("the namespace %s of a removed database is not deleted yet: try again in some seconds", want.Name)
	}
	return nil
}

func (c *Controller) ensureSecret(ctx context.Context, want *corev1.Secret) error {
	secrets := c.kube.CoreV1().Secrets(want.Namespace)
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cur, err := secrets.Get(ctx, want.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			_, err = secrets.Create(ctx, want.DeepCopy(), metav1.CreateOptions{})
			return err
		}
		if err != nil {
			return err
		}
		cur.Data, cur.Type = want.Data, want.Type
		_, err = secrets.Update(ctx, cur, metav1.UpdateOptions{})
		return err
	})
}

func (c *Controller) ensureService(ctx context.Context, want *corev1.Service) error {
	services := c.kube.CoreV1().Services(want.Namespace)
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cur, err := services.Get(ctx, want.Name, metav1.GetOptions{})
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

func (c *Controller) ensureNetworkPolicy(ctx context.Context, want *networkingv1.NetworkPolicy) error {
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

// waitReady waits until the pod of the server is ready.
func (c *Controller) waitReady(ctx context.Context, db string) error {
	deadline := time.Now().Add(c.opts.ReadyTimeout)
	for {
		sts, err := c.kube.AppsV1().StatefulSets(Namespace(db)).Get(ctx, WorkloadName, metav1.GetOptions{})
		if err == nil && statefulSetReady(sts) {
			return nil
		}
		if time.Now().After(deadline) {
			reason := fmt.Sprintf("the server did not start in %s", c.opts.ReadyTimeout)
			if r := c.podProblem(ctx, db); r != "" {
				reason += ": " + r
			}
			return errors.New(reason)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(c.opts.PollInterval):
		}
	}
}

func statefulSetReady(s *appsv1.StatefulSet) bool {
	return s.Status.ObservedGeneration >= s.Generation && s.Status.ReadyReplicas == 1 &&
		s.Status.UpdatedReplicas == 1 && (s.Status.UpdateRevision == "" || s.Status.CurrentRevision == s.Status.UpdateRevision)
}

// podProblem gives the reason when the pod of the server has a problem.
func (c *Controller) podProblem(ctx context.Context, db string) string {
	p, err := c.kube.CoreV1().Pods(Namespace(db)).Get(ctx, PodName, metav1.GetOptions{})
	if err != nil {
		return "the pod is not there"
	}
	for _, cs := range p.Status.ContainerStatuses {
		if w := cs.State.Waiting; w != nil {
			return trim(w.Reason + ": " + w.Message)
		}
	}
	for _, cond := range p.Status.Conditions {
		if cond.Type == corev1.PodScheduled && cond.Status == corev1.ConditionFalse {
			return trim("Kubernetes cannot put the pod on the computer: " + cond.Message)
		}
	}
	return ""
}

func trim(s string) string {
	const max = 300
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}

// Sync applies the objects of each ready database again. Thus a deleted
// object comes back, and a new certificate goes to the servers.
func (c *Controller) Sync(ctx context.Context) {
	for _, d := range c.store.List() {
		if d.State != StateReady || c.Busy(d.Name) {
			continue
		}
		if err := c.apply(ctx, d); err != nil {
			c.opts.Logger.Warn("the controller cannot sync the database", "database", d.Name, "err", err)
		}
		if err := c.applyLAN(ctx, d); err != nil {
			c.opts.Logger.Warn("the controller cannot sync the access from the home network", "database", d.Name, "err", err)
		}
		c.syncLinks(ctx, d)
	}
}

// Remove deletes the namespace of the database, with its data.
func (c *Controller) Remove(ctx context.Context, db string) error {
	nss := c.kube.CoreV1().Namespaces()
	cur, err := nss.Get(ctx, Namespace(db), metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if cur.Labels[LabelManaged] != "true" || cur.Labels[LabelDB] != db {
		return fmt.Errorf("the namespace %s is not of this database", cur.Name)
	}
	err = nss.Delete(ctx, cur.Name, metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}
