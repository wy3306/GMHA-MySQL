package cli

import (
	"strings"
)

// ParseArgs 解析命令行参数，逻辑复用 ParseLine
func ParseArgs(args []string) (cmd string, clusterID string, flags map[string]string) {
	if len(args) == 0 {
		return "", "", nil
	}
	// 重组为类似 ParseLine 的输入
	line := strings.Join(args, " ")
	return ParseLine(line)
}

// parseFlags 从 tokens 中解析 --key=value 或 -key=value，返回 map，并返回剩余非 flag 的 tokens
func parseFlags(tokens []string) (flags map[string]string, rest []string) {
	flags = make(map[string]string)
	for _, t := range tokens {
		if strings.HasPrefix(t, "--") {
			kv := strings.TrimPrefix(t, "--")
			if idx := strings.Index(kv, "="); idx >= 0 {
				flags[kv[:idx]] = kv[idx+1:]
			}
		} else if strings.HasPrefix(t, "-") && len(t) > 1 && t[1] != '-' {
			kv := strings.TrimPrefix(t, "-")
			if idx := strings.Index(kv, "="); idx >= 0 {
				flags[kv[:idx]] = kv[idx+1:]
			}
		} else {
			rest = append(rest, t)
		}
	}
	return flags, rest
}

// ParseLine 解析一行命令，返回：命令类型、集群ID（若有）、剩余参数/flag
// 例如: "cluster add --id=cluster-a" -> cmd=cluster_add, clusterID="", flags 含 id
//       "cluster cluster-a host add --ip=1.2.3.4" -> cmd=host_add, clusterID=cluster-a, flags 含 ip
func ParseLine(line string) (cmd string, clusterID string, flags map[string]string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "", "", nil
	}
	tokens := splitQuoted(line)
	if len(tokens) == 0 {
		return "", "", nil
	}
	first := strings.ToLower(tokens[0])
	if first == "exit" || first == "quit" {
		return "exit", "", nil
	}
	if first == "help" {
		return "help", "", nil
	}
	if first != "cluster" {
		return "unknown", "", nil
	}
	if len(tokens) < 2 {
		return "cluster_help", "", nil
	}
	second := strings.ToLower(tokens[1])
	switch second {
	case "add":
		flags, _ = parseFlags(tokens[2:])
		return "cluster_add", "", flags
	case "list":
		return "cluster_list", "", nil
	case "show":
		if len(tokens) < 3 {
			return "cluster_show_need_id", "", nil
		}
		return "cluster_show", tokens[2], nil
	default:
		// second 当作 cluster_id
		clusterID = tokens[1]
		if len(tokens) < 3 {
			return "cluster_sub_need_cmd", clusterID, nil
		}
		third := strings.ToLower(tokens[2])
		switch third {
		case "host":
			if len(tokens) < 4 {
				return "host_need_add_list", clusterID, nil
			}
			fourth := strings.ToLower(tokens[3])
			if fourth == "add" {
				flags, _ = parseFlags(tokens[4:])
				return "host_add", clusterID, flags
			}
			if fourth == "list" {
				return "host_list", clusterID, nil
			}
			return "host_need_add_list", clusterID, nil
		case "instance":
			if len(tokens) < 4 {
				return "instance_need_add_list", clusterID, nil
			}
			fourth := strings.ToLower(tokens[3])
			if fourth == "add" {
				flags, _ = parseFlags(tokens[4:])
				return "instance_add", clusterID, flags
			}
			if fourth == "list" {
				return "instance_list", clusterID, nil
			}
			return "instance_need_add_list", clusterID, nil
		default:
			return "unknown_sub", clusterID, nil
		}
	}
}

// splitQuoted 按空格分割，但保留引号内的内容
func splitQuoted(s string) []string {
	var tokens []string
	var buf strings.Builder
	inQuote := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '"', '\'':
			inQuote = !inQuote
		case ' ', '\t':
			if !inQuote {
				if buf.Len() > 0 {
					tokens = append(tokens, buf.String())
					buf.Reset()
				}
			} else {
				buf.WriteByte(c)
			}
		default:
			buf.WriteByte(c)
		}
	}
	if buf.Len() > 0 {
		tokens = append(tokens, buf.String())
	}
	return tokens
}
