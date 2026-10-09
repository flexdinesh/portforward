package forward

import (
	"encoding/hex"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
)

const DefaultBind = "127.0.0.1"

type Mapping struct {
	BindAddress string `json:"bind_address"`
	LocalPort   int    `json:"local_port"`
	SSHHost     string `json:"ssh_host"`
	RemoteHost  string `json:"remote_host"`
	RemotePort  int    `json:"remote_port"`
}

func NewMapping(bind string, port int, host, destination string) (Mapping, error) {
	bind, err := NormalizeBind(bind)
	if err != nil {
		return Mapping{}, err
	}
	m := Mapping{BindAddress: bind, LocalPort: port, SSHHost: host, RemoteHost: "localhost", RemotePort: port}
	if destination != "" {
		remoteHost, remotePort, err := net.SplitHostPort(destination)
		if err != nil {
			return Mapping{}, fmt.Errorf("invalid destination %q: expected host:port", destination)
		}
		m.RemoteHost = remoteHost
		m.RemotePort, err = ParsePort(remotePort)
		if err != nil {
			return Mapping{}, err
		}
		if ip, err := netip.ParseAddr(remoteHost); err == nil {
			m.RemoteHost = ip.Unmap().String()
		}
	}
	return m, m.Validate()
}

func ParsePort(value string) (int, error) {
	for _, c := range value {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("invalid port %q: expected 1–65535", value)
		}
	}
	port, err := strconv.Atoi(value)
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("invalid port %q: expected 1–65535", value)
	}
	return port, nil
}

func NormalizeBind(bind string) (string, error) {
	address, err := netip.ParseAddr(bind)
	if err != nil || address.Zone() != "" {
		return "", fmt.Errorf("invalid bind IP address %q", bind)
	}
	return address.Unmap().String(), nil
}

func ValidateSSHHost(host string) error {
	if host == "" || len(host) > 255 || strings.HasPrefix(host, "-") {
		return fmt.Errorf("invalid SSH host %q", host)
	}
	if strings.Count(host, "@") > 1 || strings.HasPrefix(host, "@") || strings.HasSuffix(host, "@") {
		return fmt.Errorf("invalid SSH host %q: expected [user@]hostname", host)
	}
	for _, c := range host {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("._-@[]:", c)) {
			return fmt.Errorf("invalid SSH host %q: expected alias or [user@]hostname", host)
		}
	}
	return nil
}

func (m Mapping) Validate() error {
	ip, err := netip.ParseAddr(m.BindAddress)
	if err != nil || ip.Zone() != "" || ip.Unmap().String() != m.BindAddress {
		return fmt.Errorf("invalid bind IP address %q", m.BindAddress)
	}
	if m.LocalPort < 1 || m.LocalPort > 65535 || m.RemotePort < 1 || m.RemotePort > 65535 {
		return fmt.Errorf("ports must be between 1 and 65535")
	}
	if err := ValidateSSHHost(m.SSHHost); err != nil {
		return err
	}
	if ip, err := netip.ParseAddr(m.RemoteHost); err == nil && ip.Zone() == "" {
		return nil
	}
	if m.RemoteHost == "" || len(m.RemoteHost) > 253 || strings.HasPrefix(m.RemoteHost, "-") {
		return fmt.Errorf("invalid remote host %q", m.RemoteHost)
	}
	for _, c := range m.RemoteHost {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("._-", c)) {
			return fmt.Errorf("invalid remote host %q", m.RemoteHost)
		}
	}
	return nil
}

func (m Mapping) Local() string {
	return net.JoinHostPort(m.BindAddress, strconv.Itoa(m.LocalPort))
}

func (m Mapping) Destination() string {
	return net.JoinHostPort(m.RemoteHost, strconv.Itoa(m.RemotePort))
}

func (m Mapping) SameListener(other Mapping) bool {
	return m.BindAddress == other.BindAddress && m.LocalPort == other.LocalPort
}

type Phase string

const (
	Adding   Phase = "adding"
	Active   Phase = "active"
	Removing Phase = "removing"
)

type Connection struct {
	Host string
	ID   string
}

func (c Connection) Validate() error {
	if err := ValidateSSHHost(c.Host); err != nil {
		return err
	}
	decoded, err := hex.DecodeString(c.ID)
	if err != nil || len(decoded) != 16 || strings.ToLower(c.ID) != c.ID {
		return fmt.Errorf("invalid connection ID")
	}
	return nil
}

type Record struct {
	Mapping
	ConnectionID string `json:"connection_id"`
	Phase        Phase  `json:"phase"`
}

func (r Record) Connection() Connection {
	return Connection{Host: r.SSHHost, ID: r.ConnectionID}
}

func (r Record) Validate() error {
	if err := r.Mapping.Validate(); err != nil {
		return err
	}
	if err := r.Connection().Validate(); err != nil {
		return err
	}
	switch r.Phase {
	case Adding, Active, Removing:
		return nil
	default:
		return fmt.Errorf("invalid operation phase %q", r.Phase)
	}
}
