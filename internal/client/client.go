// Package client provides a minimal Mattermost API v4 REST client.
package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client is a thin Mattermost REST API v4 client.
type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

// New returns a Client for the given server URL and personal access token.
func New(serverURL, token string) *Client {
	return &Client{
		baseURL: strings.TrimRight(serverURL, "/") + "/api/v4",
		token:   token,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// User is a minimal Mattermost user object.
type User struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

// Post is a minimal Mattermost post object.
type Post struct {
	ID        string `json:"id"`
	ChannelID string `json:"channel_id"`
	UserID    string `json:"user_id"`
	RootID    string `json:"root_id"`
	Message   string `json:"message"`
	CreateAt  int64  `json:"create_at"`
}

// Channel is a minimal Mattermost channel object.
type Channel struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Type        string `json:"type"` // "D" = direct, "O" = public, "P" = private
	TeamID      string `json:"team_id"`
}

// Team is a minimal Mattermost team object.
type Team struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
}

// PostList is the response from the posts endpoint.
type PostList struct {
	Order []string        `json:"order"`
	Posts map[string]Post `json:"posts"`
}

// Me returns the authenticated user.
func (c *Client) Me() (*User, error) {
	var u User
	if err := c.get("/users/me", &u); err != nil {
		return nil, err
	}
	return &u, nil
}

// GetChannel returns channel metadata by ID.
func (c *Client) GetChannel(channelID string) (*Channel, error) {
	var ch Channel
	if err := c.get("/channels/"+channelID, &ch); err != nil {
		return nil, err
	}
	return &ch, nil
}

// CreatePost sends a message to a channel, optionally as a thread reply.
// Set rootID to "" for a top-level post.
func (c *Client) CreatePost(channelID, rootID, message string) (*Post, error) {
	body := map[string]string{
		"channel_id": channelID,
		"message":    message,
	}
	if rootID != "" {
		body["root_id"] = rootID
	}
	var post Post
	if err := c.post("/posts", body, &post); err != nil {
		return nil, err
	}
	return &post, nil
}

// GetMyTeams returns the authenticated user's teams.
func (c *Client) GetMyTeams() ([]Team, error) {
	var teams []Team
	if err := c.get("/users/me/teams", &teams); err != nil {
		return nil, err
	}
	return teams, nil
}

// GetMyChannelsForTeam returns channels the user belongs to in a team.
func (c *Client) GetMyChannelsForTeam(teamID string) ([]Channel, error) {
	var channels []Channel
	if err := c.get("/users/me/teams/"+teamID+"/channels", &channels); err != nil {
		return nil, err
	}
	return channels, nil
}

// GetDirectChannels returns the user's open direct message channels.
func (c *Client) GetDirectChannels(userID string) ([]Channel, error) {
	var channels []Channel
	if err := c.get(fmt.Sprintf("/users/%s/channels?include_deleted=false", userID), &channels); err != nil {
		return nil, err
	}
	var direct []Channel
	for _, ch := range channels {
		if ch.Type == "D" || ch.Type == "G" {
			direct = append(direct, ch)
		}
	}
	return direct, nil
}

// GetPostsSince returns posts in a channel created after sinceMs (Unix ms).
func (c *Client) GetPostsSince(channelID string, sinceMs int64) ([]Post, error) {
	var pl PostList
	if err := c.get(fmt.Sprintf("/channels/%s/posts?since=%d", channelID, sinceMs), &pl); err != nil {
		return nil, err
	}
	posts := make([]Post, 0, len(pl.Order))
	for _, id := range pl.Order {
		if p, ok := pl.Posts[id]; ok {
			posts = append(posts, p)
		}
	}
	// Reverse: oldest first.
	for i, j := 0, len(posts)-1; i < j; i, j = i+1, j-1 {
		posts[i], posts[j] = posts[j], posts[i]
	}
	return posts, nil
}

// GetUserByUsername looks up a user by their username.
func (c *Client) GetUserByUsername(username string) (*User, error) {
	var u User
	if err := c.get("/users/username/"+username, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

// GetUsersByIDs fetches multiple users by their IDs in a single call.
func (c *Client) GetUsersByIDs(ids []string) ([]User, error) {
	var users []User
	if err := c.post("/users/ids", ids, &users); err != nil {
		return nil, err
	}
	return users, nil
}

// CreateDirectChannel opens (or returns existing) DM channel with another user.
func (c *Client) CreateDirectChannel(myID, otherID string) (*Channel, error) {
	var ch Channel
	if err := c.post("/channels/direct", []string{myID, otherID}, &ch); err != nil {
		return nil, err
	}
	return &ch, nil
}

// WebSocketURL returns the WebSocket endpoint for this server.
func (c *Client) WebSocketURL() string {
	base := strings.TrimSuffix(c.baseURL, "/api/v4")
	base = strings.Replace(base, "https://", "wss://", 1)
	base = strings.Replace(base, "http://", "ws://", 1)
	return base + "/api/v4/websocket"
}

// Token returns the auth token (needed for WebSocket auth frame).
func (c *Client) Token() string { return c.token }

func (c *Client) get(path string, out any) error {
	req, err := http.NewRequest(http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("GET %s: HTTP %d: %s", path, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) post(path string, body, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, c.baseURL+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("POST %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		rb, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("POST %s: HTTP %d: %s", path, resp.StatusCode, strings.TrimSpace(string(rb)))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
