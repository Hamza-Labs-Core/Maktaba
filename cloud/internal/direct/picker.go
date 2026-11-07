package direct

import "sort"

// WeeklyBudget is how many new certificates a direct-name domain takes from Let's Encrypt in
// 7 days: 5 under its 50, for retries (story 50.15 §4).
const WeeklyBudget = 45

// CAs a home's certificate can come from: Let's Encrypt within the domains' weekly budgets,
// then ZeroSSL, then Google Trust Services (both with external account binding).
const (
	CALetsEncrypt = "letsencrypt"
	CAZeroSSL     = "zerossl"
	CAGoogle      = "google"
)

// DomainUse is one domain's last 7 days and its homes.
type DomainUse struct {
	Domain     string
	IssuedWeek int // new certificates in the last 7 days (renewals not counted)
	Homes      int
}

// Pick chooses a new home's domain and CA (story 50.15 §4): the configured domain with the most
// room left this week, ties to the one with the fewest homes, then the order configured. When
// every domain's budget is used, the domain with the fewest homes and the first overflow CA.
// A domain configured but not yet used has no row: it counts as empty.
func Pick(domains []string, use []DomainUse, budget int) (domain, ca string, overflow bool) {
	by := map[string]DomainUse{}
	for _, u := range use {
		by[u.Domain] = u
	}
	type cand struct {
		DomainUse
		order int
	}
	cs := make([]cand, 0, len(domains))
	for i, d := range domains {
		u := by[d]
		u.Domain = d
		cs = append(cs, cand{u, i})
	}
	if len(cs) == 0 {
		return "", "", false
	}
	sort.SliceStable(cs, func(i, j int) bool {
		ri, rj := budget-cs[i].IssuedWeek, budget-cs[j].IssuedWeek
		if ri != rj {
			return ri > rj
		}
		if cs[i].Homes != cs[j].Homes {
			return cs[i].Homes < cs[j].Homes
		}
		return cs[i].order < cs[j].order
	})
	if cs[0].IssuedWeek < budget {
		return cs[0].Domain, CALetsEncrypt, false
	}
	sort.SliceStable(cs, func(i, j int) bool {
		if cs[i].Homes != cs[j].Homes {
			return cs[i].Homes < cs[j].Homes
		}
		return cs[i].order < cs[j].order
	})
	return cs[0].Domain, CAZeroSSL, true
}

// NextCA is the overflow CA after ca failed ("" when none is left).
func NextCA(ca string) string {
	switch ca {
	case CALetsEncrypt:
		return CAZeroSSL
	case CAZeroSSL:
		return CAGoogle
	}
	return ""
}
