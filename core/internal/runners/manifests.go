package runners

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// The names and the labels of the runner objects.
const (
	// Namespace keeps all the runner pods.
	Namespace = "nida-runners"
	// LabelRunner marks the objects of the runner controller.
	LabelRunner = "nidaos.io/runner"
	// LabelApp gives the app of a runner pod.
	LabelApp = "nidaos.io/app"
	// LabelJob gives the GitHub job of a runner pod.
	LabelJob = "nidaos.io/job"

	// BuildKitAddr is the address of BuildKit in the pod. The containers of
	// a pod share the network, thus only the pod can use it.
	BuildKitAddr = "tcp://127.0.0.1:1234"
	// ToolsDir has buildctl and nida in the runner container.
	ToolsDir = "/nida/tools"
)

// RunnerLabels are the labels of the runner of an app. The workflow uses
// them in runs-on.
func RunnerLabels(app string) []string {
	return []string{"self-hosted", "nidaos", AppLabel(app)}
}

// AppLabel is the label of the runners of one app.
func AppLabel(app string) string { return "nidaos-" + app }

// PodSpec gives the data of one runner pod.
type PodSpec struct {
	Name     string
	App      string
	JobID    int64
	Secret   string // the Secret with the JIT config and the job token
	JobURL   string // the job endpoint of nida-core, for example http://10.42.0.1:8471
	Registry string // the host of the registry for the pushes, for example 10.42.0.1:8471
	// DeployRegistry is the name of the registry in the image of a deploy,
	// for example localhost:5000 (decision D21).
	DeployRegistry string
	Images         Images
	Limits         Limits
	Deadline       int64 // seconds
}

// Images are the images of a runner pod.
type Images struct {
	Runner   string // ghcr.io/actions/actions-runner:<version>
	BuildKit string // docker.io/moby/buildkit:<version>-rootless
}

// Limits are the limits of a runner pod. BuildKit gets most of them,
// because it runs the build.
type Limits struct {
	CPUMillis   int64
	MemoryBytes int64
	DiskBytes   int64
}

func (l Limits) split() (runner, buildkit corev1.ResourceRequirements) {
	rCPU, rMem := l.CPUMillis/4, l.MemoryBytes/4
	bCPU, bMem := l.CPUMillis-rCPU, l.MemoryBytes-rMem
	mk := func(cpu, mem int64) corev1.ResourceRequirements {
		return corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(cpu/10, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(mem/8, resource.BinarySI),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(cpu, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(mem, resource.BinarySI),
			},
		}
	}
	return mk(rCPU, rMem), mk(bCPU, bMem)
}

func objectLabels() map[string]string {
	return map[string]string{LabelRunner: "true", "app.kubernetes.io/managed-by": "nida-core"}
}

// BuildNamespace gives the namespace of the runners. Rootless BuildKit
// needs the seccomp profile Unconfined to make a user namespace, thus the
// namespace cannot use the restricted level. The pods do not use the
// privileged mode, and they run as normal users.
func BuildNamespace() *corev1.Namespace {
	l := objectLabels()
	l["pod-security.kubernetes.io/enforce"] = "privileged"
	l["pod-security.kubernetes.io/warn"] = "baseline"
	return &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: Namespace, Labels: l}}
}

// BuildNetworkPolicy isolates the runner pods. No connection comes in. The
// pods can open only DNS, the job endpoint, and addresses on the internet:
// not the apps, the databases or the home network.
func BuildNetworkPolicy(jobHost string, jobPort int32) *networkingv1.NetworkPolicy {
	tcp, udp := corev1.ProtocolTCP, corev1.ProtocolUDP
	dns := intstr.FromInt32(53)
	job := intstr.FromInt32(jobPort)
	return &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "runner-isolation", Namespace: Namespace, Labels: objectLabels()},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress},
			Egress: []networkingv1.NetworkPolicyEgressRule{
				{
					To: []networkingv1.NetworkPolicyPeer{{NamespaceSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"kubernetes.io/metadata.name": "kube-system"}}}},
					Ports: []networkingv1.NetworkPolicyPort{{Protocol: &udp, Port: &dns}, {Protocol: &tcp, Port: &dns}},
				},
				{
					To:    []networkingv1.NetworkPolicyPeer{{IPBlock: &networkingv1.IPBlock{CIDR: jobHost + "/32"}}},
					Ports: []networkingv1.NetworkPolicyPort{{Protocol: &tcp, Port: &job}},
				},
				{
					To: []networkingv1.NetworkPolicyPeer{{IPBlock: &networkingv1.IPBlock{
						CIDR: "0.0.0.0/0",
						Except: []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "169.254.0.0/16",
							"100.64.0.0/10", "127.0.0.0/8"},
					}}},
				},
			},
		},
	}
}

// BuildSecret keeps the JIT config and the job token of one pod.
func BuildSecret(name, app string, jobID int64, jit, token string) *corev1.Secret {
	l := objectLabels()
	l[LabelApp], l[LabelJob] = app, fmt.Sprint(jobID)
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: Namespace, Labels: l},
		StringData: map[string]string{"jitconfig": jit, "token": token},
	}
}

func ptr[T any](v T) *T { return &v }

// BuildPod gives a runner pod for one job (decision D26):
//   - the init container tools copies buildctl and gets nida from the job
//     endpoint;
//   - the sidecar buildkitd is rootless BuildKit, as the user 1000;
//   - the container runner is the GitHub runner with the JIT config. The
//     pod stops after the job.
func BuildPod(s PodSpec) *corev1.Pod {
	l := objectLabels()
	l[LabelApp], l[LabelJob] = s.App, fmt.Sprint(s.JobID)
	runnerRes, buildRes := s.Limits.split()
	always := corev1.ContainerRestartPolicyAlways
	secretEnv := func(name, key string) corev1.EnvVar {
		return corev1.EnvVar{Name: name, ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: s.Secret}, Key: key}}}
	}
	tools := corev1.VolumeMount{Name: "tools", MountPath: ToolsDir}
	disk := resource.NewQuantity(s.Limits.DiskBytes, resource.BinarySI)

	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: s.Name, Namespace: Namespace, Labels: l},
		Spec: corev1.PodSpec{
			RestartPolicy:                 corev1.RestartPolicyNever,
			ActiveDeadlineSeconds:         ptr(s.Deadline),
			AutomountServiceAccountToken:  ptr(false),
			EnableServiceLinks:            ptr(false),
			TerminationGracePeriodSeconds: ptr(int64(10)),
			SecurityContext:               &corev1.PodSecurityContext{FSGroup: ptr(int64(1001))},
			InitContainers: []corev1.Container{
				{
					Name:    "tools",
					Image:   s.Images.BuildKit,
					Command: []string{"sh", "-c"},
					Args: []string{`set -e
cp /usr/bin/buildctl ` + ToolsDir + `/buildctl
wget -q -O ` + ToolsDir + `/nida "$NIDA_SERVER/tools/nida"
chmod 0755 ` + ToolsDir + `/buildctl ` + ToolsDir + `/nida`},
					Env:          []corev1.EnvVar{{Name: "NIDA_SERVER", Value: s.JobURL}},
					VolumeMounts: []corev1.VolumeMount{tools},
					SecurityContext: &corev1.SecurityContext{
						RunAsUser: ptr(int64(1000)), RunAsNonRoot: ptr(true), AllowPrivilegeEscalation: ptr(false),
						Capabilities:   &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
						SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
					},
				},
				{
					// A sidecar: it starts before the runner and stops after it.
					Name:          "buildkitd",
					Image:         s.Images.BuildKit,
					RestartPolicy: &always,
					Args:          []string{"--oci-worker-no-process-sandbox", "--addr", BuildKitAddr},
					Resources:     buildRes,
					VolumeMounts:  []corev1.VolumeMount{{Name: "buildkit", MountPath: "/home/user/.local/share/buildkit"}},
					SecurityContext: &corev1.SecurityContext{
						RunAsUser: ptr(int64(1000)), RunAsGroup: ptr(int64(1000)), RunAsNonRoot: ptr(true),
						// rootlesskit uses newuidmap to make the user namespace.
						AllowPrivilegeEscalation: ptr(true),
						// Fedora has no AppArmor, thus the pod sets no AppArmor profile.
						SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeUnconfined},
					},
				},
			},
			Containers: []corev1.Container{{
				Name:    "runner",
				Image:   s.Images.Runner,
				Command: []string{"/home/runner/run.sh"},
				Args:    []string{"--jitconfig", "$(ACTIONS_RUNNER_INPUT_JITCONFIG)"},
				Env: []corev1.EnvVar{
					secretEnv("ACTIONS_RUNNER_INPUT_JITCONFIG", "jitconfig"),
					secretEnv("NIDA_TOKEN", "token"),
					{Name: "NIDA_SERVER", Value: s.JobURL},
					{Name: "NIDA_REGISTRY", Value: s.Registry},
					{Name: "NIDA_DEPLOY_REGISTRY", Value: s.DeployRegistry},
					{Name: "NIDA_APP", Value: s.App},
					{Name: "BUILDKIT_HOST", Value: BuildKitAddr},
					{Name: "PATH", Value: ToolsDir + ":/home/runner/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"},
				},
				Resources:    runnerRes,
				VolumeMounts: []corev1.VolumeMount{tools, {Name: "work", MountPath: "/home/runner/_work"}},
				SecurityContext: &corev1.SecurityContext{
					RunAsUser: ptr(int64(1001)), RunAsNonRoot: ptr(true), AllowPrivilegeEscalation: ptr(false),
					Capabilities:   &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
					SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
				},
			}},
			Volumes: []corev1.Volume{
				{Name: "tools", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{SizeLimit: resource.NewQuantity(256<<20, resource.BinarySI)}}},
				{Name: "work", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{SizeLimit: disk}}},
				{Name: "buildkit", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{SizeLimit: disk}}},
			},
		},
	}
}
