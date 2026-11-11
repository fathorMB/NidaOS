package databases

import (
	"context"
	"errors"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ErrNoPort: all the ports for the home network are in use.
var ErrNoPort = errors.New("all the ports for the home network are in use")

// SetLAN turns the access from the home network on or off. On, the
// database gets the first free port from 15432 to 15531. The port accepts
// only TLS with a password (decision D24).
func (c *Controller) SetLAN(ctx context.Context, db string, on bool) (Database, error) {
	d, ok := c.store.Get(db)
	if !ok {
		return Database{}, ErrNotFound
	}
	if d.State != StateReady || c.Busy(db) {
		return Database{}, ErrNotReady
	}
	if d.LAN == on {
		return d, nil
	}
	port := 0
	if on {
		var err error
		if port, err = c.freePort(); err != nil {
			return Database{}, err
		}
	}
	d, err := c.store.Change(db, func(x *Database) error {
		x.LAN, x.LANPort = on, port
		return nil
	})
	if err != nil {
		return Database{}, err
	}
	if err := c.applyLAN(ctx, d); err != nil {
		return d, fmt.Errorf("the access from the home network: %w", err)
	}
	c.opts.Logger.Info("the access from the home network changed", "database", db, "on", on, "port", port)
	return d, nil
}

// freePort gives the first port that no database uses.
func (c *Controller) freePort() (int, error) {
	used := map[int]bool{}
	for _, d := range c.store.List() {
		if d.LAN {
			used[d.LANPort] = true
		}
	}
	for p := LANPortFirst; p <= LANPortLast; p++ {
		if !used[p] {
			return p, nil
		}
	}
	return 0, ErrNoPort
}

// applyLAN makes or deletes the Service and the network policy of the
// home network.
func (c *Controller) applyLAN(ctx context.Context, d Database) error {
	ns := Namespace(d.Name)
	if d.LAN {
		if err := c.ensureService(ctx, BuildLANService(d.Name, d.LANPort)); err != nil {
			return err
		}
		return c.ensureNetworkPolicy(ctx, BuildLANNetworkPolicy(d.Name))
	}
	if err := c.kube.CoreV1().Services(ns).Delete(ctx, LANServiceName, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	if err := c.kube.NetworkingV1().NetworkPolicies(ns).Delete(ctx, LANNetworkPolicyName, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}
