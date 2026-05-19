package collect

import (
	"context"
	"sort"
	"strings"

	collectdomain "gmha/internal/collect"
)

func collectNetwork(ctx context.Context) ([]string, []collectdomain.NetworkInterface, error) {
	out, err := runCommand(ctx, "ip", "addr")
	if err != nil {
		return nil, nil, err
	}
	lines := strings.Split(out, "\n")
	ipSet := make(map[string]struct{})
	ifaceIPs := make(map[string]map[string]struct{})
	var currentIface string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if !strings.HasPrefix(line, " ") && strings.Contains(line, ":") {
			parts := strings.SplitN(trimmed, ":", 3)
			if len(parts) >= 2 {
				name := normalizeInterfaceName(strings.TrimSpace(parts[1]))
				currentIface = ""
				if isCollectableInterface(name) {
					currentIface = name
					if _, ok := ifaceIPs[currentIface]; !ok {
						ifaceIPs[currentIface] = make(map[string]struct{})
					}
				}
			}
		}
		if currentIface != "" && strings.HasPrefix(trimmed, "inet ") {
			fields := strings.Fields(trimmed)
			if len(fields) >= 2 {
				addr := fields[1]
				ip := strings.SplitN(addr, "/", 2)[0]
				if ip != "" && ip != "127.0.0.1" {
					ipSet[ip] = struct{}{}
					ifaceIPs[currentIface][ip] = struct{}{}
				}
			}
		}
	}
	ifaces := make([]collectdomain.NetworkInterface, 0)
	for _, name := range sortedKeysMap(ifaceIPs) {
		ips := sortedKeys(ifaceIPs[name])
		if len(ips) == 0 {
			continue
		}
		ifaces = append(ifaces, collectdomain.NetworkInterface{
			Name: name,
			IPs:  ips,
		})
	}
	return sortedKeys(ipSet), ifaces, nil
}

func normalizeInterfaceName(name string) string {
	name = strings.TrimSpace(name)
	if idx := strings.Index(name, "@"); idx >= 0 {
		name = name[:idx]
	}
	return name
}

func isCollectableInterface(name string) bool {
	switch name {
	case "", "lo", "sit0", "tunl0", "ip6tnl0":
		return false
	}
	return true
}

func sortedKeysMap(set map[string]map[string]struct{}) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
