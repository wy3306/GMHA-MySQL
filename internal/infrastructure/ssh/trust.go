package ssh

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	machinedomain "gmha/internal/domain/machine"
	gossh "golang.org/x/crypto/ssh"
)

// TrustService 负责 SSH 免密信任配置，将 Manager 的公钥分发到被纳管机器。
type TrustService struct {
	managerPublicKey string
	client           *Client
	keyPath          string
}

func NewTrustService(managerPublicKeyPath string, client *Client) (*TrustService, error) {
	keyPath, key, err := discoverPublicKey(managerPublicKeyPath)
	if err != nil {
		return nil, err
	}
	return &TrustService{
		managerPublicKey: key,
		client:           client,
		keyPath:          keyPath,
	}, nil
}

func (s *TrustService) SetupPasswordless(ctx context.Context, endpoint machinedomain.Endpoint, auth machinedomain.SSHAuth) error {
	if strings.TrimSpace(s.managerPublicKey) == "" {
		if s.keyPath != "" {
			return fmt.Errorf("manager SSH public key not found, please generate one or pass --manager-pubkey, tried: %s", s.keyPath)
		}
		return fmt.Errorf("manager SSH public key not found, please generate one or pass --manager-pubkey")
	}
	sshClient, err := dial(ctx, endpoint, auth, 15*time.Second)
	if err != nil {
		return err
	}
	defer sshClient.Close()

	if err := s.ensureRemoteSSHReady(ctx, sshClient); err != nil {
		return err
	}

	session, err := sshClient.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()

	cmd := strings.Join([]string{
		"set -eu",
		"mkdir -p ~/.ssh",
		"chmod 700 ~/.ssh",
		"touch ~/.ssh/authorized_keys",
		"chmod 600 ~/.ssh/authorized_keys",
		fmt.Sprintf("grep -Fqx %s ~/.ssh/authorized_keys || printf '%%s\\n' %s >> ~/.ssh/authorized_keys", shellQuote(s.managerPublicKey), shellQuote(s.managerPublicKey)),
	}, " && ")

	done := make(chan error, 1)
	go func() {
		done <- session.Run(cmd)
	}()

	select {
	case <-ctx.Done():
		_ = session.Close()
		return ctx.Err()
	case err := <-done:
		return err
	}
}

func (s *TrustService) ensureRemoteSSHReady(ctx context.Context, sshClient sessionFactory) error {
	session, err := sshClient.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()

	cmd := strings.Join([]string{
		"set -eu",
		`need_install=0`,
		`if ! command -v ssh >/dev/null 2>&1; then need_install=1; fi`,
		`if ! command -v ssh-keygen >/dev/null 2>&1; then need_install=1; fi`,
		`if ! command -v sshd >/dev/null 2>&1 && [ ! -x /usr/sbin/sshd ] && [ ! -x /sbin/sshd ]; then need_install=1; fi`,
		`if [ "$need_install" -eq 1 ]; then`,
		`  if command -v apt-get >/dev/null 2>&1; then`,
		`    DEBIAN_FRONTEND=noninteractive apt-get update`,
		`    DEBIAN_FRONTEND=noninteractive apt-get install -y openssh-client openssh-server`,
		`  elif command -v dnf >/dev/null 2>&1; then`,
		`    dnf install -y openssh-clients openssh-server`,
		`  elif command -v yum >/dev/null 2>&1; then`,
		`    yum install -y openssh-clients openssh-server`,
		`  else`,
		`    echo "unsupported package manager" >&2`,
		`    exit 1`,
		`  fi`,
		`fi`,
		`mkdir -p ~/.ssh`,
		`chmod 700 ~/.ssh`,
		`if [ ! -f ~/.ssh/id_rsa ] || [ ! -f ~/.ssh/id_rsa.pub ]; then`,
		`  ssh-keygen -t rsa -b 2048 -N "" -f ~/.ssh/id_rsa`,
		`fi`,
		`chmod 600 ~/.ssh/id_rsa`,
		`chmod 644 ~/.ssh/id_rsa.pub`,
	}, "\n")

	done := make(chan error, 1)
	go func() {
		done <- session.Run(cmd)
	}()

	select {
	case <-ctx.Done():
		_ = session.Close()
		return ctx.Err()
	case err := <-done:
		return err
	}
}

func resolvePath(path string) (string, error) {
	if strings.HasPrefix(path, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, strings.TrimPrefix(path, "~/")), nil
	}
	return path, nil
}

func discoverPublicKey(path string) (string, string, error) {
	if strings.TrimSpace(path) != "" {
		resolved, err := resolvePath(path)
		if err != nil {
			return "", "", err
		}
		data, err := os.ReadFile(resolved)
		if err != nil {
			if os.IsNotExist(err) {
				return resolved, "", nil
			}
			return "", "", err
		}
		return resolved, strings.TrimSpace(string(data)), nil
	}

	candidates := []string{"~/.ssh/id_ed25519.pub", "~/.ssh/id_rsa.pub"}
	for _, candidate := range candidates {
		resolved, err := resolvePath(candidate)
		if err != nil {
			return "", "", err
		}
		data, err := os.ReadFile(resolved)
		if err == nil {
			return resolved, strings.TrimSpace(string(data)), nil
		}
		if !os.IsNotExist(err) {
			return "", "", err
		}
	}
	return strings.Join(candidates, ", "), "", nil
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

type sessionFactory interface {
	NewSession() (*gossh.Session, error)
}
