// Package state manages persistent chat session state between widget invocations.
package state

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// State holds the active channel and poll cursor for the chat widget.
type State struct {
	ActiveChannelID   string `json:"active_channel_id"`
	ActiveChannelName string `json:"active_channel_name"`
	MyUserID          string `json:"my_user_id"`
	LastPollAt        int64  `json:"last_poll_at"` // Unix milliseconds
}

func path() (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(cache, "glitch")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "mattermost-state.json"), nil
}

// Load reads the persisted state. Returns a zero State if the file doesn't exist.
func Load() (State, error) {
	p, err := path()
	if err != nil {
		return State{}, err
	}
	b, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return State{}, nil
	}
	if err != nil {
		return State{}, err
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return State{}, err
	}
	return s, nil
}

// Save writes state to disk.
func Save(s State) error {
	p, err := path()
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, b, 0o600)
}

// Clear removes the state file.
func Clear() error {
	p, err := path()
	if err != nil {
		return err
	}
	err = os.Remove(p)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
