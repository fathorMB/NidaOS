package mdns

import (
	"net/netip"
	"testing"

	"golang.org/x/net/dns/dnsmessage"
)

func query(t *testing.T, id uint16, names ...string) []byte {
	t.Helper()
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: id})
	_ = b.StartQuestions()
	for _, n := range names {
		_ = b.Question(dnsmessage.Question{Name: dnsmessage.MustNewName(n), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET})
	}
	out, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func parseAnswers(t *testing.T, b []byte) (dnsmessage.Header, []dnsmessage.Resource, int) {
	t.Helper()
	var m dnsmessage.Message
	if err := m.Unpack(b); err != nil {
		t.Fatal(err)
	}
	return m.Header, m.Answers, len(m.Questions)
}

var names = map[string]bool{"blog.nidaos.local": true, "nidaos.local": true}
var addr = netip.MustParseAddr("192.168.1.20")

func TestAnswerForAKnownName(t *testing.T) {
	resp := Answer(query(t, 0, "Blog.NidaOS.local."), names, addr, false)
	if resp == nil {
		t.Fatal("no answer")
	}
	h, ans, nq := parseAnswers(t, resp)
	if !h.Response || !h.Authoritative || h.ID != 0 || nq != 0 || len(ans) != 1 {
		t.Fatalf("header %+v, %d questions, %d answers", h, nq, len(ans))
	}
	a := ans[0].Body.(*dnsmessage.AResource)
	if netip.AddrFrom4(a.A) != addr || ans[0].Header.TTL != TTL || ans[0].Header.Class&classCacheFlush == 0 {
		t.Errorf("answer %+v", ans[0])
	}
}

func TestNoAnswerForOtherNames(t *testing.T) {
	if Answer(query(t, 0, "printer.local."), names, addr, false) != nil {
		t.Error("the responder answers for a name that is not its name")
	}
	if Answer([]byte{1, 2, 3}, names, addr, false) != nil {
		t.Error("the responder answers a bad packet")
	}
	if Answer(query(t, 0, "blog.nidaos.local."), names, netip.MustParseAddr("fe80::1"), false) != nil {
		t.Error("the responder gives an IPv6 address in an A record")
	}
}

func TestLegacyUnicastAnswer(t *testing.T) {
	resp := Answer(query(t, 4242, "printer.local.", "blog.nidaos.local."), names, addr, true)
	h, ans, nq := parseAnswers(t, resp)
	if h.ID != 4242 || nq != 1 || len(ans) != 1 || ans[0].Header.Class&classCacheFlush != 0 {
		t.Errorf("header %+v, %d questions, answers %+v", h, nq, ans)
	}
	if a, ok := firstA(resp, dnsmessage.MustNewName("blog.nidaos.local.")); !ok || a != addr {
		t.Errorf("firstA: %v %v", a, ok)
	}
}

func TestAnnouncement(t *testing.T) {
	_, ans, _ := parseAnswers(t, Announcement([]string{"blog.nidaos.local", "shop.nidaos.local"}, addr))
	if len(ans) != 2 || ans[1].Header.Name.String() != "shop.nidaos.local." {
		t.Errorf("answers %+v", ans)
	}
}

func TestPodInterfaces(t *testing.T) {
	for _, n := range []string{"veth1a2b", "cni0", "flannel.1"} {
		if !podInterface(n) {
			t.Errorf("%s is a pod interface", n)
		}
	}
	for _, n := range []string{"eth0", "enp1s0", "wlan0"} {
		if podInterface(n) {
			t.Errorf("%s is not a pod interface", n)
		}
	}
}
