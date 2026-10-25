package networkpolicy

import (
	"net/netip"
	"testing"
)

func TestParsePrivatePrefixes(t *testing.T) {
	for _, invalid := range []string{
		"127.0.0.0/8",
		"0.0.0.0/0",
		"10.0.0.0/7",
		"10.42.1.1/24",
		"169.254.0.0/16",
		"172.18.1.1/16",
	} {
		if _, err := ParsePrivatePrefixes(invalid); err == nil {
			t.Fatalf("accepted unsafe private network ceiling %q", invalid)
		}
	}

	got, err := ParsePrivatePrefixes("10.42.0.0/16,fd00::/64")
	if err != nil || len(got) != 2 || got[0] != netip.MustParsePrefix("10.42.0.0/16") {
		t.Fatalf("private network ceiling: %v %v", got, err)
	}
	if got, err := ParsePrivatePrefixes(""); err != nil || got != nil {
		t.Fatalf("empty private network ceiling: %v %v", got, err)
	}
}
