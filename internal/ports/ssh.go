package ports

import "context"

// SSHClient 是 SSH 客户端接口，定义了远程命令执行和文件上传操作。
type SSHClient interface {
	Run(ctx context.Context, input RunInput) (RunOutput, error)
	Upload(ctx context.Context, input UploadInput) error
}

// RunInput 是远程命令执行的输入参数，包含连接信息和要执行的命令。
type RunInput struct {
	Address  string
	Port     int
	User     string
	Password string
	Command  string
	Timeout  int
}

// RunOutput 是远程命令执行的输出结果，包含标准输出和标准错误。
type RunOutput struct {
	Stdout string
	Stderr string
}

// UploadInput 是文件上传的输入参数，包含连接信息、远程路径和文件内容。
type UploadInput struct {
	Address     string
	Port        int
	User        string
	Password    string
	RemotePath  string
	Permissions int
	Content     []byte
}
