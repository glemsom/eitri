package tui

type liveMarkdownCache struct {
	key    liveMarkdownCacheKey
	out    string
	valid  bool
	hits   int
	misses int
}

type liveMarkdownCacheKey struct {
	text  string
	width int
	theme string
}

func (c *liveMarkdownCache) render(text string, width int, theme string) (string, error) {
	key := liveMarkdownCacheKey{text: text, width: width, theme: theme}
	if c != nil && c.valid && c.key == key {
		c.hits++
		return c.out, nil
	}
	out, err := RenderMarkdown(text, width, theme)
	if err != nil {
		return "", err
	}
	if c != nil {
		c.key = key
		c.out = out
		c.valid = true
		c.misses++
	}
	return out, nil
}

func renderCachedMarkdown(c *liveMarkdownCache, text string, width int, theme string) (string, error) {
	if c == nil {
		return RenderMarkdown(text, width, theme)
	}
	return c.render(text, width, theme)
}
