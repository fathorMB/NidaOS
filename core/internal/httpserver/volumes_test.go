package httpserver

import "testing"

func TestVolumeUser(t *testing.T) {
	cases := map[string][2]string{
		"pvc-1a2b_nida-app-blog_data":     {"app", "blog"},
		"pvc-9z_nida-db-shop-db_pg-data":   {"database", "shop-db"},
		"pvc-3c_kube-system_registry":      {"system", "registry"},
		"pvc-4d_other_claim_with_underbar": {"other", "other/claim_with_underbar"},
		"lost+found":                       {"other", ""},
	}
	for name, want := range cases {
		k, u := VolumeUser(name)
		if k != want[0] || u != want[1] {
			t.Errorf("%s: %s %s, want %v", name, k, u, want)
		}
	}
}
