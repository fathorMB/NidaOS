package apps

import (
	"context"
	"errors"
	"os"
	"sync"
	"time"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// ErrNotReady: the controller does not run yet, because k3s is not ready.
var ErrNotReady = errors.New("k3s is not ready: the app controller does not run yet")

// Runner starts the controller when the access file of k3s is there. k3s
// can start after the core service, and the service must give the state,
// the metrics and the logs also without k3s.
type Runner struct {
	src        Source
	kubeconfig string
	opts       Options
	retry      time.Duration

	mu sync.Mutex
	c  *Controller
}

// NewRunner makes a runner. Call Run to start it.
func NewRunner(src Source, kubeconfig string, opts Options) *Runner {
	opts.defaults()
	return &Runner{src: src, kubeconfig: kubeconfig, opts: opts, retry: 5 * time.Second}
}

// Run waits for k3s, then runs the controller until ctx stops.
func (r *Runner) Run(ctx context.Context) {
	logged := false
	for {
		c, err := r.connect()
		if err == nil {
			r.mu.Lock()
			r.c = c
			r.mu.Unlock()
			r.opts.Logger.Info("the app controller started", "kubeconfig", r.kubeconfig)
			c.Run(ctx)
			return
		}
		if !logged {
			r.opts.Logger.Info("the app controller waits for k3s", "kubeconfig", r.kubeconfig, "err", err)
			logged = true
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(r.retry):
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
	rc.Timeout = 30 * time.Second
	kube, err := kubernetes.NewForConfig(rc)
	if err != nil {
		return nil, err
	}
	dyn, err := dynamic.NewForConfig(rc)
	if err != nil {
		return nil, err
	}
	return New(r.src, kube, dyn, r.opts), nil
}

// Controller gives the controller, or nil when it does not run yet.
func (r *Runner) Controller() *Controller {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.c
}

// SetController sets the controller without k3s. Only for the tests.
func (r *Runner) SetController(c *Controller) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.c = c
}

// Kick tells the controller that there is a new deploy. Before k3s is
// ready, the controller finds the deploy when it starts.
func (r *Runner) Kick() {
	if c := r.Controller(); c != nil {
		c.Kick()
	}
}

// Host gives the address of an app.
func (r *Runner) Host(app string) string {
	if r.opts.Domain == "" {
		return ""
	}
	return app + "." + r.opts.Domain
}
