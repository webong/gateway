package integration

import (
	"encoding/csv"
	"net/netip"
	"os"
	"testing"
)

// The PHP MailNetworkPolicy test consumes this same fixture so the Planner's
// credential probe cannot silently disagree with Spinner's dial policy.
func TestEgressPolicyConformance(t *testing.T) {
	file, err := os.Open("testdata/egress_decisions.csv")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	records, err := csv.NewReader(file).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) < 2 || len(records[0]) != 3 || records[0][0] != "address" || records[0][1] != "public" || records[0][2] != "private" {
		t.Fatal("invalid egress conformance fixture")
	}
	seen := make(map[string]bool, len(records)-1)
	for _, record := range records[1:] {
		address, err := netip.ParseAddr(record[0])
		if err != nil || seen[record[0]] || (record[1] != "allow" && record[1] != "deny") || (record[2] != "allow" && record[2] != "deny") {
			t.Fatalf("invalid or duplicate egress fixture row %v", record)
		}
		seen[record[0]] = true
		if got, want := PublicEgressAddress(address), record[1] == "allow"; got != want {
			t.Errorf("public %s = %v, want %v", address, got, want)
		}
		if got, want := EgressAddressAllowed(address, true), record[2] == "allow"; got != want {
			t.Errorf("private %s = %v, want %v", address, got, want)
		}
	}
}
