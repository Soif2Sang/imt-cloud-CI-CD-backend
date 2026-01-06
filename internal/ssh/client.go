package ssh

import (
	"bytes"
	"fmt"
	"strings"

	"golang.org/x/crypto/ssh"
)

type Client struct {
	client *ssh.Client
}

// NewClient creates a new SSH connection
func NewClient(host, user, privateKey string) (*Client, error) {
	signer, err := ssh.ParsePrivateKey([]byte(privateKey))
	if err != nil {
		return nil, fmt.Errorf("failed to parse private key: %w", err)
	}

	config := &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(signer),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // Note: In production, verify host keys
	}

	// Handle host:port logic simply
	addr := host
	if !strings.Contains(host, ":") {
		addr = host + ":22"
	}

	client, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		return nil, fmt.Errorf("failed to dial ssh: %w", err)
	}

	return &Client{client: client}, nil
}

// Close closes the connection
func (c *Client) Close() error {
	return c.client.Close()
}

// RunCommand executes a command on the remote server
func (c *Client) RunCommand(cmd string) (string, error) {
	session, err := c.client.NewSession()
	if err != nil {
		return "", fmt.Errorf("failed to create session: %w", err)
	}
	defer session.Close()

	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr

	err = session.Run(cmd)
	output := stdout.String() + stderr.String() // Combine logs

	if err != nil {
		return output, fmt.Errorf("remote command failed: %w", err)
	}
	return output, nil
}

// CopyFile sends a file content to a remote path (using simple cat redirection)
func (c *Client) CopyFile(localContent []byte, remotePath string) error {
	session, err := c.client.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()

	session.Stdin = bytes.NewReader(localContent)
	// Write stdin to file on remote
	return session.Run(fmt.Sprintf("cat > %s", remotePath))
}