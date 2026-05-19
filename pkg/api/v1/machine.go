package v1

import "errors"

// OnboardMachineRequest 表示纳管机器的请求参数，包含机器名称、IP 地址、SSH 连接信息及所属集群。
type OnboardMachineRequest struct {
	Name           string `json:"name"`
	IP             string `json:"ip"`
	SSHPort        int    `json:"ssh_port"`
	SSHUser        string `json:"ssh_user"`
	SSHPassword    string `json:"ssh_password"`
	CredentialID   string `json:"credential_id"`
	CredentialName string `json:"credential_name"`
	Cluster        string `json:"cluster"`
}

// Validate 对纳管机器请求参数进行校验，确保必填字段不为空且端口号合法。
func (r OnboardMachineRequest) Validate() error {
	switch {
	case r.Name == "":
		return errors.New("name is required")
	case r.IP == "":
		return errors.New("ip is required")
	case r.SSHPort <= 0:
		return errors.New("ssh_port must be positive")
	case r.SSHUser == "" && r.CredentialID == "" && r.CredentialName == "":
		return errors.New("ssh_user is required")
	default:
		return nil
	}
}

// OnboardMachineResponse 表示纳管机器操作的响应结果，包含机器详细信息和状态。
type OnboardMachineResponse struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	IP           string `json:"ip"`
	SSHPort      int    `json:"ssh_port"`
	SSHUser      string `json:"ssh_user"`
	CredentialID string `json:"credential_id"`
	Cluster      string `json:"cluster"`
	Status       string `json:"status"`
	LastError    string `json:"last_error"`
}

// MachineItem 表示机器列表中的单个机器信息，包含连接信息、集群归属和运行状态。
type MachineItem struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	IP           string `json:"ip"`
	SSHPort      int    `json:"ssh_port"`
	SSHUser      string `json:"ssh_user"`
	CredentialID string `json:"credential_id"`
	Cluster      string `json:"cluster"`
	Status       string `json:"status"`
	LastError    string `json:"last_error"`
}

// ListMachinesResponse 表示机器列表的响应结果，包含机器信息列表。
type ListMachinesResponse struct {
	Items []MachineItem `json:"items"`
}
