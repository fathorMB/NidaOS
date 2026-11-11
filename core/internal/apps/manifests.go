package apps

import (
	"sort"
	"strconv"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// The names and the labels of the Kubernetes objects of an app.
const (
	// NamespacePrefix: each app has its own namespace "nida-app-<name>".
	NamespacePrefix = "nida-app-"
	// WorkloadName is the name of the Deployment, the Service and the
	// container of each app.
	WorkloadName = "app"
	// LabelManaged marks the namespaces and the objects of the controller.
	// The controller changes only the objects with this label.
	LabelManaged = "nidaos.io/managed-app"
	// LabelApp gives the name of the app.
	LabelApp = "nidaos.io/app"
	// LabelAlias marks the internal names of the other apps (T-07).
	LabelAlias = "nidaos.io/alias"
	// LabelFolder gives the name of the folder of a volume claim.
	LabelFolder = "nidaos.io/folder"
	// AnnotationVersion gives the version of a pod.
	AnnotationVersion = "nidaos.io/version"
	// AnnotationRestartedAt starts all the copies again, as
	// "kubectl rollout restart".
	AnnotationRestartedAt = "kubectl.kubernetes.io/restartedAt"

	// secretName is the Secret with the secrets of the app.
	secretName = "app-secrets"
	// servicePort is the port of the Service for the route.
	servicePort = 80
	// declaredPortName is the port of the Service with the number of the app
	// port, for the other apps (T-07).
	declaredPortName = "app"
	// copyPodStopSeconds: a copy stays in operation some seconds after the
	// stop signal, thus the route stops to send requests to it first.
	copyPodStopSeconds = 5
)

// Namespace gives the namespace of an app.
func Namespace(app string) string { return NamespacePrefix + app }

// StorageClass gives the storage class of a tier (image/rootfs/usr/share/
// nidaos/k3s/nida-storage.yaml).
func StorageClass(tier string) string { return "nida-" + tier }

// ClaimName gives the volume claim of a folder on a tier. The tier is in
// the name, thus a move can copy from the old claim to the new claim.
func ClaimName(folder, tier string) string { return "folder-" + folder + "-" + tier }

func selectorLabels(app string) map[string]string {
	return map[string]string{"app.kubernetes.io/name": WorkloadName, LabelApp: app}
}

func objectLabels(app string) map[string]string {
	return map[string]string{LabelManaged: "true", LabelApp: app, "app.kubernetes.io/managed-by": "nida-core"}
}

func podLabels(app string) map[string]string {
	l := objectLabels(app)
	for k, v := range selectorLabels(app) {
		l[k] = v
	}
	return l
}

// BuildOptions are the parts of a Deployment that do not come from the
// version.
type BuildOptions struct {
	// Copies is the number of copies. 0 stops the app.
	Copies int32
	// ProgressSeconds is the maximum time of a rollout.
	ProgressSeconds int32
	// Secrets is true when the Secret of the app exists.
	Secrets bool
	// RestartedAt keeps the restart annotation of the current Deployment.
	RestartedAt string
	// Folders are the folders of the app, with the current tier.
	Folders []Folder
}

// BuildNamespace gives the namespace of an app. The Pod Security level is
// baseline: the app can run as root, but never with privileges, host
// folders or host networks.
func BuildNamespace(app string) *corev1.Namespace {
	l := objectLabels(app)
	l["pod-security.kubernetes.io/enforce"] = "baseline"
	return &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: Namespace(app), Labels: l}}
}

// BuildDeployment gives the Deployment of a version.
func BuildDeployment(app string, v Version, o BuildOptions) *appsv1.Deployment {
	cfg := v.Config
	ann := map[string]string{AnnotationVersion: strconv.Itoa(v.Number)}
	if o.RestartedAt != "" {
		ann[AnnotationRestartedAt] = o.RestartedAt
	}
	no := false
	deadline := o.ProgressSeconds
	if deadline <= 0 {
		deadline = 120
	}
	c := corev1.Container{
		Name:            WorkloadName,
		Image:           v.Image,
		ImagePullPolicy: corev1.PullIfNotPresent,
		Env:             envVars(app, v),
		SecurityContext: &corev1.SecurityContext{
			AllowPrivilegeEscalation: &no,
			Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
			SeccompProfile:           &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
		},
		Resources: corev1.ResourceRequirements{Limits: resourceList(cfg.Limits)},
	}
	if o.Secrets {
		c.EnvFrom = []corev1.EnvFromSource{{SecretRef: &corev1.SecretEnvSource{
			LocalObjectReference: corev1.LocalObjectReference{Name: secretName}}}}
	}
	minReady := int32(0)
	if cfg.Port != nil {
		c.Ports = []corev1.ContainerPort{{Name: "http", ContainerPort: int32(*cfg.Port), Protocol: corev1.ProtocolTCP}}
		c.ReadinessProbe, c.LivenessProbe = probes(cfg)
		c.Lifecycle = &corev1.Lifecycle{PreStop: &corev1.LifecycleHandler{Sleep: &corev1.SleepAction{Seconds: copyPodStopSeconds}}}
	} else {
		// Without a port there is nothing to examine. The version passes
		// when the process stays alive for some seconds.
		minReady = 5
	}
	pod := corev1.PodSpec{
		AutomountServiceAccountToken: &no,
		EnableServiceLinks:           &no,
		Containers:                   []corev1.Container{c},
	}
	// The new copy starts before the old copy stops.
	zero, one := intstr.FromInt32(0), intstr.FromInt32(1)
	strategy := appsv1.DeploymentStrategy{
		Type:          appsv1.RollingUpdateDeploymentStrategyType,
		RollingUpdate: &appsv1.RollingUpdateDeployment{MaxUnavailable: &zero, MaxSurge: &one},
	}
	if len(o.Folders) > 0 {
		// Two copies cannot write in the same folder. Thus the old copy
		// stops first, then the new copy starts.
		strategy = appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType}
		for _, f := range o.Folders {
			name := "folder-" + f.Name
			pod.Volumes = append(pod.Volumes, corev1.Volume{Name: name, VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: ClaimName(f.Name, f.Tier)}}})
			pod.Containers[0].VolumeMounts = append(pod.Containers[0].VolumeMounts, corev1.VolumeMount{Name: name, MountPath: f.Path})
		}
	}
	copies := o.Copies
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: WorkloadName, Namespace: Namespace(app), Labels: objectLabels(app)},
		Spec: appsv1.DeploymentSpec{
			Replicas:                &copies,
			MinReadySeconds:         minReady,
			ProgressDeadlineSeconds: &deadline,
			RevisionHistoryLimit:    ptr(int32(3)),
			Selector:                &metav1.LabelSelector{MatchLabels: selectorLabels(app)},
			Strategy:                strategy,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: podLabels(app), Annotations: ann},
				Spec:       pod,
			},
		},
	}
}

func ptr[T any](v T) *T { return &v }

// probes gives the readiness and the liveness checks. Without a health
// check path, the check opens the TCP port.
func probes(cfg Config) (ready, live *corev1.Probe) {
	port := intstr.FromInt32(int32(*cfg.Port))
	handler := corev1.ProbeHandler{TCPSocket: &corev1.TCPSocketAction{Port: port}}
	period, timeout := int32(5), int32(2)
	if hc := cfg.HealthCheck; hc != nil {
		path := hc.Path
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		handler = corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: path, Port: port}}
		if hc.IntervalSeconds > 0 {
			period = int32(hc.IntervalSeconds)
		}
		if hc.TimeoutSeconds > 0 {
			timeout = int32(hc.TimeoutSeconds)
		}
	}
	ready = &corev1.Probe{ProbeHandler: handler, PeriodSeconds: period, TimeoutSeconds: timeout, SuccessThreshold: 1, FailureThreshold: 2}
	// The liveness check starts after the readiness check had more
	// attempts. Thus it does not stop an app that starts slowly.
	live = &corev1.Probe{ProbeHandler: handler, InitialDelaySeconds: 15, PeriodSeconds: max(period, 5), TimeoutSeconds: timeout, FailureThreshold: 3}
	return ready, live
}

// System variables. The prefix NIDA_ is reserved: the variables and the
// secrets of the app cannot use it.
const (
	EnvApp     = "NIDA_APP"
	EnvVersion = "NIDA_VERSION"
	EnvCommit  = "NIDA_COMMIT"
	EnvPort    = "PORT"
)

// ReservedEnvName tells if the app cannot use a variable name.
func ReservedEnvName(name string) bool {
	return strings.HasPrefix(name, "NIDA_") || name == EnvPort
}

func envVars(app string, v Version) []corev1.EnvVar {
	keys := make([]string, 0, len(v.Config.Variables))
	for k := range v.Config.Variables {
		if !ReservedEnvName(k) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	env := make([]corev1.EnvVar, 0, len(keys)+4)
	for _, k := range keys {
		env = append(env, corev1.EnvVar{Name: k, Value: v.Config.Variables[k]})
	}
	env = append(env,
		corev1.EnvVar{Name: EnvApp, Value: app},
		corev1.EnvVar{Name: EnvVersion, Value: strconv.Itoa(v.Number)},
	)
	if v.Commit != "" {
		env = append(env, corev1.EnvVar{Name: EnvCommit, Value: v.Commit})
	}
	if v.Config.Port != nil {
		env = append(env, corev1.EnvVar{Name: EnvPort, Value: strconv.Itoa(*v.Config.Port)})
	}
	return env
}

func resourceList(l Limits) corev1.ResourceList {
	r := corev1.ResourceList{}
	if l.CPUMillis > 0 {
		r[corev1.ResourceCPU] = *resource.NewMilliQuantity(int64(l.CPUMillis), resource.DecimalSI)
	}
	if l.MemoryBytes > 0 {
		r[corev1.ResourceMemory] = *resource.NewQuantity(l.MemoryBytes, resource.BinarySI)
	}
	if len(r) == 0 {
		return nil
	}
	return r
}

// BuildService gives the Service of an app, or nil when the app has no
// port. Port 80 is for the route. When the app port is not 80, the Service
// also has the app port, thus the other apps can use "http://<app>:<port>".
func BuildService(app string, cfg Config) *corev1.Service {
	if cfg.Port == nil {
		return nil
	}
	ports := []corev1.ServicePort{{Name: "http", Port: servicePort, TargetPort: intstr.FromString("http"), Protocol: corev1.ProtocolTCP}}
	if *cfg.Port != servicePort {
		ports = append(ports, corev1.ServicePort{Name: declaredPortName, Port: int32(*cfg.Port), TargetPort: intstr.FromInt32(int32(*cfg.Port)), Protocol: corev1.ProtocolTCP})
	}
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: WorkloadName, Namespace: Namespace(app), Labels: objectLabels(app)},
		Spec:       corev1.ServiceSpec{Selector: selectorLabels(app), Ports: ports},
	}
}

// BuildSecret gives the Secret with the secrets of the app. The pods get
// each secret as an environment variable.
func BuildSecret(app string, values map[string]string) *corev1.Secret {
	data := make(map[string][]byte, len(values))
	for k, v := range values {
		data[k] = []byte(v)
	}
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: Namespace(app), Labels: objectLabels(app)},
		Type:       corev1.SecretTypeOpaque,
		Data:       data,
	}
}

// BuildClaim gives the volume claim of a folder.
func BuildClaim(app string, f Folder) *corev1.PersistentVolumeClaim {
	class := StorageClass(f.Tier)
	l := objectLabels(app)
	l[LabelFolder] = f.Name
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: ClaimName(f.Name, f.Tier), Namespace: Namespace(app), Labels: l},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			StorageClassName: &class,
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: *resource.NewQuantity(f.SizeBytes, resource.BinarySI)},
			},
		},
	}
}

// IngressNamespace is the namespace of Traefik, the ingress controller of
// k3s.
const IngressNamespace = "kube-system"

// BuildNetworkPolicy gives the only network policy of an app. A copy
// accepts connections only from Traefik and from the other apps. The
// connections out of the app are not limited.
func BuildNetworkPolicy(app string) *networkingv1.NetworkPolicy {
	return &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "app-isolation", Namespace: Namespace(app), Labels: objectLabels(app)},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
			Ingress: []networkingv1.NetworkPolicyIngressRule{{
				From: []networkingv1.NetworkPolicyPeer{
					{NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"kubernetes.io/metadata.name": IngressNamespace}}},
					{NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{LabelManaged: "true"}}},
				},
			}},
		},
	}
}

// RouteName is the name of the Traefik route of an app.
const RouteName = "app"

// BuildRoute gives the Traefik IngressRoute of an app at host. The route
// uses the default certificate of Traefik: the certificate of the NidaOS
// authority for *.nidaos.local (decision D20).
func BuildRoute(app, host string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "traefik.io/v1alpha1",
		"kind":       "IngressRoute",
		"metadata": map[string]any{
			"name":      RouteName,
			"namespace": Namespace(app),
			"labels":    toAny(objectLabels(app)),
		},
		"spec": map[string]any{
			"entryPoints": []any{"websecure"},
			"routes": []any{map[string]any{
				"match":    "Host(`" + host + "`)",
				"kind":     "Rule",
				"services": []any{map[string]any{"name": WorkloadName, "port": int64(servicePort)}},
			}},
			"tls": map[string]any{},
		},
	}}
	return u
}

func toAny(m map[string]string) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
