package main

import "testing"

func TestRecipientAllowed(t *testing.T) {
	cases := []struct {
		addr, domain string
		want         bool
	}{
		// empty filter is the default: allow everything
		{"anyone@anywhere.tld", "", true},

		{"user@example.com", "example.com", true},
		{"user@other.com", "example.com", false},

		// subdomains are a different domain, not a match
		{"user@mail.example.com", "example.com", false},

		// RFC 5321 domains are case-insensitive
		{"user@EXAMPLE.COM", "example.com", true},
		{"user@example.com", "EXAMPLE.COM", true},

		// domain is what follows the LAST "@", not the first
		{"weird@name@example.com", "example.com", true},

		// no domain part at all must not be allowed through
		{"nodomain", "example.com", false},
		{"", "example.com", false},

		// must not match on a suffix/prefix of the configured domain
		{"user@notexample.com", "example.com", false},
		{"user@example.com.evil.tld", "example.com", false},
	}

	for _, c := range cases {
		if got := recipientAllowed(c.addr, c.domain); got != c.want {
			t.Errorf("recipientAllowed(%q, %q) = %v, want %v", c.addr, c.domain, got, c.want)
		}
	}
}
