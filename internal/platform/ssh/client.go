// Package ssh 提供 SSH 客户端实现，用于远程执行命令和上传文件。
package ssh

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"path"
	"time"

	"gmha/internal/ports"
	gossh "golang.org/x/crypto/ssh"
)

// Client 是 SSH 客户端实现，满足 ports.SSHClient 接口。
type Client struct{}

// NewClient 创建一个新的 SSH 客户端实例。
func NewClient() *Client {
	return &Client{}
}

// Run 通过 SSH 连接到远程主机并执行指定命令，返回标准输出和标准错误。
func (c *Client) Run(ctx context.Context, input ports.RunInput) (ports.RunOutput, error) {
	client, err := dial(input.Address, input.Port, input.User, input.Password, time.Duration(input.Timeout)*time.Second)
	if err != nil {
		return ports.RunOutput{}, err
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return ports.RunOutput{}, err
	}
	defer session.Close()

	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr

	done := make(chan error, 1)
	go func() {
		done <- session.Run(input.Command)
	}()

	select {
	case <-ctx.Done():
		_ = session.Close()
		return ports.RunOutput{}, ctx.Err()
	case err := <-done:
		if err != nil {
			return ports.RunOutput{Stdout: stdout.String(), Stderr: stderr.String()}, fmt.Errorf("%w: %s", err, stderr.String())
		}
		return ports.RunOutput{Stdout: stdout.String(), Stderr: stderr.String()}, nil
	}
}

// Upload 通过 SSH 将文件内容上传到远程主机的指定路径，并设置文件权限。
func (c *Client) Upload(ctx context.Context, input ports.UploadInput) error {
	client, err := dial(input.Address, input.Port, input.User, input.Password, 15*time.Second)
	if err != nil {
		return err
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()

	stdin, err := session.StdinPipe()
	if err != nil {
		return err
	}

	errCh := make(chan error, 1)
	go func() {
		defer stdin.Close()
		_, writeErr := io.Copy(stdin, bytes.NewReader(input.Content))
		errCh <- writeErr
	}()

	cmd := fmt.Sprintf("mkdir -p %s && cat > %s && chmod %04o %s", shellQuote(path.Dir(input.RemotePath)), shellQuote(input.RemotePath), input.Permissions, shellQuote(input.RemotePath))
	if err := session.Start(cmd); err != nil {
		return err
	}

	select {
	case <-ctx.Done():
		_ = session.Close()
		return ctx.Err()
	case err := <-errCh:
		if err != nil {
			return err
		}
	}
	return session.Wait()
}

// dial 建立到远程主机的 SSH 连接，使用密码认证方式。
func dial(address string, port int, user, password string, timeout time.Duration) (*gossh.Client, error) {
	cfg := &gossh.ClientConfig{
		User:            user,
		Auth:            []gossh.AuthMethod{gossh.Password(password)},
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
		Timeout:         timeout,
	}
	return gossh.Dial("tcp", fmt.Sprintf("%s:%d", address, port), cfg)
}

// shellQuote 对字符串进行 Shell 引号转义，防止命令注入。
func shellQuote(s string) string {
	return "'" + bytes.NewBufferString(s).String() + "'"
}
