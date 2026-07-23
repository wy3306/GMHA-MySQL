package app

import (
	"testing"

	collectdomain "gmha/internal/collect"
)

func TestRankManagerNetworkInterfacesPrefersVIPSubnet(t *testing.T) {
	items, recommended := rankManagerNetworkInterfaces([]collectdomain.NetworkInterface{
		{Name: "eth0", IPs: []string{"172.16.0.12"}},
		{Name: "bond0", IPs: []string{"10.20.30.12"}},
		{Name: "lo", IPs: []string{"127.0.0.1"}},
	}, "172.16.0.12", "10.20.30.100", 24)
	if recommended != "bond0" {
		t.Fatalf("recommended = %q, want bond0", recommended)
	}
	if len(items) != 2 || !items[0].Recommended || items[0].Reason != "与 Manager VIP 位于同一网段" {
		t.Fatalf("unexpected ranked interfaces: %+v", items)
	}
}

func TestRankManagerNetworkInterfacesFallsBackToManagementAddress(t *testing.T) {
	_, recommended := rankManagerNetworkInterfaces([]collectdomain.NetworkInterface{
		{Name: "eth1", IPs: []string{"192.168.50.12"}},
		{Name: "eth0", IPs: []string{"10.0.0.12"}},
	}, "192.168.50.12", "", 24)
	if recommended != "eth1" {
		t.Fatalf("recommended = %q, want eth1", recommended)
	}
}

func TestManagerSameSubnetSupportsIPv4AndIPv6(t *testing.T) {
	if !managerSameSubnet("10.1.2.3", "10.1.2.200", 24) {
		t.Fatal("expected IPv4 addresses to share /24")
	}
	if managerSameSubnet("10.1.2.3", "10.1.3.3", 24) {
		t.Fatal("unexpected IPv4 subnet match")
	}
	if !managerSameSubnet("2001:db8::1", "2001:db8::100", 64) {
		t.Fatal("expected IPv6 addresses to share /64")
	}
}
