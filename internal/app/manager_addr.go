// Manager 地址探测与解析工具函数。
package app

import (
	"fmt"
	"net"
	"net/url"
	"sort"
	"strings"
)

type managerLocalAddr struct {
	ip            net.IP
	network       *net.IPNet
	interfaceName string
}

// DetectManagerHost 自动检测 Manager 主机的 IPv4 地址。
// 优先采用系统默认路由实际选择的源地址，避免结果受网卡枚举顺序影响；没有
// 默认路由时再从活动网卡中选择，并把常见虚拟网卡排到物理网卡之后。
func DetectManagerHost() string {
	locals := managerLocalIPv4Addrs()
	return selectDetectedManagerHost(locals, routeSourceIPv4(net.ParseIP("1.1.1.1")))
}

func selectDetectedManagerHost(locals []managerLocalAddr, routed net.IP) string {
	// VPN 开启时系统默认路由常落到 utun/tun，作为 Agent 回连地址通常不可用。
	// 默认展示地址只接受物理网卡路由；针对具体 Agent 的地址解析仍保留路由优先。
	for _, local := range locals {
		if local.ip.Equal(routed) && !isVirtualInterface(local.interfaceName) {
			return local.ip.String()
		}
	}
	if len(locals) > 0 {
		return locals[0].ip.String()
	}
	return "127.0.0.1"
}

// DefaultManagerHTTPAddr 返回默认的 Manager HTTP 地址（http://<本机IP>:8080）。
func DefaultManagerHTTPAddr() string {
	return "http://" + net.JoinHostPort(DetectManagerHost(), "8080")
}

// BuildManagerHTTPAddr 根据主机和端口构建 Manager HTTP 地址。
func BuildManagerHTTPAddr(host string, port string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		host = DetectManagerHost()
	}
	port = strings.TrimPrefix(strings.TrimSpace(port), ":")
	if port == "" {
		port = "8080"
	}
	return fmt.Sprintf("http://%s:%s", host, port)
}

// BuildManagerGRPCAddr 根据主机和端口构建 Manager gRPC 地址（host:port 格式）。
func BuildManagerGRPCAddr(host string, port string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		host = DetectManagerHost()
	}
	port = strings.TrimPrefix(strings.TrimSpace(port), ":")
	if port == "" {
		port = "9100"
	}
	return net.JoinHostPort(host, port)
}

// NormalizeManagerGRPCAddr 规范化 Manager gRPC 地址。
// 若已配置则直接返回，否则从 HTTP 地址推导主机名，使用默认端口 9100。
func NormalizeManagerGRPCAddr(managerHTTPAddr, managerGRPCAddr string) string {
	managerGRPCAddr = strings.TrimSpace(managerGRPCAddr)
	if managerGRPCAddr != "" {
		return managerGRPCAddr
	}
	host := DetectManagerHost()
	raw := strings.TrimSpace(managerHTTPAddr)
	if raw != "" {
		if parsed, err := url.Parse(raw); err == nil && parsed.Host != "" {
			host = parsed.Hostname()
		}
	}
	return net.JoinHostPort(host, "9100")
}

// ResolveManagerGRPCAddrForTarget 为目标机器解析可达的 Manager gRPC 地址。
// 优先使用同子网的本机地址，确保目标机器上的 Agent 能访问到 Manager。
func ResolveManagerGRPCAddrForTarget(managerHTTPAddr, managerGRPCAddr, targetIP string) string {
	port := "9100"
	configuredHost := ""
	if host, parsedPort, err := net.SplitHostPort(strings.TrimSpace(managerGRPCAddr)); err == nil {
		configuredHost = host
		if strings.TrimSpace(parsedPort) != "" {
			port = parsedPort
		}
	}
	if configuredHost == "" {
		configuredHost, _ = SplitManagerGRPCAddr(NormalizeManagerGRPCAddr(managerHTTPAddr, managerGRPCAddr))
	}
	hosts := managerHostCandidates(targetIP, configuredHost)
	addrs := make([]string, 0, len(hosts))
	for _, host := range hosts {
		addrs = append(addrs, net.JoinHostPort(host, port))
	}
	return strings.Join(addrs, ",")
}

// ResolveManagerHTTPAddrForTarget 为目标机器解析可达的 Manager HTTP 地址。
// 优先使用同子网的本机地址。
func ResolveManagerHTTPAddrForTarget(managerHTTPAddr, targetIP string) string {
	host, port := SplitManagerHTTPAddr(managerHTTPAddr)
	hosts := managerHostCandidates(targetIP, host)
	addrs := make([]string, 0, len(hosts))
	for _, candidate := range hosts {
		addrs = append(addrs, BuildManagerHTTPAddr(candidate, port))
	}
	return strings.Join(addrs, ",")
}

// managerHostCandidates 返回给定目标可尝试的 Manager 地址。
//
// 只返回内核路由到目标时采用的源地址（或严格同子网地址），不再把所有活动
// 网卡都下发给 Agent。这样 Docker、VMware、VPN 等无关网卡不会意外成为
// Manager 地址。显式 DNS 名称作为容灾入口保留；显式 IP 仅在无法针对目标
// 选出本机地址时兜底。
func managerHostCandidates(targetIP, configuredHost string) []string {
	target := parseTargetIPv4(targetIP)
	locals := managerLocalIPv4Addrs()
	routed := routeSourceIPv4(target)
	return selectManagerHostCandidates(target, configuredHost, locals, routed)
}

func selectManagerHostCandidates(target net.IP, configuredHost string, locals []managerLocalAddr, routed net.IP) []string {
	seen := map[string]bool{}
	result := make([]string, 0, 2)
	add := func(host string) {
		host = strings.TrimSpace(host)
		if host != "" && !seen[host] {
			seen[host] = true
			result = append(result, host)
		}
	}
	selected := ""
	if target != nil && isLocalIPv4(routed, locals) {
		selected = routed.String()
	}
	if selected == "" && target != nil {
		for _, local := range locals {
			if local.network != nil && local.network.Contains(target) {
				selected = local.ip.String()
				break
			}
		}
	}
	add(selected)

	// DNS 名称可能指向浮动地址或经由外部 DNS 更新，保留为可靠的兜底入口。
	if configuredHost != "" && net.ParseIP(configuredHost) == nil {
		add(configuredHost)
	}
	// 未知目标或本机没有到目标的可用路由时，尊重显式配置。
	if selected == "" {
		add(configuredHost)
	}
	return result
}

func managerLocalIPv4Addrs() []managerLocalAddr {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	result := make([]managerLocalAddr, 0, 4)
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			ipnet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			if ip := ipnet.IP.To4(); ip != nil && !ip.IsLoopback() {
				value := ip.String()
				if !seen[value] {
					seen[value] = true
					result = append(result, managerLocalAddr{ip: append(net.IP(nil), ip...), network: ipnet, interfaceName: iface.Name})
				}
			}
		}
	}
	sort.SliceStable(result, func(i, j int) bool {
		iVirtual := isVirtualInterface(result[i].interfaceName)
		jVirtual := isVirtualInterface(result[j].interfaceName)
		if iVirtual != jVirtual {
			return !iVirtual
		}
		return result[i].ip.String() < result[j].ip.String()
	})
	return result
}

func parseTargetIPv4(value string) net.IP {
	value = strings.TrimSpace(value)
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	return net.ParseIP(value).To4()
}

// routeSourceIPv4 让内核路由表决定访问目标所使用的源地址。UDP Dial 不会发送
// 数据包，因此不会对目标机器产生探测流量。
func routeSourceIPv4(target net.IP) net.IP {
	if target == nil || target.To4() == nil {
		return nil
	}
	conn, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: target.To4(), Port: 9})
	if err != nil {
		return nil
	}
	defer conn.Close()
	addr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		return nil
	}
	return addr.IP.To4()
}

func isLocalIPv4(ip net.IP, locals []managerLocalAddr) bool {
	if ip == nil || ip.To4() == nil || ip.IsLoopback() || ip.IsUnspecified() {
		return false
	}
	for _, local := range locals {
		if local.ip.Equal(ip) {
			return true
		}
	}
	return false
}

func isVirtualInterface(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	virtualPrefixes := []string{
		"awdl", "br-", "bridge", "cni", "docker", "dummy", "feth", "flannel", "ham", "llw",
		"podman", "tap", "tailscale", "tun", "utun", "vboxnet", "veth", "virbr", "vmenet",
		"vmnet", "wg", "zt",
	}
	for _, prefix := range virtualPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// SplitManagerHTTPAddr 从 HTTP 地址中拆分出主机名和端口号。
func SplitManagerHTTPAddr(addr string) (string, string) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return DetectManagerHost(), "8080"
	}
	parsed, err := url.Parse(addr)
	if err != nil || parsed.Host == "" {
		return DetectManagerHost(), "8080"
	}
	host := parsed.Hostname()
	port := parsed.Port()
	if host == "" {
		host = DetectManagerHost()
	}
	if port == "" {
		port = "8080"
	}
	return host, port
}

// SplitManagerGRPCAddr 从 gRPC 地址中拆分出主机名和端口号。
func SplitManagerGRPCAddr(addr string) (string, string) {
	addr = strings.TrimSpace(addr)
	if host, port, err := net.SplitHostPort(addr); err == nil {
		if host == "" {
			host = DetectManagerHost()
		}
		if port == "" {
			port = "9100"
		}
		return host, port
	}
	return DetectManagerHost(), "9100"
}

// detectSameSubnetHost 查找与目标 IP 在同一子网的本机地址。
// 用于 Agent 安装时选择 Manager 的可达地址。
func detectSameSubnetHost(targetIP string) string {
	target := parseTargetIPv4(targetIP)
	if target == nil {
		return ""
	}
	locals := managerLocalIPv4Addrs()
	if routed := routeSourceIPv4(target); isLocalIPv4(routed, locals) {
		return routed.String()
	}
	for _, local := range locals {
		if local.network != nil && local.network.Contains(target) {
			return local.ip.String()
		}
	}
	return ""
}
