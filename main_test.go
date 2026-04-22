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
