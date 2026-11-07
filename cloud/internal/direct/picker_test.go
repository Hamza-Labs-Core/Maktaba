package direct

import "testing"

// T11: the domain with the most room; the overflow CA when all are full; a new domain is used.
func TestTheDomainPicker(t *testing.T) {
	domains := []string{"direct-a.example", "direct-b.example", "direct-c.example", "direct-d.example"}
	use := []DomainUse{{"direct-a.example", 45, 300}, {"direct-b.example", 10, 120}, {"direct-c.example", 0, 400}, {"direct-d.example", 30, 50}}
	if d, ca, over := Pick(domains, use, WeeklyBudget); d != "direct-c.example" || ca != CALetsEncrypt || over {
		t.Fatalf("most room: %s %s %v", d, ca, over)
	}
	// a tie on room goes to the fewest homes
	tie := []DomainUse{{"direct-a.example", 10, 300}, {"direct-b.example", 10, 120}}
	if d, _, _ := Pick(domains[:2], tie, WeeklyBudget); d != "direct-b.example" {
		t.Fatalf("tie: %s", d)
	}
	full := []DomainUse{{"direct-a.example", 45, 300}, {"direct-b.example", 46, 120}, {"direct-c.example", 45, 400}, {"direct-d.example", 50, 50}}
	if d, ca, over := Pick(domains, full, WeeklyBudget); !over || ca != CAZeroSSL || d != "direct-d.example" {
		t.Fatalf("all full: %s %s %v", d, ca, over)
	}
	// the owner appends a fifth domain: it has no row yet and room for the week
	if d, ca, over := Pick(append(domains, "direct-e.example"), full, WeeklyBudget); d != "direct-e.example" || ca != CALetsEncrypt || over {
		t.Fatalf("a new domain: %s %s %v", d, ca, over)
	}
	if NextCA(CALetsEncrypt) != CAZeroSSL || NextCA(CAZeroSSL) != CAGoogle || NextCA(CAGoogle) != "" {
		t.Fatal("the overflow order")
	}
	if d, _, _ := Pick(nil, nil, WeeklyBudget); d != "" {
		t.Fatal("no domain configured")
	}
}
