package databases

import (
	"context"
	"os"
	"sync"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// Runner starts the controller when the access file of k3s is there, as
// the runner of the apps.
type Runner struct {
	store      *Store
	kubeconfig string
	opts       Options
	apps       AppLinker

	mu sync.Mutex
	c  *Controller
}

// NewRunner makes a runner. apps can be nil: then the links are not
// available.
func NewRunner(store *Store, kubeconfig string, apps AppLinker, opts Options) *Runner {
	opts.defaults()
	return &Runner{store: store, kubeconfig: kubeconfig, opts: opts, apps: apps}
}

// Run waits for k3s, then runs the controller until ctx stops.
func (r *Runner) Run(ctx context.Context) {
	logged := false
	for {
		c, err := r.connect()
		if err == nil {
			r.SetController(c)
			r.opts.Logger.Info("the database controller started")
			c.Run(ctx)
			return
		}
		if !logged {
			r.opts.Logger.Info("the database controller waits for k3s", "err", err)
			logged = true
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}

func (r *Runner) connect() (*Controller, error) {
	if _, err := os.Stat(r.kubeconfig); err != nil {
		return nil, err
	}
	rc, err := clientcmd.BuildConfigFromFlags("", r.kubeconfig)
	if err != nil {
		return nil, err
	}
	kube, err := kubernetes.NewForConfig(rc)
	if err != nil {
		return nil, err
	}
	pod := &PodExecutor{Config: rc, Client: kube}
	c := NewController(r.store, kube, pod, &Psql{Pod: pod, Redact: r.store.AllSecrets}, r.opts)
	if r.apps != nil {
		c.SetApps(r.apps)
	}
	return c, nil
}

// Controller gives the controller, or nil when it does not run yet.
func (r *Runner) Controller() *Controller {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.c
}

// SetController sets the controller. The tests use it without k3s.
func (r *Runner) SetController(c *Controller) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.c = c
}

// Store gives the store of the databases.
func (r *Runner) Store() *Store { return r.store }

// Images gives the image of each major version.
func (r *Runner) Images() map[int]string { return r.opts.Images }

// DefaultVersion is the version of a new database without a version.
const DefaultVersion = 18
