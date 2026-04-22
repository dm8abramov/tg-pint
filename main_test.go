package main

import "testing"

func TestMarkdownToTelegramHTML(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "bold",
			in:   "Это **важно**.",
			want: "Это <b>важно</b>.",
		},
		{
			name: "escape html outside and inside bold",
			in:   "2 < 3 && **x > y**",
			want: "2 &lt; 3 &amp;&amp; <b>x &gt; y</b>",
		},
		{
			name: "italic strike link and inline code",
			in:   "_i_ ~~s~~ [`code`](https://example.com?a=1&b=2)",
			want: `<i>i</i> <s>s</s> <a href="https://example.com?a=1&amp;b=2"><code>code</code></a>`,
		},
		{
			name: "headings and lists",
			in:   "## План\n\n- один\n- **два**",
			want: "<b>План</b>\n\n- один\n- <b>два</b>",
		},
		{
			name: "fenced code",
			in:   "```go\nfmt.Println(\"<ok>\")\n```",
			want: `<pre><code class="language-go">fmt.Println("&lt;ok&gt;")` + "\n</code></pre>",
		},
		{
			name: "raw html is escaped",
			in:   `<b>not telegram markup</b>`,
			want: "&lt;b&gt;not telegram markup&lt;/b&gt;",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := markdownToTelegramHTML(tt.in)
			if got != tt.want {
				t.Fatalf("markdownToTelegramHTML(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestSplitBotCommand(t *testing.T) {
	tests := []struct {
		name        string
		text        string
		botUsername string
		wantCommand string
		wantArgs    string
		wantOK      bool
	}{
		{
			name:        "plain command",
			text:        "/set_probability 0.5",
			wantCommand: "/set_probability",
			wantArgs:    "0.5",
			wantOK:      true,
		},
		{
			name:        "command with bot suffix",
			text:        "/set_promt@TestBot новый промт",
			botUsername: "testbot",
			wantCommand: "/set_promt",
			wantArgs:    "новый промт",
			wantOK:      true,
		},
		{
			name:        "set prompt alias",
			text:        "/set_prompt новый промт",
			wantCommand: "/set_prompt",
			wantArgs:    "новый промт",
			wantOK:      true,
		},
		{
			name:        "settings command with bot suffix",
			text:        "/settings@TestBot",
			botUsername: "testbot",
			wantCommand: "/settings",
			wantOK:      true,
		},
		{
			name:        "command for another bot",
			text:        "/set_probability@OtherBot 0.5",
			botUsername: "testbot",
			wantOK:      false,
		},
		{
			name:   "not command",
			text:   "set_probability 0.5",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotCommand, gotArgs, gotOK := splitBotCommand(tt.text, tt.botUsername)
			if gotOK != tt.wantOK {
				t.Fatalf("splitBotCommand(%q, %q) ok = %v, want %v", tt.text, tt.botUsername, gotOK, tt.wantOK)
			}
			if gotCommand != tt.wantCommand || gotArgs != tt.wantArgs {
				t.Fatalf("splitBotCommand(%q, %q) = (%q, %q), want (%q, %q)", tt.text, tt.botUsername, gotCommand, gotArgs, tt.wantCommand, tt.wantArgs)
			}
		})
	}
}

func TestUpdateSystemPromptClearsHistory(t *testing.T) {
	b := &bot{
		cfg: config{
			SystemPrompt: "old",
		},
		histories: &chatHistories{items: make(map[int64][]chatMessage)},
	}
	b.histories.add(1, chatMessage{Role: "user", Text: "old context"})

	b.updateSystemPrompt("new")

	if got := b.systemPrompt(); got != "new" {
		t.Fatalf("systemPrompt() = %q, want %q", got, "new")
	}
	if got := b.histories.get(1); len(got) != 0 {
		t.Fatalf("history len = %d, want 0", len(got))
	}
}
