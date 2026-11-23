package main

import (
	"net"
	"testing"
)

func TestIsTailnet(t *testing.T) {
	for _, tc := range []struct {
		ip   net.IP
		want bool
	}{
		{net.IPv4(100, 100, 1, 1), true},
		{net.IPv4(100, 127, 255, 254), true},
		{net.IPv4(100, 128, 0, 1), false},
		{net.IPv4(100, 63, 255, 255), false},
		{net.IPv4(10, 0, 0, 1), false},
		{net.IPv4(192, 168, 1, 1), false},
		{net.ParseIP("fd7a:115c:a1e0::1"), true},
		{net.ParseIP("fd7a:115c:a1e1::1"), false},
		{net.ParseIP("2001:db8::1"), false},
	} {
		if got := isTailnet(tc.ip); got != tc.want {
			t.Errorf("isTailnet(%v) = %v, want %v", tc.ip, got, tc.want)
		}
	}
}
