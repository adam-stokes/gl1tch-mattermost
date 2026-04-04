// Package publish provides a minimal BUSD client for publishing events
// to the gl1tch internal event bus from plugin processes.
//
// Wire protocol:
//
//	Connect to Unix socket (path from SocketPath).
//	Send registration frame: {"name":"mattermost","subscribe":[]}\n
//	Send publish frame:      {"action":"publish","event":"<topic>","payload":{...}}\n
package publish

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"
)

// SocketPath resolves the busd Unix socket path using the same logic as gl1tch.
// Primary:  $XDG_RUNTIME_DIR/glitch/bus.sock
// Fallback: $XDG_CACHE_HOME/glitch/bus.sock
func SocketPath() (string, error) {
	if xdg := os.Getenv("XDG_RUNTIME_DIR"); xdg != "" {
		return filepath.Join(xdg, "glitch", "bus.sock"), nil
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("busd: cannot determine socket path: %w", err)
	}
	return filepath.Join(cache, "glitch", "bus.sock"), nil
}

type registrationFrame struct {
	Name      string   `json:"name"`
	Subscribe []string `json:"subscribe"`
}

type publishFrame struct {
	Action  string `json:"action"`
	Event   string `json:"event"`
	Payload any    `json:"payload"`
}

// Event publishes topic with payload to the gl1tch BUSD.
// Returns nil silently if the daemon is not running.
func Event(sockPath, topic string, payload any) error {
	conn, err := net.DialTimeout("unix", sockPath, 200*time.Millisecond)
	if err != nil {
		return nil // bus not running — degrade silently
	}
	defer conn.Close()

	reg, _ := json.Marshal(registrationFrame{Name: "mattermost", Subscribe: nil})
	reg = append(reg, '\n')
	if _, err := conn.Write(reg); err != nil {
		return fmt.Errorf("busd: register: %w", err)
	}

	pub, err := json.Marshal(publishFrame{Action: "publish", Event: topic, Payload: payload})
	if err != nil {
		return fmt.Errorf("busd: marshal: %w", err)
	}
	pub = append(pub, '\n')
	if _, err := conn.Write(pub); err != nil {
		return fmt.Errorf("busd: publish: %w", err)
	}
	return nil
}
