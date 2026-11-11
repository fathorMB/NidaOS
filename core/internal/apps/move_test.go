package apps

import (
	"context"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func folderApp() App {
	a := webApp()
	a.Folders = []Folder{{Name: "data", Path: "/data", Tier: TierStandard, SizeBytes: 1 << 30}}
	a.Active = 1
	a.Versions[0].State = StateServing
	return a
}

// endJob sets the condition of the copy job when it appears.
func endJob(t *testing.T, kube *fake.Clientset, ns, name string, cond batchv1.JobConditionType) {
	t.Helper()
	jobs := kube.BatchV1().Jobs(ns)
	for range 200 {
		j, err := jobs.Get(context.Background(), name, metav1.GetOptions{})
		if err == nil {
			j.Status.Conditions = []batchv1.JobCondition{{Type: cond, Status: corev1.ConditionTrue, Message: "disk full"}}
			if _, err := jobs.UpdateStatus(context.Background(), j, metav1.UpdateOptions{}); err == nil {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Error("the copy job did not appear")
}

func TestMoveCopiesTheFolderToTheNewTier(t *testing.T) {
	app := folderApp()
	src := newFakeSource(app)
	c, kube := testController(t, src)
	ctx := context.Background()
	go markReady(t, kube, "blog", 1)
	if r := c.rollout(ctx, app, app.Versions[0]); r != "" {
		t.Fatal(r)
	}
	ns := Namespace("blog")
	d, _ := kube.AppsV1().Deployments(ns).Get(ctx, WorkloadName, metav1.GetOptions{})
	if d.Spec.Strategy.Type != "Recreate" || d.Spec.Template.Spec.Volumes[0].PersistentVolumeClaim.ClaimName != "folder-data-standard" {
		t.Fatalf("deployment with a folder: %+v", d.Spec)
	}
	pvc, err := kube.CoreV1().PersistentVolumeClaims(ns).Get(ctx, "folder-data-standard", metav1.GetOptions{})
	if err != nil || *pvc.Spec.StorageClassName != "nida-standard" {
		t.Fatalf("claim: %v %v", pvc, err)
	}

	src.mu.Lock()
	a := src.apps["blog"]
	a.Folders[0].MovingTo = TierPerformance
	src.apps["blog"] = a
	src.mu.Unlock()
	moving, _ := src.Get("blog")
	f, _ := moving.Moving()

	go endJob(t, kube, ns, "move-data", batchv1.JobComplete)
	go func() {
		// The app starts again on the new tier.
		time.Sleep(100 * time.Millisecond)
		markReady(t, kube, "blog", 1)
	}()
	c.Move(ctx, moving, f)

	got, _ := src.Get("blog")
	if got.Folders[0].Tier != TierPerformance || got.Folders[0].MovingTo != "" || got.Folders[0].MoveError != "" {
		t.Fatalf("folder after the move: %+v (%v)", got.Folders[0], src.results)
	}
	if _, err := kube.CoreV1().PersistentVolumeClaims(ns).Get(ctx, "folder-data-performance", metav1.GetOptions{}); err != nil {
		t.Errorf("the new claim is not there: %v", err)
	}
	if _, err := kube.CoreV1().PersistentVolumeClaims(ns).Get(ctx, "folder-data-standard", metav1.GetOptions{}); err == nil {
		t.Error("the old claim is still there")
	}
	d, _ = kube.AppsV1().Deployments(ns).Get(ctx, WorkloadName, metav1.GetOptions{})
	if d.Spec.Template.Spec.Volumes[0].PersistentVolumeClaim.ClaimName != "folder-data-performance" || *d.Spec.Replicas != 1 {
		t.Errorf("the app does not use the new folder: %+v", d.Spec.Template.Spec.Volumes)
	}
}

func TestFailedMoveKeepsTheOldTier(t *testing.T) {
	app := folderApp()
	src := newFakeSource(app)
	c, kube := testController(t, src)
	ctx := context.Background()
	go markReady(t, kube, "blog", 1)
	_ = c.rollout(ctx, app, app.Versions[0])
	ns := Namespace("blog")

	app.Folders[0].MovingTo = TierPerformance
	src.mu.Lock()
	src.apps["blog"] = app
	src.mu.Unlock()
	go endJob(t, kube, ns, "move-data", batchv1.JobFailed)
	go func() {
		time.Sleep(100 * time.Millisecond)
		markReady(t, kube, "blog", 1)
	}()
	c.Move(ctx, app, app.Folders[0])

	got, _ := src.Get("blog")
	if got.Folders[0].Tier != TierStandard || got.Folders[0].MoveError == "" {
		t.Fatalf("folder after a failed move: %+v", got.Folders[0])
	}
	if _, err := kube.CoreV1().PersistentVolumeClaims(ns).Get(ctx, "folder-data-standard", metav1.GetOptions{}); err != nil {
		t.Error("the old data went away after a failed move")
	}
	if _, err := kube.CoreV1().PersistentVolumeClaims(ns).Get(ctx, "folder-data-performance", metav1.GetOptions{}); err == nil {
		t.Error("the half copy is still there")
	}
}

func TestStoreMoveRules(t *testing.T) {
	s, _ := newStore(t)
	_, _ = s.Create(App{Name: "blog", Copies: 1, Folders: []Folder{{Name: "data", Path: "/data", Tier: TierStandard, SizeBytes: MinFolderBytes}}})
	if _, err := s.RequestMove("blog", "data", TierStandard); !IsFieldError(err) {
		t.Errorf("move to the same tier: %v", err)
	}
	if _, err := s.RequestMove("blog", "none", TierPerformance); err != ErrFolderNotFound {
		t.Errorf("move of an unknown folder: %v", err)
	}
	if _, err := s.RequestMove("blog", "data", TierPerformance); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Deploy("blog", "localhost:5000/blog:1", "", ""); err != ErrMoving {
		t.Errorf("deploy during a move: %v", err)
	}
	if err := s.FinishMove("blog", "data", ""); err != nil {
		t.Fatal(err)
	}
	a, _ := s.Get("blog")
	if a.Folders[0].Tier != TierPerformance {
		t.Errorf("tier %s", a.Folders[0].Tier)
	}
}
