package databases

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// The names and the labels of the Kubernetes objects of a database.
const (
	// NamespacePrefix: each database has its own namespace "nida-db-<name>".
	NamespacePrefix = "nida-db-"
	// LabelManaged marks the namespaces and the objects of the controller.
	LabelManaged = "nidaos.io/managed-db"
	// LabelDB gives the name of the database.
	LabelDB = "nidaos.io/db"
	// LabelLinkPrefix: the namespace of a connected app has the label
	// "nidaos.io/db-link-<database>=true". The network policy of the
	// database accepts only these namespaces.
	LabelLinkPrefix = "nidaos.io/db-link-"

	// WorkloadName is the name of the StatefulSet, the Service and the
	// container. The pod is pg-0.
	WorkloadName = "pg"
	PodName      = "pg-0"

	SecretTLS    = "pg-tls"
	SecretConfig = "pg-config"

	// AdminRole is the superuser of the controller. It can connect only
	// through the local socket in the container.
	AdminRole = "nida_admin"
	// PGData is a folder in the volume: the root of a local-path volume does
	// not accept chmod.
	PGData = "/var/lib/postgresql/data/pgdata"
	// PostgresUID is the user of the official alpine image.
	PostgresUID int64 = 70
	// PostgresPort is the port of the server in the pod.
	PostgresPort = 5432

	annotationSpecHash = "nidaos.io/spec-hash"
)

// Namespace gives the namespace of a database.
func Namespace(db string) string { return NamespacePrefix + db }

// ServiceHost gives the internal address of a database for the apps.
func ServiceHost(db string) string {
	return WorkloadName + "." + Namespace(db) + ".svc.cluster.local"
}

// StorageClass gives the storage class of a tier.
func StorageClass(tier string) string { return "nida-" + tier }

// LinkLabel gives the label of the namespace of an app that the database
// accepts.
func LinkLabel(db string) string { return LabelLinkPrefix + db }

func selectorLabels(db string) map[string]string {
	return map[string]string{LabelDB: db, "app.kubernetes.io/name": WorkloadName}
}

func objectLabels(db string) map[string]string {
	l := selectorLabels(db)
	l[LabelManaged] = "true"
	l["app.kubernetes.io/managed-by"] = "nida-core"
	return l
}

// BuildNamespace gives the namespace of a database, with the Pod Security
// level restricted: the server never runs as root.
func BuildNamespace(db string) *corev1.Namespace {
	return &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: Namespace(db), Labels: map[string]string{
		LabelManaged:                         "true",
		LabelDB:                              db,
		"pod-security.kubernetes.io/enforce": "restricted",
		"app.kubernetes.io/managed-by":       "nida-core",
	}}}
}

// HBA is pg_hba.conf. The superuser of the controller connects only
// through the local socket. On the network, the server accepts only TLS
// with a password (decision D24).
const HBA = `# Made by NidaOS (decision D24). Do not change it.
local     all   ` + AdminRole + `   trust
local     all   all                reject
hostssl   all   ` + AdminRole + `   0.0.0.0/0   reject
hostssl   all   ` + AdminRole + `   ::/0        reject
hostssl   all   all                0.0.0.0/0   scram-sha-256
hostssl   all   all                ::/0        scram-sha-256
hostnossl all   all                0.0.0.0/0   reject
hostnossl all   all                ::/0        reject
`

// BuildConfigSecret gives pg_hba.conf in a Secret.
func BuildConfigSecret(db string) *corev1.Secret {
	return &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: SecretConfig, Namespace: Namespace(db), Labels: objectLabels(db)},
		Data: map[string][]byte{"pg_hba.conf": []byte(HBA)}}
}

// BuildTLSSecret gives the certificate of the server: the certificate of
// the NidaOS authority for nidaos.local (decision D20).
func BuildTLSSecret(db string, certPEM, keyPEM []byte) *corev1.Secret {
	return &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: SecretTLS, Namespace: Namespace(db), Labels: objectLabels(db)},
		Type: corev1.SecretTypeTLS,
		Data: map[string][]byte{corev1.TLSCertKey: certPEM, corev1.TLSPrivateKeyKey: keyPEM}}
}

// BuildService gives the internal Service on port 5432.
func BuildService(db string) *corev1.Service {
	return &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: WorkloadName, Namespace: Namespace(db), Labels: objectLabels(db)},
		Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeClusterIP, Selector: selectorLabels(db),
			Ports: []corev1.ServicePort{{Name: "postgres", Port: PostgresPort, TargetPort: intstr.FromInt32(PostgresPort), Protocol: corev1.ProtocolTCP}}}}
}

// NetworkPolicyName is the network policy that accepts only the connected
// apps. The database has no connection out.
const NetworkPolicyName = "db-isolation"

// BuildNetworkPolicy gives the isolation policy of a database.
func BuildNetworkPolicy(db string) *networkingv1.NetworkPolicy {
	tcp := corev1.ProtocolTCP
	port := intstr.FromInt32(PostgresPort)
	return &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: NetworkPolicyName, Namespace: Namespace(db), Labels: objectLabels(db)},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress},
			Ingress: []networkingv1.NetworkPolicyIngressRule{{
				From:  []networkingv1.NetworkPolicyPeer{{NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{LinkLabel(db): "true"}}}},
				Ports: []networkingv1.NetworkPolicyPort{{Protocol: &tcp, Port: &port}},
			}},
		}}
}

// The access from the home network (T-04): a LoadBalancer Service of k3s
// on the port of the database, and a network policy that accepts the
// connections from outside the pod network.
const (
	LANServiceName       = "pg-lan"
	LANNetworkPolicyName = "db-lan"
	// PodCIDR is the pod network of k3s. The home network is not in it.
	PodCIDR = "10.42.0.0/16"
	// LANPortFirst and LANPortLast are the ports for the home network.
	LANPortFirst = 15432
	LANPortLast  = 15531
)

// BuildLANService gives the Service of the home network on port.
func BuildLANService(db string, port int) *corev1.Service {
	return &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: LANServiceName, Namespace: Namespace(db), Labels: objectLabels(db)},
		Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer, Selector: selectorLabels(db),
			Ports: []corev1.ServicePort{{Name: "postgres-lan", Port: int32(port), TargetPort: intstr.FromInt32(PostgresPort), Protocol: corev1.ProtocolTCP}}}}
}

// BuildLANNetworkPolicy accepts port 5432 from each address outside the
// pod network. The apps that are not connected stay refused.
func BuildLANNetworkPolicy(db string) *networkingv1.NetworkPolicy {
	tcp := corev1.ProtocolTCP
	port := intstr.FromInt32(PostgresPort)
	return &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: LANNetworkPolicyName, Namespace: Namespace(db), Labels: objectLabels(db)},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
			Ingress: []networkingv1.NetworkPolicyIngressRule{{
				From:  []networkingv1.NetworkPolicyPeer{{IPBlock: &networkingv1.IPBlock{CIDR: "0.0.0.0/0", Except: []string{PodCIDR}}}},
				Ports: []networkingv1.NetworkPolicyPort{{Protocol: &tcp, Port: &port}},
			}},
		}}
}

func ptr[T any](v T) *T { return &v }

// BuildStatefulSet gives the server of a database. The pod runs as the
// user 70 with a read-only root file system. The size is the request of
// the volume claim, but local-path does not apply it: it is a soft limit.
func BuildStatefulSet(d Database) *appsv1.StatefulSet {
	mem := *resource.NewQuantity(d.MemoryBytes, resource.BinarySI)
	cpu := *resource.NewMilliQuantity(int64(d.CPUMillis), resource.DecimalSI)
	size := *resource.NewQuantity(d.SizeBytes, resource.BinarySI)
	secretMode := int32(0o440)
	ready := []string{"pg_isready", "-h", "/var/run/postgresql", "-U", AdminRole}
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: WorkloadName, Namespace: Namespace(d.Name), Labels: objectLabels(d.Name)},
		Spec: appsv1.StatefulSetSpec{
			Replicas:    ptr(int32(1)),
			ServiceName: WorkloadName,
			Selector:    &metav1.LabelSelector{MatchLabels: selectorLabels(d.Name)},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: objectLabels(d.Name)},
				Spec: corev1.PodSpec{
					AutomountServiceAccountToken:  ptr(false),
					EnableServiceLinks:            ptr(false),
					TerminationGracePeriodSeconds: ptr(int64(60)),
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot: ptr(true), RunAsUser: ptr(PostgresUID), RunAsGroup: ptr(PostgresUID), FSGroup: ptr(PostgresUID),
						SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
					},
					Containers: []corev1.Container{{
						Name:  WorkloadName,
						Image: d.Image,
						Args: []string{
							"-c", "listen_addresses=*",
							"-c", "ssl=on",
							"-c", "ssl_cert_file=/etc/nidaos/pg-tls/tls.crt",
							"-c", "ssl_key_file=/etc/nidaos/pg-tls/tls.key",
							"-c", "hba_file=/etc/nidaos/pg-config/pg_hba.conf",
							"-c", "password_encryption=scram-sha-256",
						},
						Env: []corev1.EnvVar{
							{Name: "POSTGRES_USER", Value: AdminRole},
							{Name: "POSTGRES_DB", Value: "postgres"},
							// trust is only for initdb and the local socket:
							// pg_hba.conf refuses the admin on the network.
							{Name: "POSTGRES_HOST_AUTH_METHOD", Value: "trust"},
							{Name: "PGDATA", Value: PGData},
						},
						Ports: []corev1.ContainerPort{{Name: "postgres", ContainerPort: PostgresPort}},
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{corev1.ResourceCPU: cpu, corev1.ResourceMemory: mem},
							Limits:   corev1.ResourceList{corev1.ResourceMemory: mem},
						},
						SecurityContext: &corev1.SecurityContext{
							AllowPrivilegeEscalation: ptr(false), ReadOnlyRootFilesystem: ptr(true), RunAsNonRoot: ptr(true),
							RunAsUser: ptr(PostgresUID), RunAsGroup: ptr(PostgresUID),
							Capabilities:   &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
							SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
						},
						ReadinessProbe: &corev1.Probe{ProbeHandler: corev1.ProbeHandler{Exec: &corev1.ExecAction{Command: ready}},
							InitialDelaySeconds: 3, PeriodSeconds: 3, FailureThreshold: 20},
						LivenessProbe: &corev1.Probe{ProbeHandler: corev1.ProbeHandler{Exec: &corev1.ExecAction{Command: ready}},
							InitialDelaySeconds: 60, PeriodSeconds: 10, FailureThreshold: 6},
						VolumeMounts: []corev1.VolumeMount{
							{Name: "data", MountPath: "/var/lib/postgresql/data"},
							{Name: "run", MountPath: "/var/run/postgresql"},
							{Name: "tmp", MountPath: "/tmp"},
							{Name: "pg-tls", MountPath: "/etc/nidaos/pg-tls", ReadOnly: true},
							{Name: "pg-config", MountPath: "/etc/nidaos/pg-config", ReadOnly: true},
						},
					}},
					Volumes: []corev1.Volume{
						{Name: "run", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
						{Name: "tmp", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
						{Name: "pg-tls", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: SecretTLS, DefaultMode: &secretMode}}},
						{Name: "pg-config", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: SecretConfig}}},
					},
				},
			},
			VolumeClaimTemplates: []corev1.PersistentVolumeClaim{{
				ObjectMeta: metav1.ObjectMeta{Name: "data", Labels: objectLabels(d.Name)},
				Spec: corev1.PersistentVolumeClaimSpec{
					AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
					StorageClassName: ptr(StorageClass(d.Tier)),
					Resources:        corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: size}},
				},
			}},
		},
	}
	sts.Annotations = map[string]string{annotationSpecHash: specHash(sts)}
	return sts
}

// specHash is the fingerprint of the pod template. The controller writes
// the StatefulSet again only when it changes.
func specHash(sts *appsv1.StatefulSet) string {
	b, _ := json.Marshal(sts.Spec.Template)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}
