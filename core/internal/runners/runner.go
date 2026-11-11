package runners

import (
	"context"
	"os"
	"sync"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/fathorMB/NidaOS/core/internal/github"
)

// Runner starts the controller when the access file of k3s is there, as
// the runners of the apps and of the databases.
type Runner struct {
	store      *Store
	gh         *github.Client
	tokens     *Tokens
	kubeconfig string
	opts       Options

	mu sync.Mutex
	c  *Controller
}

// NewRunner makes a runner.
func NewRunner(store *Store, gh *github.Client, tokens *Tokens, kubeconfig string, opts Options) *Runner {
	opts.defaults()
	return &Runner{store: store, gh: gh, tokens: tokens, kubeconfig: kubeconfig, opts: opts}
}

// Run waits for k3s, then runs the controller until ctx stops.
func (r *Runner) Run(ctx context.Context) {
	logged := false
	for {
		kube, err := r.connect()
		if err == nil {
			c := NewController(r.store, r.gh, kube, r.tokens, r.opts)
			r.mu.Lock()
			r.c = c
			r.mu.Unlock()
			r.opts.Logger.Info("the runner controller started")
			c.Run(ctx)
			return
		}
		if !logged {
			r.opts.Logger.Info("the runner controller waits for k3s", "err", err)
			logged = true
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}

func (r *Runner) connect() (kubernetes.Interface, error) {
	if _, err := os.Stat(r.kubeconfig); err != nil {
		return nil, err
	}
	rc, err := clientcmd.BuildConfigFromFlags("", r.kubeconfig)
	if err != nil {
		return nil, err
	}
	return kubernetes.NewForConfig(rc)
}

// Controller gives the controller, or nil when it does not run yet.
func (r *Runner) Controller() *Controller {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.c
}
