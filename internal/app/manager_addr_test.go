package app

import (
	"net"
	"reflect"
	"testing"
)

func TestSelectManagerHostCandidatesUsesTargetRouteOnly(t *testing.T) {
	locals := []managerLocalAddr{
		localAddr("192.168.31.10", 24, "en0"),
		localAddr("192.168.139.1", 24, "vmnet8"),
		localAddr("172.17.0.1", 16, "docker0"),
	}

	got := selectManagerHostCandidates(
		net.ParseIP("192.168.31.88").To4(),
		"192.168.139.1",
		locals,
		net.ParseIP("192.168.31.10").To4(),
	)
	want := []string{"192.168.31.10"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("candidates = %v, want %v", got, want)
	}
}

func TestSelectManagerHostCandidatesFallsBackToSameSubnet(t *testing.T) {
	locals := []managerLocalAddr{
		localAddr("192.168.31.10", 24, "en0"),
		localAddr("192.168.139.1", 24, "vmnet8"),
	}

	got := selectManagerHostCandidates(
		net.ParseIP("192.168.31.88").To4(),
		"192.168.139.1",
		locals,
		nil,
	)
	want := []string{"192.168.31.10"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("candidates = %v, want %v", got, want)
	}
}

func TestSelectManagerHostCandidatesKeepsDNSFallback(t *testing.T) {
	locals := []managerLocalAddr{localAddr("10.20.30.4", 24, "eth0")}

	got := selectManagerHostCandidates(
		net.ParseIP("10.20.30.99").To4(),
		"manager.internal.example",
		locals,
		net.ParseIP("10.20.30.4").To4(),
	)
	want := []string{"10.20.30.4", "manager.internal.example"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("candidates = %v, want %v", got, want)
	}
}

func TestSelectManagerHostCandidatesUsesConfiguredIPWhenTargetUnknown(t *testing.T) {
	locals := []managerLocalAddr{localAddr("192.168.31.10", 24, "en0")}

	got := selectManagerHostCandidates(nil, "10.0.0.50", locals, nil)
	want := []string{"10.0.0.50"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("candidates = %v, want %v", got, want)
	}
}

func TestVirtualInterfacesSortAfterPhysicalInterfaces(t *testing.T) {
	for _, name := range []string{"docker0", "veth123", "vmnet8", "vmenet0", "feth3041", "bridge100", "utun3", "virbr0", "tailscale0"} {
		if !isVirtualInterface(name) {
			t.Errorf("expected %q to be treated as virtual", name)
		}
	}
	for _, name := range []string{"en0", "eth0", "ens192", "wlan0"} {
		if isVirtualInterface(name) {
			t.Errorf("expected %q to be treated as physical", name)
		}
	}
}

func TestDetectedManagerHostDoesNotAdvertiseVPNDefaultRoute(t *testing.T) {
	locals := []managerLocalAddr{
		localAddr("192.168.31.59", 24, "en0"),
		localAddr("198.18.0.1", 15, "utun1024"),
	}
	got := selectDetectedManagerHost(locals, net.ParseIP("198.18.0.1").To4())
	if got != "192.168.31.59" {
		t.Fatalf("detected host = %q, want physical LAN address", got)
	}
}

func localAddr(ip string, prefix int, interfaceName string) managerLocalAddr {
	parsed := net.ParseIP(ip).To4()
	mask := net.CIDRMask(prefix, 32)
	return managerLocalAddr{
		ip:            parsed,
		network:       &net.IPNet{IP: parsed.Mask(mask), Mask: mask},
		interfaceName: interfaceName,
	}
}
