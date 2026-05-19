// Package ssh 提供 SSH 基础设施，包括 SSH 客户端连接、命令执行、文件上传、
// 免密信任配置和 Agent 恢复执行等功能。
package ssh

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	machinedomain "gmha/internal/domain/machine"
	gossh "golang.org/x/crypto/ssh"
)

// Client 是 SSH 客户端，提供连接测试、命令执行和文件上传功能。
type Client struct{}

func NewClient() *Client {
	return &Client{}
}

func (c *Client) CheckTrustConnection(ctx context.Context, endpoint machinedomain.Endpoint, user string) (bool, error) {
	signers, err := loadLocalSigners()
	if err != nil {
		return false, err
	}
	if len(signers) == 0 {
		return false, nil
	}

	client, err := dialWithAuthMethods(ctx, endpoint, user, []gossh.AuthMethod{gossh.PublicKeys(signers...)}, 10*time.Second)
	if err != nil {
		return false, nil
	}
	defer client.Close()
	return true, nil
}

func (c *Client) TestConnection(ctx context.Context, endpoint machinedomain.Endpoint, auth machinedomain.SSHAuth) error {
	client, err := dial(ctx, endpoint, auth, 10*time.Second)
	if err != nil {
		return err
	}
	defer client.Close()
	return nil
}

func (c *Client) Run(ctx context.Context, endpoint machinedomain.Endpoint, auth machinedomain.SSHAuth, command string) error {
	_, err := c.RunOutput(ctx, endpoint, auth, command)
	return err
}

func (c *Client) RunOutput(ctx context.Context, endpoint machinedomain.Endpoint, auth machinedomain.SSHAuth, command string) ([]byte, error) {
	client, err := dial(ctx, endpoint, auth, 30*time.Second)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		return nil, err
	}
	defer session.Close()
	type result struct {
		output []byte
		err    error
	}
	done := make(chan result, 1)
	go func() {
		out, runErr := session.CombinedOutput(command)
		done <- result{output: out, err: runErr}
	}()
	select {
	case <-ctx.Done():
		_ = session.Close()
		return nil, ctx.Err()
	case out := <-done:
		return out.output, out.err
	}
}

func (c *Client) Upload(ctx context.Context, endpoint machinedomain.Endpoint, auth machinedomain.SSHAuth, remotePath string, content []byte, perm string) error {
	client, err := dial(ctx, endpoint, auth, 30*time.Second)
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
	done := make(chan error, 1)
	go func() {
		defer stdin.Close()
		_, copyErr := io.Copy(stdin, bytes.NewReader(content))
		done <- copyErr
	}()
	cmd := fmt.Sprintf("mkdir -p %s && cat > %s && chmod %s %s", shellQuote(filepath.Dir(remotePath)), shellQuote(remotePath), perm, shellQuote(remotePath))
	if err := session.Start(cmd); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		_ = session.Close()
		return ctx.Err()
	case err := <-done:
		if err != nil {
			return err
		}
	}
	return session.Wait()
}

func dial(ctx context.Context, endpoint machinedomain.Endpoint, auth machinedomain.SSHAuth, timeout time.Duration) (*gossh.Client, error) {
	methods := make([]gossh.AuthMethod, 0, 2)
	if auth.Password != "" {
		methods = append(methods, gossh.Password(auth.Password))
	}
	signers, err := loadLocalSigners()
	if err != nil {
		return nil, err
	}
	if len(signers) > 0 {
		methods = append(methods, gossh.PublicKeys(signers...))
	}
	if len(methods) == 0 {
		return nil, errors.New("no ssh auth method available")
	}
	return dialWithAuthMethods(ctx, endpoint, auth.User, methods, timeout)
}

func dialWithAuthMethods(ctx context.Context, endpoint machinedomain.Endpoint, user string, methods []gossh.AuthMethod, timeout time.Duration) (*gossh.Client, error) {
	cfg := &gossh.ClientConfig{
		User:            user,
		Auth:            methods,
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
		Timeout:         timeout,
	}

	type result struct {
		client *gossh.Client
		err    error
	}
	done := make(chan result, 1)
	go func() {
		client, err := gossh.Dial("tcp", fmt.Sprintf("%s:%d", endpoint.IP, endpoint.SSHPort), cfg)
		done <- result{client: client, err: err}
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case out := <-done:
		return out.client, out.err
	}
}

func loadLocalSigners() ([]gossh.Signer, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	candidates := []string{
		filepath.Join(home, ".ssh", "id_ed25519"),
		filepath.Join(home, ".ssh", "id_rsa"),
	}
	var signers []gossh.Signer
	for _, candidate := range candidates {
		data, err := os.ReadFile(candidate)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		}
		signer, err := gossh.ParsePrivateKey(data)
		if err != nil {
			return nil, err
		}
		signers = append(signers, signer)
	}
	return signers, nil
}
