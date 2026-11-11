package apps

import (
	"context"
	"errors"
	"fmt"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

// CopyImage is the image of the copy job of a move. It is the image of the
// helper pods of the storage provisioner, thus k3s has it already.
const CopyImage = "rancher/mirrored-library-busybox:1.37.0"

// moveJobName gives the name of the copy job of a folder.
func moveJobName(folder string) string { return "move-" + folder }

// BuildMoveJob gives the job that copies the files of a folder from the
// old claim to the new claim. "cp -a" keeps the owners and the modes of
// the files.
func BuildMoveJob(app string, f Folder, from, to string) *batchv1.Job {
	zero := int32(0)
	l := objectLabels(app)
	l[LabelFolder] = f.Name
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: moveJobName(f.Name), Namespace: Namespace(app), Labels: l},
		Spec: batchv1.JobSpec{
			BackoffLimit: &zero,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: l},
				Spec: corev1.PodSpec{
					RestartPolicy:                corev1.RestartPolicyNever,
					AutomountServiceAccountToken: ptr(false),
					Containers: []corev1.Container{{
						Name:            "copy",
						Image:           CopyImage,
						ImagePullPolicy: corev1.PullIfNotPresent,
						Command:         []string{"sh", "-c", "cp -a /from/. /to/ && sync"},
						VolumeMounts: []corev1.VolumeMount{
							{Name: "from", MountPath: "/from", ReadOnly: true},
							{Name: "to", MountPath: "/to"},
						},
					}},
					Volumes: []corev1.Volume{
						{Name: "from", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: from, ReadOnly: true}}},
						{Name: "to", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: to}}},
					},
				},
			},
		},
	}
}

// Move copies a folder to its target tier and writes the result. The app
// stops during the copy. When the copy fails, the app starts again on the
// old tier and the old data stay.
func (c *Controller) Move(ctx context.Context, app App, f Folder) {
	log := c.cfg.Logger.With("app", app.Name, "folder", f.Name, "from", f.Tier, "to", f.MovingTo)
	log.Info("the move of the folder starts")
	err := c.move(ctx, app, f)
	if ctx.Err() != nil {
		return // the move starts again at the next start of the service
	}
	reason := ""
	if err != nil {
		reason = trim(err.Error())
		log.Warn("the move failed", "err", err)
	}
	if err := c.src.FinishMove(app.Name, f.Name, reason); err != nil {
		log.Warn("the controller cannot write the result of the move", "err", err)
		return
	}
	// Start the app again with the folder on the new tier (or on the old
	// tier after a failure).
	if next, ok := c.src.Get(app.Name); ok && next.Active != 0 {
		if v, ok := next.Version(next.Active); ok {
			if r := c.rollout(ctx, next, v); r != "" {
				log.Warn("the app did not start again after the move", "reason", r)
				return
			}
		}
	}
	if err == nil {
		c.cleanupMove(ctx, app.Name, f, f.Tier)
		log.Info("the folder is on the new tier")
	} else {
		c.cleanupMove(ctx, app.Name, f, f.MovingTo)
	}
	c.Sync(ctx)
}

func (c *Controller) move(ctx context.Context, app App, f Folder) error {
	ns := Namespace(app.Name)
	from, to := ClaimName(f.Name, f.Tier), ClaimName(f.Name, f.MovingTo)
	claims := c.kube.CoreV1().PersistentVolumeClaims(ns)
	if _, err := claims.Get(ctx, from, metav1.GetOptions{}); apierrors.IsNotFound(err) {
		return nil // the app never ran: there are no files to copy
	} else if err != nil {
		return err
	}
	if err := c.stopCopies(ctx, app.Name); err != nil {
		return fmt.Errorf("the app did not stop: %w", err)
	}
	target := f
	target.Tier = f.MovingTo
	if _, err := claims.Create(ctx, BuildClaim(app.Name, target), metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("the new folder: %w", err)
	}
	jobs := c.kube.BatchV1().Jobs(ns)
	c.deleteJob(ctx, ns, f.Name) // a job of a move that stopped before
	if _, err := jobs.Create(ctx, BuildMoveJob(app.Name, f, from, to), metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("the copy job: %w", err)
	}
	deadline := time.Now().Add(c.cfg.MoveTimeout)
	for {
		j, err := jobs.Get(ctx, moveJobName(f.Name), metav1.GetOptions{})
		if err != nil && ctx.Err() == nil {
			return err
		}
		for _, cond := range j.Status.Conditions {
			if cond.Status != corev1.ConditionTrue {
				continue
			}
			switch cond.Type {
			case batchv1.JobComplete:
				return nil
			case batchv1.JobFailed:
				return errors.New("the copy failed: " + firstNonEmpty(cond.Message, cond.Reason))
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the copy did not end in %s", c.cfg.MoveTimeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(c.cfg.PollInterval):
		}
	}
}

// stopCopies sets the copies of the app to 0 and waits until no pod of
// the app runs.
func (c *Controller) stopCopies(ctx context.Context, app string) error {
	stopped := App{Name: app, Copies: 0}
	if err := c.scale(ctx, stopped); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	deadline := time.Now().Add(c.cfg.ProgressTimeout)
	for {
		pods, err := c.kube.CoreV1().Pods(Namespace(app)).List(ctx, metav1.ListOptions{
			LabelSelector: labels.Set{"app.kubernetes.io/name": WorkloadName}.String()})
		if err != nil {
			return err
		}
		if len(pods.Items) == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%d copies still run", len(pods.Items))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(c.cfg.PollInterval):
		}
	}
}

func (c *Controller) deleteJob(ctx context.Context, ns, folder string) {
	bg := metav1.DeletePropagationBackground
	_ = c.kube.BatchV1().Jobs(ns).Delete(ctx, moveJobName(folder), metav1.DeleteOptions{PropagationPolicy: &bg})
}

// cleanupMove deletes the copy job and the claim on the tier that the
// folder does not use.
func (c *Controller) cleanupMove(ctx context.Context, app string, f Folder, unusedTier string) {
	ns := Namespace(app)
	c.deleteJob(ctx, ns, f.Name)
	err := c.kube.CoreV1().PersistentVolumeClaims(ns).Delete(ctx, ClaimName(f.Name, unusedTier), metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		c.cfg.Logger.Warn("the controller cannot delete the old folder", "app", app, "folder", f.Name, "err", err)
	}
}
