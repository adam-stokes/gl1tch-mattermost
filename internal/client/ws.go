package client

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// EventType constants for Mattermost WebSocket events we care about.
const (
	EventPosted        = "posted"
	EventDirectPosted  = "direct_message"
	EventStatusChange  = "status_change"
)

// WSEvent is a decoded Mattermost WebSocket event frame.
type WSEvent struct {
	Event   string          `json:"event"`
	Data    json.RawMessage `json:"data"`
	Seq     int64           `json:"seq"`
	Broadcast struct {
		ChannelID string `json:"channel_id"`
		TeamID    string `json:"team_id"`
		UserID    string `json:"user_id"`
	} `json:"broadcast"`
}

// PostedData is the inner data payload for "posted" events.
type PostedData struct {
	Post            string `json:"post"`   // JSON-encoded Post object (yes, double-encoded)
	ChannelType     string `json:"channel_type"`
	SenderName      string `json:"sender_name"`
	TeamID          string `json:"team_id"`
	Mentions        string `json:"mentions"` // JSON-encoded []string of mentioned user IDs
	SetOnline       bool   `json:"set_online"`
}

// PostedPost is the inner Post extracted from PostedData.Post.
type PostedPost struct {
	ID        string `json:"id"`
	ChannelID string `json:"channel_id"`
	UserID    string `json:"user_id"`
	RootID    string `json:"root_id"`
	Message   string `json:"message"`
	CreateAt  int64  `json:"create_at"`
}

// Listen connects to the Mattermost WebSocket and calls handler for each event.
// It reconnects automatically on disconnect until ctx is cancelled.
func (c *Client) Listen(ctx context.Context, handler func(WSEvent)) error {
	wsURL := c.WebSocketURL()
	backoff := 2 * time.Second

	for {
		if err := ctx.Err(); err != nil {
			return nil
		}

		if err := c.listenOnce(ctx, wsURL, handler); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			fmt.Printf("mattermost ws: disconnected (%v), reconnecting in %s\n", err, backoff)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(backoff):
				backoff = min(backoff*2, 60*time.Second)
			}
		} else {
			backoff = 2 * time.Second
		}
	}
}

func (c *Client) listenOnce(ctx context.Context, wsURL string, handler func(WSEvent)) error {
	dialer := websocket.DefaultDialer
	conn, _, err := dialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()

	// Authenticate via the challenge/response sequence.
	authMsg := map[string]any{
		"seq":    1,
		"action": "authentication_challenge",
		"data":   map[string]string{"token": c.token},
	}
	if err := conn.WriteJSON(authMsg); err != nil {
		return fmt.Errorf("auth write: %w", err)
	}

	// Context cancellation → close the connection.
	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		var evt WSEvent
		if err := json.Unmarshal(msg, &evt); err != nil || evt.Event == "" {
			continue
		}
		handler(evt)
	}
}

// ParsePostedData decodes the double-encoded post from a "posted" event.
func ParsePostedData(evt WSEvent) (PostedData, PostedPost, error) {
	var data PostedData
	if err := json.Unmarshal(evt.Data, &data); err != nil {
		return data, PostedPost{}, fmt.Errorf("decode posted data: %w", err)
	}
	var post PostedPost
	if err := json.Unmarshal([]byte(data.Post), &post); err != nil {
		return data, post, fmt.Errorf("decode inner post: %w", err)
	}
	return data, post, nil
}

// IsMention returns true if userID appears in the mentions list of data.
func IsMention(data PostedData, userID string) bool {
	if data.Mentions == "" {
		return false
	}
	var ids []string
	if err := json.Unmarshal([]byte(data.Mentions), &ids); err != nil {
		// Fallback: plain string check.
		return strings.Contains(data.Mentions, userID)
	}
	for _, id := range ids {
		if id == userID {
			return true
		}
	}
	return false
}

func min(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
