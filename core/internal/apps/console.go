package apps

import (
	"context"
	"fmt"
	"net"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/util/retry"
)

// The web console is at https://<domain> (decision D29). Traefik gets the
// requests on its HTTPS entry with the certificate of the NidaOS authority
// and sends them to nida-core on the computer. These objects are in the
// namespace of Traefik.
const (
	// ConsoleName is the name of the Service, the routes and the
	// EndpointSlice of the console.
	ConsoleName = "nida-console"
	// ConsoleRedirect is the Traefik middleware that sends the browser from
	// http:// to https://.
	ConsoleRedirect = "nida-https"
)

var middlewareGVR = schema.GroupVersionResource{Group: "traefik.io", Version: "v1alpha1", Resource: "middlewares"}

func consoleLabels() map[string]string {
	return map[string]string{"app.kubernetes.io/managed-by": "nida-core", "app.kubernetes.io/name": ConsoleName}
}

// SplitUpstream gives the address and the port of "10.42.0.1:8470".
func SplitUpstream(upstream string) (string, int32, error) {
	host, p, err := net.SplitHostPort(upstream)
	if err != nil {
		return "", 0, err
	}
	ip := net.ParseIP(host)
	if ip == nil || ip.To4() == nil {
		return "", 0, fmt.Errorf("the address %q is not an IPv4 address", host)
	}
	port, err := strconv.Atoi(p)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, fmt.Errorf("the port %q is not correct", p)
	}
	return host, int32(port), nil
}

// BuildConsoleService gives the Service without a selector. Its only
// endpoint is nida-core (BuildConsoleEndpoints).
func BuildConsoleService(port int32) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: ConsoleName, Namespace: IngressNamespace, Labels: consoleLabels()},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeClusterIP,
			Ports: []corev1.ServicePort{{
				Name: "http", Protocol: corev1.ProtocolTCP, Port: port, TargetPort: intstr.FromInt32(port),
			}},
		},
	}
}

// BuildConsoleEndpoints gives the EndpointSlice with the address of
// nida-core on the bridge of the pod network.
func BuildConsoleEndpoints(addr string, port int32) *discoveryv1.EndpointSlice {
	labels := consoleLabels()
	labels[discoveryv1.LabelServiceName] = ConsoleName
	labels[discoveryv1.LabelManagedBy] = "nida-core"
	ready := true
	name, proto := "http", corev1.ProtocolTCP
	return &discoveryv1.EndpointSlice{
		ObjectMeta:  metav1.ObjectMeta{Name: ConsoleName, Namespace: IngressNamespace, Labels: labels},
		AddressType: discoveryv1.AddressTypeIPv4,
		Endpoints: []discoveryv1.Endpoint{{
			Addresses:  []string{addr},
			Conditions: discoveryv1.EndpointConditions{Ready: &ready},
		}},
		Ports: []discoveryv1.EndpointPort{{Name: &name, Port: &port, Protocol: &proto}},
	}
}

// BuildConsoleRedirect gives the middleware that changes http to https.
func BuildConsoleRedirect() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "traefik.io/v1alpha1",
		"kind":       "Middleware",
		"metadata":   map[string]any{"name": ConsoleRedirect, "namespace": IngressNamespace, "labels": toAny(consoleLabels())},
		"spec":       map[string]any{"redirectScheme": map[string]any{"scheme": "https", "permanent": true}},
	}}
}

// BuildConsoleRoutes gives the two routes of the console: https at host,
// and http at host that sends the browser to https.
func BuildConsoleRoutes(host string, port int32) []*unstructured.Unstructured {
	service := []any{map[string]any{"name": ConsoleName, "port": int64(port)}}
	match := "Host(`" + host + "`)"
	secure := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "traefik.io/v1alpha1",
		"kind":       "IngressRoute",
		"metadata":   map[string]any{"name": ConsoleName, "namespace": IngressNamespace, "labels": toAny(consoleLabels())},
		"spec": map[string]any{
			"entryPoints": []any{"websecure"},
			"routes":      []any{map[string]any{"match": match, "kind": "Rule", "services": service}},
			"tls":         map[string]any{},
		},
	}}
	plain := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "traefik.io/v1alpha1",
		"kind":       "IngressRoute",
		"metadata":   map[string]any{"name": ConsoleName + "-http", "namespace": IngressNamespace, "labels": toAny(consoleLabels())},
		"spec": map[string]any{
			"entryPoints": []any{"web"},
			"routes": []any{map[string]any{
				"match": match, "kind": "Rule", "services": service,
				"middlewares": []any{map[string]any{"name": ConsoleRedirect}},
			}},
		},
	}}
	return []*unstructured.Unstructured{secure, plain}
}

// EnsureConsole makes the Service, the EndpointSlice, the middleware and the
// routes of the console. The controller calls it each minute, thus a
// change by hand goes away.
func (c *Controller) EnsureConsole(ctx context.Context) error {
	addr, port, err := SplitUpstream(c.cfg.ConsoleUpstream)
	if err != nil {
		return err
	}
	if err := c.ensureConsoleService(ctx, BuildConsoleService(port)); err != nil {
		return fmt.Errorf("the service of the console: %w", err)
	}
	if err := c.ensureConsoleEndpoints(ctx, BuildConsoleEndpoints(addr, port)); err != nil {
		return fmt.Errorf("the endpoint of the console: %w", err)
	}
	if c.dyn == nil {
		return nil
	}
	if err := upsert(ctx, c.dyn.Resource(middlewareGVR).Namespace(IngressNamespace), BuildConsoleRedirect()); err != nil {
		return fmt.Errorf("the middleware of the console: %w", err)
	}
	for _, r := range BuildConsoleRoutes(c.cfg.Domain, port) {
		if err := upsert(ctx, c.dyn.Resource(routeGVR).Namespace(IngressNamespace), r); err != nil {
			return fmt.Errorf("the route %s: %w", r.GetName(), err)
		}
	}
	return nil
}

func (c *Controller) ensureConsoleService(ctx context.Context, want *corev1.Service) error {
	services := c.kube.CoreV1().Services(IngressNamespace)
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cur, err := services.Get(ctx, want.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			_, err = services.Create(ctx, want.DeepCopy(), metav1.CreateOptions{})
			return err
		}
		if err != nil {
			return err
		}
		cur.Labels = want.Labels
		cur.Spec.Ports = want.Spec.Ports
		cur.Spec.Selector = nil
		_, err = services.Update(ctx, cur, metav1.UpdateOptions{})
		return err
	})
}

func (c *Controller) ensureConsoleEndpoints(ctx context.Context, want *discoveryv1.EndpointSlice) error {
	slices := c.kube.DiscoveryV1().EndpointSlices(IngressNamespace)
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cur, err := slices.Get(ctx, want.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			_, err = slices.Create(ctx, want.DeepCopy(), metav1.CreateOptions{})
			return err
		}
		if err != nil {
			return err
		}
		cur.Labels, cur.AddressType, cur.Endpoints, cur.Ports = want.Labels, want.AddressType, want.Endpoints, want.Ports
		_, err = slices.Update(ctx, cur, metav1.UpdateOptions{})
		return err
	})
}
