package main

import "testing"

func TestParseDeveloperID(t *testing.T) {
	one := `  1) 0123456789ABCDEF0123456789ABCDEF01234567 "Apple Development: A Person (XYZ9876543)"
  2) 89ABCDEF0123456789ABCDEF0123456789ABCDEF "Developer ID Application: Example Ltd (ABCDE12345)"
  3) 89ABCDEF0123456789ABCDEF0123456789ABCDEF "Developer ID Application: Example Ltd (ABCDE12345)"
     3 valid identities found
`
	id := parseDeveloperID(one)
	if id != "Developer ID Application: Example Ltd (ABCDE12345)" {
		t.Errorf("got %q", id)
	}
	if team := identityTeam(id); team != "ABCDE12345" {
		t.Errorf("team %q", team)
	}
	two := one + `  4) FEDCBA "Developer ID Application: Other (ZZZZZ99999)"` + "\n"
	if id := parseDeveloperID(two); id != "" {
		t.Errorf("two identities: got %q", id)
	}
	if id := parseDeveloperID("     0 valid identities found\n"); id != "" {
		t.Errorf("none: got %q", id)
	}
}
