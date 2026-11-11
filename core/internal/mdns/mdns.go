// Package mdns announces the addresses of the apps on the home network
// with multicast DNS (decision D19).
//
// A device that looks for "blog.nidaos.local" sends a question to the
// group 224.0.0.251, port 5353. The responder answers with the IPv4
// address of the interface that got the question. The responder answers
// only for its names. Thus it can run together with avahi.
package mdns

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/dns/dnsmessage"
	"golang.org/x/net/ipv4"
)

// Port and group of multicast DNS (RFC 6762).
const (
	Port = 5353
	// TTL is the life of an answer in the caches of the devices.
	TTL = 120
)

var group = net.IPv4(224, 0, 0, 251)

// classCacheFlush is the bit of the class that tells the devices to
// replace the old records of the name (RFC 6762, section 10.2).
const classCacheFlush = 1 << 15

// Answer gives the response to a query, or nil when the query has no
// question for a name in names. addr is the address of the interface that
// got the query. unicast is true for a query from a port that is not 5353
// (a "legacy" query, RFC 6762, section 6.7): then the response has the ID
// and the questions of the query.
func Answer(query []byte, names map[string]bool, addr netip.Addr, unicast bool) []byte {
	var p dnsmessage.Parser
	h, err := p.Start(query)
	if err != nil || h.Response || h.OpCode != 0 {
		return nil
	}
	qs, err := p.AllQuestions()
	if err != nil {
		return nil
	}
	var answers []dnsmessage.Question
	for _, q := range qs {
		name := strings.ToLower(strings.TrimSuffix(q.Name.String(), "."))
		if !names[name] {
			continue
		}
		if q.Type == dnsmessage.TypeA || q.Type == dnsmessage.TypeALL {
			answers = append(answers, q)
		}
	}
	if len(answers) == 0 || !addr.Is4() {
		return nil
	}
	rh := dnsmessage.Header{Response: true, Authoritative: true}
	if unicast {
		rh.ID = h.ID
	}
	b := dnsmessage.NewBuilder(nil, rh)
	b.EnableCompression()
	if unicast {
		_ = b.StartQuestions()
		for _, q := range answers {
			_ = b.Question(dnsmessage.Question{Name: q.Name, Type: q.Type, Class: dnsmessage.ClassINET})
		}
	}
	if err := b.StartAnswers(); err != nil {
		return nil
	}
	class := dnsmessage.ClassINET
	if !unicast {
		class |= classCacheFlush
	}
	for _, q := range answers {
		rr := dnsmessage.ResourceHeader{Name: q.Name, Class: class, TTL: TTL}
		if err := b.AResource(rr, dnsmessage.AResource{A: addr.As4()}); err != nil {
			return nil
		}
	}
	out, err := b.Finish()
	if err != nil {
		return nil
	}
	return out
}

// Announcement gives an unsolicited response for names (RFC 6762, section
// 8.3). The responder sends it when a name is new.
func Announcement(names []string, addr netip.Addr) []byte {
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{Response: true, Authoritative: true})
	b.EnableCompression()
	if err := b.StartAnswers(); err != nil {
		return nil
	}
	for _, n := range names {
		name, err := dnsmessage.NewName(n + ".")
		if err != nil {
			continue
		}
		rr := dnsmessage.ResourceHeader{Name: name, Class: dnsmessage.ClassINET | classCacheFlush, TTL: TTL}
		_ = b.AResource(rr, dnsmessage.AResource{A: addr.As4()})
	}
	out, _ := b.Finish()
	return out
}

// Responder answers the queries for its names.
type Responder struct {
	// Names gives the current names, for example "blog.nidaos.local".
	Names  func() []string
	Logger *slog.Logger
	// Refresh is the time between two reads of the names and of the
	// interfaces. The default is 5 seconds.
	Refresh time.Duration

	mu    sync.Mutex
	names map[string]bool
}

func (r *Responder) current() map[string]bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.names
}

// Run answers until ctx stops.
func (r *Responder) Run(ctx context.Context) error {
	if r.Logger == nil {
		r.Logger = slog.New(slog.DiscardHandler)
	}
	if r.Refresh <= 0 {
		r.Refresh = 5 * time.Second
	}
	lc := net.ListenConfig{Control: reuse}
	c, err := lc.ListenPacket(ctx, "udp4", "0.0.0.0:5353")
	if err != nil {
		return err
	}
	pc := ipv4.NewPacketConn(c)
	if err := pc.SetControlMessage(ipv4.FlagInterface|ipv4.FlagDst, true); err != nil {
		c.Close()
		return err
	}
	_ = pc.SetMulticastLoopback(true)
	_ = pc.SetMulticastTTL(255)
	go func() {
		<-ctx.Done()
		c.Close()
	}()

	joined := map[int]bool{}
	var known map[string]bool
	refresh := func() {
		for _, ifi := range usableInterfaces() {
			if !joined[ifi.Index] {
				if err := pc.JoinGroup(&ifi, &net.UDPAddr{IP: group}); err == nil {
					joined[ifi.Index] = true
					r.Logger.Debug("mdns: the responder listens", "interface", ifi.Name)
				}
			}
		}
		next := map[string]bool{}
		var added []string
		for _, n := range r.Names() {
			n = strings.ToLower(n)
			next[n] = true
			if !known[n] {
				added = append(added, n)
			}
		}
		r.mu.Lock()
		r.names = next
		r.mu.Unlock()
		known = next
		if len(added) > 0 {
			r.announce(pc, added)
		}
	}
	refresh()
	go func() {
		t := time.NewTicker(r.Refresh)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				refresh()
			}
		}
	}()

	buf := make([]byte, 9000)
	for {
		n, cm, src, err := pc.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			continue
		}
		if cm == nil {
			continue
		}
		addr, ok := interfaceAddr(cm.IfIndex)
		if !ok {
			continue
		}
		from, _ := src.(*net.UDPAddr)
		if from == nil {
			continue
		}
		unicast := from.Port != Port
		resp := Answer(buf[:n], r.current(), addr, unicast)
		if resp == nil {
			continue
		}
		dst := &net.UDPAddr{IP: group, Port: Port}
		if unicast {
			dst = from
		}
		ifi, _ := net.InterfaceByIndex(cm.IfIndex)
		_, _ = pc.WriteTo(resp, &ipv4.ControlMessage{IfIndex: ifiIndex(ifi)}, dst)
	}
}

func ifiIndex(ifi *net.Interface) int {
	if ifi == nil {
		return 0
	}
	return ifi.Index
}

// announce sends the new names two times, one second apart, on each
// interface (RFC 6762, section 8.3).
func (r *Responder) announce(pc *ipv4.PacketConn, names []string) {
	r.Logger.Info("mdns: the responder announces names", "names", names)
	send := func() {
		for _, ifi := range usableInterfaces() {
			addr, ok := interfaceAddr(ifi.Index)
			if !ok {
				continue
			}
			msg := Announcement(names, addr)
			_, _ = pc.WriteTo(msg, &ipv4.ControlMessage{IfIndex: ifi.Index}, &net.UDPAddr{IP: group, Port: Port})
		}
	}
	send()
	time.AfterFunc(time.Second, send)
}

// usableInterfaces gives the interfaces of the home network: up, with
// multicast, not the loopback, and not an interface of the pods.
func usableInterfaces() []net.Interface {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []net.Interface
	for _, ifi := range ifs {
		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagMulticast == 0 || ifi.Flags&net.FlagLoopback != 0 {
			continue
		}
		if podInterface(ifi.Name) {
			continue
		}
		out = append(out, ifi)
	}
	return out
}

// podInterface tells if the interface is a part of the pod network of k3s
// or of a container engine.
func podInterface(name string) bool {
	for _, p := range []string{"veth", "cni", "flannel", "docker", "podman", "virbr", "kube-"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// interfaceAddr gives the first IPv4 address of an interface.
func interfaceAddr(index int) (netip.Addr, bool) {
	ifi, err := net.InterfaceByIndex(index)
	if err != nil || podInterface(ifi.Name) {
		return netip.Addr{}, false
	}
	addrs, err := ifi.Addrs()
	if err != nil {
		return netip.Addr{}, false
	}
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		if ip, ok := netip.AddrFromSlice(ipn.IP.To4()); ok && ip.Is4() && !ip.IsLinkLocalUnicast() {
			return ip, true
		}
	}
	return netip.Addr{}, false
}

// Lookup sends a query for name and gives the first address in the
// answers. It sends from a random port, thus the responders answer to it
// directly. The command "nida-core mdns-lookup" uses it for a check.
func Lookup(ctx context.Context, name string, timeout time.Duration) (netip.Addr, error) {
	qname, err := dnsmessage.NewName(strings.TrimSuffix(name, ".") + ".")
	if err != nil {
		return netip.Addr{}, err
	}
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: uint16(time.Now().UnixNano())})
	_ = b.StartQuestions()
	_ = b.Question(dnsmessage.Question{Name: qname, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET})
	msg, err := b.Finish()
	if err != nil {
		return netip.Addr{}, err
	}
	c, err := net.ListenUDP("udp4", &net.UDPAddr{})
	if err != nil {
		return netip.Addr{}, err
	}
	defer c.Close()
	deadline := time.Now().Add(timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = c.SetDeadline(deadline)
	if _, err := c.WriteTo(msg, &net.UDPAddr{IP: group, Port: Port}); err != nil {
		return netip.Addr{}, err
	}
	buf := make([]byte, 9000)
	for {
		n, _, err := c.ReadFrom(buf)
		if err != nil {
			return netip.Addr{}, err
		}
		if a, ok := firstA(buf[:n], qname); ok {
			return a, nil
		}
	}
}

func firstA(resp []byte, name dnsmessage.Name) (netip.Addr, bool) {
	var p dnsmessage.Parser
	if _, err := p.Start(resp); err != nil {
		return netip.Addr{}, false
	}
	_ = p.SkipAllQuestions()
	for {
		h, err := p.AnswerHeader()
		if err != nil {
			return netip.Addr{}, false
		}
		if h.Type == dnsmessage.TypeA && strings.EqualFold(h.Name.String(), name.String()) {
			a, err := p.AResource()
			if err != nil {
				return netip.Addr{}, false
			}
			return netip.AddrFrom4(a.A), true
		}
		if err := p.SkipAnswer(); err != nil {
			return netip.Addr{}, false
		}
	}
}
