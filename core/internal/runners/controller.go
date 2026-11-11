package runners

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/fathorMB/NidaOS/core/internal/github"
)

// Defaults of the controller.
const (
	DefaultRunnerVersion = "2.338.2"
	DefaultBuildKit      = "docker.io/moby/buildkit:v0.34.1-rootless"
	DefaultCPUMillis     = 2000
	DefaultMemoryBytes   = 4 << 30
	DefaultDiskBytes     = 10 << 30
	// MaxPodFailures is the number of failed pods for one job. Then the
	// controller marks the job as failed.
	MaxPodFailures = 3
	// MaxLogBytes is the maximum size of a job log that NidaOS keeps.
	MaxLogBytes = 5 << 20
)

// Options configure the controller.
type Options struct {
	// RunnerImage is the image of the GitHub runner without the version.
	// The default is ghcr.io/actions/actions-runner.
	RunnerImage string
	// RunnerVersion fixes the version of the runner. Empty: the controller
	// uses the last release of GitHub, and reads it again each day.
	RunnerVersion string
	BuildKitImage string
	Limits        Limits
	// JobURL is the job endpoint for the pods, and JobHost and JobPort are
	// its address for the network policy.
	JobURL  string
	JobHost string
	JobPort int32
	// RegistryHost is the host of the registry in the push names of the
	// pods. It is the job endpoint.
	RegistryHost string
	// DeployRegistry is the name of the local registry in the image of a
	// deploy (decision D21). The default is localhost:5000.
	DeployRegistry string
	// PollInterval is the time between two reads of GitHub. The default is
	// 15 seconds.
	PollInterval time.Duration
	// Deadline is the maximum time of a pod. The default is 2 hours.
	Deadline time.Duration
	// StartTimeout is the maximum time for a pod to start its runner. The
	// default is 10 minutes.
	StartTimeout time.Duration
	// AppExists tells if the app is there.
	AppExists func(string) bool
	Logger    *slog.Logger
	Now       func() time.Time
}

func (o *Options) defaults() {
	if o.RunnerImage == "" {
		o.RunnerImage = "ghcr.io/actions/actions-runner"
	}
	if o.BuildKitImage == "" {
		o.BuildKitImage = DefaultBuildKit
	}
	if o.Limits.CPUMillis == 0 {
		o.Limits.CPUMillis = DefaultCPUMillis
	}
	if o.Limits.MemoryBytes == 0 {
		o.Limits.MemoryBytes = DefaultMemoryBytes
	}
	if o.Limits.DiskBytes == 0 {
		o.Limits.DiskBytes = DefaultDiskBytes
	}
	if o.DeployRegistry == "" {
		o.DeployRegistry = "localhost:5000"
	}
	if o.PollInterval == 0 {
		o.PollInterval = 15 * time.Second
	}
	if o.Deadline == 0 {
		o.Deadline = 2 * time.Hour
	}
	if o.StartTimeout == 0 {
		o.StartTimeout = 10 * time.Minute
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.AppExists == nil {
		o.AppExists = func(string) bool { return true }
	}
}

// Controller starts and stops the runner pods.
type Controller struct {
	store  *Store
	gh     *github.Client
	kube   kubernetes.Interface
	tokens *Tokens
	opts   Options

	mu        sync.Mutex
	version   string
	versionAt time.Time
	failures  map[int64]int
	kick      chan struct{}
}

// NewController makes a controller.
func NewController(store *Store, gh *github.Client, kube kubernetes.Interface, tokens *Tokens, opts Options) *Controller {
	opts.defaults()
	return &Controller{store: store, gh: gh, kube: kube, tokens: tokens, opts: opts,
		failures: map[int64]int{}, kick: make(chan struct{}, 1)}
}

// Kick asks for a sync now.
func (c *Controller) Kick() {
	select {
	case c.kick <- struct{}{}:
	default:
	}
}

// Run syncs until ctx stops.
func (c *Controller) Run(ctx context.Context) {
	for {
		if err := c.ensureBase(ctx); err != nil {
			c.opts.Logger.Warn("the runner namespace is not ready", "err", err)
		} else {
			c.Sync(ctx)
		}
		select {
		case <-ctx.Done():
			return
		case <-c.kick:
		case <-time.After(c.opts.PollInterval):
		}
	}
}

// ensureBase makes the namespace and the network policy of the runners.
func (c *Controller) ensureBase(ctx context.Context) error {
	ns := BuildNamespace()
	if _, err := c.kube.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	np := BuildNetworkPolicy(c.opts.JobHost, c.opts.JobPort)
	cur, err := c.kube.NetworkingV1().NetworkPolicies(Namespace).Get(ctx, np.Name, metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
		_, err = c.kube.NetworkingV1().NetworkPolicies(Namespace).Create(ctx, np, metav1.CreateOptions{})
	case err == nil:
		np.ResourceVersion = cur.ResourceVersion
		_, err = c.kube.NetworkingV1().NetworkPolicies(Namespace).Update(ctx, np, metav1.UpdateOptions{})
	}
	return err
}

// Sync reads GitHub for each connection, then cleans the pods.
func (c *Controller) Sync(ctx context.Context) {
	for _, conn := range c.store.Connections() {
		problem := ""
		if err := c.syncRepo(ctx, conn); err != nil {
			problem = err.Error()
			c.opts.Logger.Warn("the runner sync of a repository failed", "app", conn.App, "repo", conn.FullName(), "err", err)
		}
		_ = c.store.UpdateConnection(conn.App, func(x *Connection) { x.Problem = problem })
	}
	if err := c.syncPods(ctx); err != nil {
		c.opts.Logger.Warn("the runner pods are not available", "err", err)
	}
}

// ErrPublic: the repository became public (decision D27).
var ErrPublic = errors.New("the repository is public: NidaOS starts runners only for private repositories (decision D27)")

func (c *Controller) syncRepo(ctx context.Context, conn Connection) error {
	repo := c.gh.Repo(conn.Owner, conn.Repo)
	info, err := repo.Info(ctx)
	if err != nil {
		return err
	}
	public := !info.Private

	// The jobs that NidaOS knows.
	for _, j := range c.store.Jobs(conn.App) {
		if !j.Final() {
			if err := c.syncJob(ctx, repo, j, public); err != nil {
				c.opts.Logger.Warn("the read of a job failed", "app", conn.App, "job", j.ID, "err", err)
			}
		}
	}
	if public {
		return ErrPublic
	}
	if !c.opts.AppExists(conn.App) {
		return fmt.Errorf("the app %s does not exist", conn.App)
	}

	// The new jobs.
	runs, err := repo.QueuedRuns(ctx)
	if err != nil {
		return err
	}
	for _, run := range runs {
		jobs, err := repo.Jobs(ctx, run.ID)
		if err != nil {
			return err
		}
		for _, gj := range jobs {
			if gj.Status != "queued" || !slices.Contains(gj.Labels, AppLabel(conn.App)) {
				continue
			}
			if _, known := c.store.Job(gj.ID); known {
				continue
			}
			j := Job{ID: gj.ID, App: conn.App, RunID: run.ID, Name: gj.Name, SHA: gj.HeadSHA, URL: gj.HTMLURL, State: JobQueued}
			if err := c.store.AddJob(j); err != nil {
				return err
			}
			if err := c.startPod(ctx, repo, j); err != nil {
				c.opts.Logger.Warn("the runner pod did not start", "app", conn.App, "job", j.ID, "err", err)
				_ = c.store.UpdateJob(j.ID, func(x *Job) { x.Message = "The runner pod did not start: " + err.Error() })
			}
		}
	}
	return nil
}

// runnerImage gives the image of the runner with the version.
func (c *Controller) runnerImage(ctx context.Context, repo *github.Repo) string {
	if c.opts.RunnerVersion != "" {
		return c.opts.RunnerImage + ":" + c.opts.RunnerVersion
	}
	c.mu.Lock()
	v, at := c.version, c.versionAt
	c.mu.Unlock()
	if v == "" || c.opts.Now().Sub(at) > 24*time.Hour {
		if nv, err := repo.LatestRunnerVersion(ctx); err == nil && nv != "" {
			v = nv
			c.mu.Lock()
			c.version, c.versionAt = v, c.opts.Now()
			c.mu.Unlock()
		} else if v == "" {
			v = DefaultRunnerVersion
		}
	}
	return c.opts.RunnerImage + ":" + v
}

func randomSuffix() string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// startPod registers a runner for one job and starts its pod.
func (c *Controller) startPod(ctx context.Context, repo *github.Repo, j Job) error {
	name := "run-" + j.App + "-" + randomSuffix()
	jit, err := repo.JITConfig(ctx, name, RunnerLabels(j.App))
	if err != nil {
		return err
	}
	tok := c.tokens.Issue(j.App, name, c.opts.Deadline+10*time.Minute)
	sec := BuildSecret(name, j.App, j.ID, jit.Config, tok)
	if _, err := c.kube.CoreV1().Secrets(Namespace).Create(ctx, sec, metav1.CreateOptions{}); err != nil {
		c.tokens.RevokePod(name)
		_ = repo.DeleteRunner(ctx, jit.ID)
		return err
	}
	pod := BuildPod(PodSpec{
		Name: name, App: j.App, JobID: j.ID, Secret: name, JobURL: c.opts.JobURL, Registry: c.opts.RegistryHost,
		DeployRegistry: c.opts.DeployRegistry,
		Images:         Images{Runner: c.runnerImage(ctx, repo), BuildKit: c.opts.BuildKitImage},
		Limits:         c.opts.Limits,
		Deadline:       int64(c.opts.Deadline / time.Second),
	})
	if _, err := c.kube.CoreV1().Pods(Namespace).Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		c.tokens.RevokePod(name)
		_ = c.kube.CoreV1().Secrets(Namespace).Delete(ctx, name, metav1.DeleteOptions{})
		_ = repo.DeleteRunner(ctx, jit.ID)
		return err
	}
	c.opts.Logger.Info("a runner pod started", "app", j.App, "job", j.ID, "pod", name)
	return c.store.UpdateJob(j.ID, func(x *Job) { x.Pod, x.RunnerID, x.Message = name, jit.ID, "" })
}

// syncJob reads one job on GitHub and changes its state.
func (c *Controller) syncJob(ctx context.Context, repo *github.Repo, j Job, public bool) error {
	gj, err := repo.Job(ctx, j.ID)
	if errors.Is(err, github.ErrNotFound) {
		c.finish(ctx, repo, j, JobFailed, "GitHub does not have the job any more.")
		return nil
	}
	if err != nil {
		return err
	}
	switch gj.Status {
	case "completed":
		state := JobFailed
		switch gj.Conclusion {
		case "success":
			state = JobSucceeded
		case "cancelled", "skipped":
			state = JobCancelled
		}
		_ = c.store.UpdateJob(j.ID, func(x *Job) {
			x.Runner = gj.RunnerName
			if gj.StartedAt != nil {
				x.Started = gj.StartedAt
			}
		})
		if log, err := repo.JobLog(ctx, j.ID, MaxLogBytes); err == nil {
			_ = c.store.WriteLog(j.ID, log)
		} else {
			c.opts.Logger.Warn("the job log is not available", "job", j.ID, "err", err)
		}
		msg := ""
		if state == JobFailed {
			msg = "The job failed on GitHub: " + gj.Conclusion + "."
		}
		c.finish(ctx, repo, j, state, msg)
		return nil
	case "in_progress":
		if j.State != JobRunning || j.Runner != gj.RunnerName {
			_ = c.store.UpdateJob(j.ID, func(x *Job) {
				x.State, x.Runner, x.Started = JobRunning, gj.RunnerName, gj.StartedAt
			})
		}
		return nil
	}

	// The job waits. Its pod must run; a public repository gets no pod.
	if public {
		return nil
	}
	pod, err := c.podOf(ctx, j)
	if err != nil {
		return err
	}
	reason := podProblem(pod, c.opts.Now(), c.opts.StartTimeout)
	if reason == "" {
		return nil
	}
	c.stopPod(ctx, repo, j.Pod, j.RunnerID)
	c.mu.Lock()
	c.failures[j.ID]++
	n := c.failures[j.ID]
	c.mu.Unlock()
	if n >= MaxPodFailures {
		_ = c.store.UpdateJob(j.ID, func(x *Job) {
			now := c.opts.Now().UTC()
			x.State, x.Finished, x.Pod, x.Message = JobFailed, &now, "", "The runner pod failed "+strconv.Itoa(n)+" times: "+reason
		})
		return nil
	}
	_ = c.store.UpdateJob(j.ID, func(x *Job) { x.Pod, x.Message = "", "The runner pod failed, NidaOS starts a new one: "+reason })
	j.Pod = ""
	return c.startPod(ctx, repo, j)
}

func (c *Controller) podOf(ctx context.Context, j Job) (*corev1.Pod, error) {
	if j.Pod == "" {
		return nil, nil
	}
	p, err := c.kube.CoreV1().Pods(Namespace).Get(ctx, j.Pod, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	return p, err
}

// podProblem tells why a pod cannot run its job, or "" when it can.
func podProblem(p *corev1.Pod, now time.Time, startTimeout time.Duration) string {
	if p == nil {
		return "the pod is not there"
	}
	switch p.Status.Phase {
	case corev1.PodFailed:
		return "the pod failed: " + p.Status.Reason + " " + p.Status.Message
	case corev1.PodSucceeded:
		return "the runner stopped before the job"
	}
	all := append(append([]corev1.ContainerStatus(nil), p.Status.InitContainerStatuses...), p.Status.ContainerStatuses...)
	for _, s := range all {
		if w := s.State.Waiting; w != nil {
			switch w.Reason {
			case "ErrImagePull", "ImagePullBackOff", "InvalidImageName", "CreateContainerConfigError", "CrashLoopBackOff":
				if now.Sub(p.CreationTimestamp.Time) > 2*time.Minute || w.Reason == "InvalidImageName" {
					return s.Name + ": " + w.Reason + " " + w.Message
				}
			}
		}
		if t := s.State.Terminated; t != nil && t.ExitCode != 0 && s.Name == "tools" {
			return "the tools did not install: " + t.Reason
		}
	}
	if p.Status.Phase == corev1.PodPending && !p.CreationTimestamp.IsZero() && now.Sub(p.CreationTimestamp.Time) > startTimeout {
		return "the pod did not start in " + startTimeout.String()
	}
	return ""
}

// finish marks a job as final and stops its pod.
func (c *Controller) finish(ctx context.Context, repo *github.Repo, j Job, state, msg string) {
	now := c.opts.Now().UTC()
	_ = c.store.UpdateJob(j.ID, func(x *Job) {
		x.State, x.Finished = state, &now
		if msg != "" {
			x.Message = msg
		}
	})
	c.mu.Lock()
	delete(c.failures, j.ID)
	c.mu.Unlock()
	c.opts.Logger.Info("a deploy job ended", "app", j.App, "job", j.ID, "state", state)
	// The pod of the job stops by itself after the job. If an other runner
	// took the job, the pod waits with no job: stop it.
	if j.Pod != "" && j.Runner != j.Pod {
		c.stopPod(ctx, repo, j.Pod, j.RunnerID)
	}
}

// stopPod removes a pod, its Secret, its tokens and its runner on GitHub.
func (c *Controller) stopPod(ctx context.Context, repo *github.Repo, pod string, runnerID int64) {
	if pod == "" {
		return
	}
	c.tokens.RevokePod(pod)
	_ = c.kube.CoreV1().Pods(Namespace).Delete(ctx, pod, metav1.DeleteOptions{})
	_ = c.kube.CoreV1().Secrets(Namespace).Delete(ctx, pod, metav1.DeleteOptions{})
	if repo != nil && runnerID != 0 {
		_ = repo.DeleteRunner(ctx, runnerID)
	}
}

// syncPods removes the pods that ended and the pods of no job.
func (c *Controller) syncPods(ctx context.Context) error {
	pods, err := c.kube.CoreV1().Pods(Namespace).List(ctx, metav1.ListOptions{LabelSelector: LabelRunner + "=true"})
	if err != nil {
		return err
	}
	active := map[string]Job{}
	for _, j := range c.store.Jobs("") {
		if j.Pod != "" {
			active[j.Pod] = j
		}
	}
	for _, p := range pods.Items {
		j, known := active[p.Name]
		ended := p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed
		if known && !j.Final() && !ended {
			continue
		}
		if known && !j.Final() && ended && j.State == JobQueued {
			continue // syncJob starts a new pod and removes this one
		}
		var repo *github.Repo
		if conn, ok := c.store.Connection(p.Labels[LabelApp]); ok {
			repo = c.gh.Repo(conn.Owner, conn.Repo)
		}
		c.stopPod(ctx, repo, p.Name, j.RunnerID)
	}
	return nil
}

// RemoveApp stops the pods of an app. The disconnect and the remove of an
// app call it.
func (c *Controller) RemoveApp(ctx context.Context, app string) {
	conn, ok := c.store.Connection(app)
	var repo *github.Repo
	if ok {
		repo = c.gh.Repo(conn.Owner, conn.Repo)
	}
	for _, j := range c.store.Jobs(app) {
		if j.Final() {
			continue
		}
		c.stopPod(ctx, repo, j.Pod, j.RunnerID)
		now := c.opts.Now().UTC()
		_ = c.store.UpdateJob(j.ID, func(x *Job) {
			x.State, x.Finished, x.Pod, x.Message = JobCancelled, &now, "", "The repository was disconnected."
		})
	}
}
