package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/adam-stokes/gl1tch-mattermost/internal/client"
	"github.com/adam-stokes/gl1tch-mattermost/internal/publish"
	"github.com/spf13/cobra"
)

// BUSD topics published by this plugin.
const (
	topicMention = "mattermost.mention"
	topicDirect  = "mattermost.direct"
	topicMessage = "mattermost.message"
)

func main() {
	if err := rootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "glitch-mattermost",
		Short: "Mattermost integration for gl1tch",
		Long: `glitch-mattermost connects to a Mattermost server and:
  - (daemon mode) listens for mentions and messages, publishing them to the gl1tch event bus
  - (post subcommand) posts a message to a channel or thread

Required environment variables:
  GLITCH_MATTERMOST_URL    Mattermost server URL (e.g. https://chat.example.com)
  GLITCH_MATTERMOST_TOKEN  Personal access token or bot token`,
	}

	root.AddCommand(daemonCmd(), postCmd())

	// Default: daemon mode when invoked with no subcommand.
	root.RunE = func(cmd *cobra.Command, args []string) error {
		return runDaemon(cmd.Context())
	}

	return root
}

// daemonCmd starts the WebSocket listener and publishes BUSD events.
func daemonCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "daemon",
		Short: "Start the Mattermost WebSocket listener",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDaemon(cmd.Context())
		},
	}
}

func runDaemon(ctx context.Context) error {
	c, me, sockPath, err := setup()
	if err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	fmt.Fprintf(os.Stderr, "glitch-mattermost: listening as @%s (id=%s)\n", me.Username, me.ID)

	return c.Listen(ctx, func(evt client.WSEvent) {
		if evt.Event != client.EventPosted {
			return
		}

		data, post, err := client.ParsePostedData(evt)
		if err != nil {
			fmt.Fprintf(os.Stderr, "glitch-mattermost: parse error: %v\n", err)
			return
		}

		// Skip own messages.
		if post.UserID == me.ID {
			return
		}

		payload := map[string]any{
			"post_id":      post.ID,
			"channel_id":   post.ChannelID,
			"root_id":      post.RootID,
			"user_id":      post.UserID,
			"sender_name":  data.SenderName,
			"channel_type": data.ChannelType,
			"message":      post.Message,
			"create_at":    post.CreateAt,
		}

		topic := topicMessage

		// Direct message channel.
		if data.ChannelType == "D" {
			topic = topicDirect
		} else if client.IsMention(data, me.ID) {
			// Mention in a public/private channel.
			topic = topicMention
		}

		if err := publish.Event(sockPath, topic, payload); err != nil {
			fmt.Fprintf(os.Stderr, "glitch-mattermost: busd publish error: %v\n", err)
		}
	})
}

// postCmd reads a message from stdin and posts it to Mattermost.
func postCmd() *cobra.Command {
	var channelID, rootID string

	cmd := &cobra.Command{
		Use:   "post",
		Short: "Post a message to a Mattermost channel or thread",
		Long: `Reads the message from stdin and posts it to Mattermost.

Channel and thread can be set via flags or environment variables:
  --channel / GLITCH_MATTERMOST_CHANNEL   Channel ID to post to
  --root    / GLITCH_MATTERMOST_ROOT_ID   Root post ID for thread replies

The stdin input can be plain text or a JSON object:
  {"channel_id":"...", "root_id":"...", "message":"..."}
When JSON is provided, it takes precedence over flags/env.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPost(channelID, rootID)
		},
	}

	cmd.Flags().StringVar(&channelID, "channel", os.Getenv("GLITCH_MATTERMOST_CHANNEL"), "Channel ID")
	cmd.Flags().StringVar(&rootID, "root", os.Getenv("GLITCH_MATTERMOST_ROOT_ID"), "Root post ID (for thread replies)")

	return cmd
}

func runPost(channelID, rootID string) error {
	c, _, _, err := setup()
	if err != nil {
		return err
	}

	stdinBytes, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("reading stdin: %w", err)
	}
	input := strings.TrimSpace(string(stdinBytes))

	// Try to parse as JSON first.
	var req struct {
		ChannelID string `json:"channel_id"`
		RootID    string `json:"root_id"`
		Message   string `json:"message"`
	}
	if json.Unmarshal([]byte(input), &req) == nil && req.Message != "" {
		if req.ChannelID != "" {
			channelID = req.ChannelID
		}
		if req.RootID != "" {
			rootID = req.RootID
		}
		input = req.Message
	}

	if channelID == "" {
		return fmt.Errorf("channel is required: set --channel flag or GLITCH_MATTERMOST_CHANNEL env var")
	}
	if input == "" {
		return fmt.Errorf("message is required: provide text on stdin")
	}

	post, err := c.CreatePost(channelID, rootID, input)
	if err != nil {
		return fmt.Errorf("creating post: %w", err)
	}

	fmt.Printf(`{"post_id":%q,"channel_id":%q}`, post.ID, post.ChannelID)
	fmt.Println()
	return nil
}

// setup validates required env vars and returns a ready client + current user.
func setup() (*client.Client, *client.User, string, error) {
	serverURL := os.Getenv("GLITCH_MATTERMOST_URL")
	token := os.Getenv("GLITCH_MATTERMOST_TOKEN")

	if serverURL == "" {
		return nil, nil, "", fmt.Errorf("GLITCH_MATTERMOST_URL is required")
	}
	if token == "" {
		return nil, nil, "", fmt.Errorf("GLITCH_MATTERMOST_TOKEN is required")
	}

	c := client.New(serverURL, token)
	me, err := c.Me()
	if err != nil {
		return nil, nil, "", fmt.Errorf("authenticating: %w", err)
	}

	sockPath, err := publish.SocketPath()
	if err != nil {
		// Non-fatal: daemon can run without BUSD.
		sockPath = ""
		fmt.Fprintf(os.Stderr, "glitch-mattermost: warning: cannot resolve busd socket: %v\n", err)
	}

	return c, me, sockPath, nil
}
