package main

import (
	"net/mail"
	"strings"
)

// recipientAllowed reports whether addr's domain matches the -domain filter.
// An empty filter allows every recipient (the default).
//
// Uses LastIndex because the domain is whatever follows the final "@", and
// EqualFold because SMTP domains are case-insensitive (RFC 5321 §2.4).
func recipientAllowed(addr, domain string) bool {
	if domain == "" {
		return true
	}

	at := strings.LastIndex(addr, "@")

	return at >= 0 && strings.EqualFold(addr[at+1:], domain)
}

func transformStdAddressToEmailAddress(addr []*mail.Address) []*EmailAddress {
	ret := []*EmailAddress{}

	for _, e := range addr {
		ret = append(ret, &EmailAddress{
			Address: e.Address,
			Name:    e.Name,
		})
	}

	return ret
}
