// Manager 地址探测与解析工具函数。
package app

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// DetectManagerHost 自动检测 Manager 主机的 IPv4 地址。
// 遍历所有非回环的活跃网卡，返回第一个 IPv4 地址；未找到时回退到 127.0.0.1。
func DetectManagerHost() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "127.0.0.1"
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.IsLoopback() {
				continue
			}
			if v4 := ip.To4(); v4 != nil {
				return v4.String()
			}
		}
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
	if host, parsedPort, err := net.SplitHostPort(strings.TrimSpace(managerGRPCAddr)); err == nil {
		if strings.TrimSpace(parsedPort) != "" {
			port = parsedPort
		}
		if sameSubnetHost := detectSameSubnetHost(targetIP); sameSubnetHost != "" {
			return net.JoinHostPort(sameSubnetHost, port)
		}
		if strings.TrimSpace(host) != "" {
			return managerGRPCAddr
		}
	}
	if sameSubnetHost := detectSameSubnetHost(targetIP); sameSubnetHost != "" {
		return net.JoinHostPort(sameSubnetHost, port)
	}
	return NormalizeManagerGRPCAddr(managerHTTPAddr, managerGRPCAddr)
}

// ResolveManagerHTTPAddrForTarget 为目标机器解析可达的 Manager HTTP 地址。
// 优先使用同子网的本机地址。
func ResolveManagerHTTPAddrForTarget(managerHTTPAddr, targetIP string) string {
	host, port := SplitManagerHTTPAddr(managerHTTPAddr)
	if sameSubnetHost := detectSameSubnetHost(targetIP); sameSubnetHost != "" {
		return BuildManagerHTTPAddr(sameSubnetHost, port)
	}
	return BuildManagerHTTPAddr(host, port)
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
	target := net.ParseIP(strings.TrimSpace(targetIP))
	if target == nil {
		return ""
	}
	target = target.To4()
	if target == nil {
		return ""
	}
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipnet, ok := addr.(*net.IPNet)
			if !ok || ipnet == nil {
				continue
			}
			local := ipnet.IP.To4()
			if local == nil || local.IsLoopback() {
				continue
			}
			if ipnet.Contains(target) {
				return local.String()
			}
		}
	}
	return ""
}
