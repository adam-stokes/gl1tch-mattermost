package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/adam-stokes/gl1tch-mattermost/internal/client"
	"github.com/adam-stokes/gl1tch-mattermost/internal/publish"
	"github.com/adam-stokes/gl1tch-mattermost/internal/state"
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
  - (chat subcommand) interactive chat widget for gl1tch's /mattermost command
  - (post subcommand) posts a message to a channel or thread

Required environment variables:
  GLITCH_MATTERMOST_URL    Mattermost server URL (e.g. https://chat.example.com)
  GLITCH_MATTERMOST_TOKEN  Personal access token or bot token`,
	}

	root.AddCommand(daemonCmd(), postCmd(), chatCmd())

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

// chatCmd is the gl1tch widget handler for /mattermost.
// Each invocation reads one line from stdin and writes output to stdout.
// State (active channel, poll cursor) is persisted between calls.
func chatCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "chat",
		Short: "Interactive chat widget (used by gl1tch /mattermost command)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runChat()
		},
	}
}

func runChat() error {
	c, me, _, err := setup()
	if err != nil {
		return err
	}

	stdinBytes, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("reading stdin: %w", err)
	}
	input := strings.TrimSpace(string(stdinBytes))

	s, _ := state.Load()
	if s.MyUserID == "" {
		s.MyUserID = me.ID
	}

	switch {
	case input == "" || input == "/channels" || input == "/ls":
		return listChannels(c, me)

	case strings.HasPrefix(input, "/join "):
		target := strings.TrimPrefix(input, "/join ")
		return joinChannel(c, me, &s, target)

	case strings.HasPrefix(input, "@"):
		username := strings.TrimPrefix(input, "@")
		return openDM(c, me, &s, username)

	case input == "/who":
		if s.ActiveChannelID == "" {
			fmt.Println("No active channel. Use /channels to list, /join <name> or @<user> to open a DM.")
			return nil
		}
		fmt.Printf("Active channel: %s\n", s.ActiveChannelName)
		return nil

	case input == "/clear":
		_ = state.Clear()
		fmt.Println("Session cleared.")
		return nil

	default:
		// Send message to active channel.
		return sendMessage(c, me, &s, input)
	}
}

func listChannels(c *client.Client, me *client.User) error {
	teams, err := c.GetMyTeams()
	if err != nil {
		return fmt.Errorf("fetching teams: %w", err)
	}

	var lines []string
	for _, team := range teams {
		channels, err := c.GetMyChannelsForTeam(team.ID)
		if err != nil {
			continue
		}
		for _, ch := range channels {
			if ch.Type == "D" || ch.Type == "G" {
				continue // DMs shown separately
			}
			lines = append(lines, fmt.Sprintf("  [%s] #%s", team.DisplayName, ch.Name))
		}
	}

	dms, _ := c.GetDirectChannels(me.ID)
	sort.Slice(dms, func(i, j int) bool { return dms[i].Name < dms[j].Name })

	fmt.Println("── Channels ──────────────────────")
	for _, l := range lines {
		fmt.Println(l)
	}
	if len(dms) > 0 {
		fmt.Println("── Direct Messages ───────────────")
		for _, dm := range dms {
			name := dm.DisplayName
			if name == "" {
				name = dm.Name
			}
			fmt.Printf("  @%s\n", name)
		}
	}
	fmt.Println()
	fmt.Println("Type /join <#channel> or @<username> to start chatting.")
	return nil
}

func joinChannel(c *client.Client, me *client.User, s *state.State, target string) error {
	target = strings.TrimPrefix(target, "#")

	teams, err := c.GetMyTeams()
	if err != nil {
		return fmt.Errorf("fetching teams: %w", err)
	}

	for _, team := range teams {
		channels, err := c.GetMyChannelsForTeam(team.ID)
		if err != nil {
			continue
		}
		for _, ch := range channels {
			if ch.Name == target || ch.DisplayName == target || ch.ID == target {
				s.ActiveChannelID = ch.ID
				s.ActiveChannelName = "#" + ch.Name
				s.LastPollAt = time.Now().UnixMilli()
				_ = state.Save(*s)
				fmt.Printf("Joined #%s — type your message.\n", ch.Name)
				return nil
			}
		}
	}

	return fmt.Errorf("channel %q not found — use /channels to list available channels", target)
}

func openDM(c *client.Client, me *client.User, s *state.State, username string) error {
	other, err := c.GetUserByUsername(username)
	if err != nil {
		return fmt.Errorf("user @%s not found: %w", username, err)
	}

	ch, err := c.CreateDirectChannel(me.ID, other.ID)
	if err != nil {
		return fmt.Errorf("opening DM: %w", err)
	}

	s.ActiveChannelID = ch.ID
	s.ActiveChannelName = "@" + other.Username
	s.LastPollAt = time.Now().UnixMilli()
	_ = state.Save(*s)
	fmt.Printf("DM opened with @%s — type your message.\n", other.Username)
	return nil
}

func sendMessage(c *client.Client, me *client.User, s *state.State, message string) error {
	if s.ActiveChannelID == "" {
		fmt.Println("No active channel. Use /channels, /join <name>, or @<username> first.")
		return nil
	}
	if message == "" {
		return nil
	}

	// Post the message.
	if _, err := c.CreatePost(s.ActiveChannelID, "", message); err != nil {
		return fmt.Errorf("posting message: %w", err)
	}

	// Poll for new messages since last check (gives a moment for replies).
	time.Sleep(300 * time.Millisecond)
	posts, err := c.GetPostsSince(s.ActiveChannelID, s.LastPollAt)
	if err == nil {
		s.LastPollAt = time.Now().UnixMilli()
		_ = state.Save(*s)

		for _, p := range posts {
			if p.UserID == me.ID {
				continue // skip echo of own message
			}
			ts := time.UnixMilli(p.CreateAt).Format("15:04")
			fmt.Printf("[%s] %s\n", ts, p.Message)
		}
	}

	return nil
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
		sockPath = ""
	}

	return c, me, sockPath, nil
}
