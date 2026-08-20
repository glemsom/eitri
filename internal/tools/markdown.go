package tools

import (
	"bytes"
	"io"
	"strings"
	"unicode"

	"golang.org/x/net/html"
)

// htmlToMarkdown converts an HTML document to a compact Markdown form via the html tokenizer.
func htmlToMarkdown(body io.Reader) (string, error) {
	z := html.NewTokenizer(body)
	st := &mdState{}
	var b bytes.Buffer
	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken:
			if z.Err() == io.EOF {
				return strings.TrimSpace(b.String()) + "\n", nil
			}
			return b.String(), z.Err()
		case html.StartTagToken:
			tn, hasAttr := z.TagName()
			switch string(tn) {
			case "script", "style", "head", "nav", "template", "svg":
				st.drop(z, string(tn))
			case "h1", "h2", "h3", "h4", "h5", "h6":
				b.WriteString(strings.Repeat("#", int(tn[1]-'0')) + " ")
			case "li":
				b.WriteString("\n- ")
			case "br":
				b.WriteString("\n")
			case "pre":
				b.WriteString("\n```\n")
				st.inPre = true
			case "code":
				if !st.inPre {
					b.WriteString("`")
				}
			case "strong", "b":
				b.WriteString("**")
			case "em", "i":
				b.WriteString("*")
			case "a":
				b.WriteString("[")
				if hasAttr {
					if href, ok := attrHref(z); ok {
						st.pendingLink = href
					}
				}
			case "p", "div", "section", "article", "ul", "ol", "blockquote", "table", "tr":
				b.WriteString("\n\n")
			}
		case html.SelfClosingTagToken:
			tn, hasAttr := z.TagName()
			switch string(tn) {
			case "br":
				b.WriteString("\n")
			case "img":
				if hasAttr {
					if alt, ok := attrAlt(z); ok && alt != "" {
						b.WriteString("[" + alt + "]")
					}
				}
			}
		case html.EndTagToken:
			tn, _ := z.TagName()
			switch string(tn) {
			case "pre":
				b.WriteString("\n```\n")
				st.inPre = false
			case "code":
				if !st.inPre {
					b.WriteString("`")
				}
			case "strong", "b":
				b.WriteString("**")
			case "em", "i":
				b.WriteString("*")
			case "a":
				b.WriteString("]")
				if st.pendingLink != "" {
					b.WriteString("(" + st.pendingLink + ")")
					st.pendingLink = ""
				}
			case "li":
				b.WriteString("\n")
			case "p", "div", "section", "article", "ul", "ol", "blockquote", "table", "tr":
				b.WriteString("\n\n")
			}
		case html.TextToken:
			text := string(z.Text())
			if st.inPre {
				b.WriteString(text)
			} else {
				b.WriteString(tidyText(text))
			}
		}
	}
}

// mdState carries per-document converter state (code-fence and in-flight link href), keeping htmlToMarkdown free of global mutable state.
type mdState struct {
	inPre       bool
	pendingLink string
}

// attrValue reads the value of the first attribute named want on the current start tag, which must be read right after z.TagName when it reported hasAttr.
func attrValue(z *html.Tokenizer, want string) (string, bool) {
	for {
		k, v, more := z.TagAttr()
		if string(k) == want {
			return string(v), true
		}
		if !more {
			return "", false
		}
	}
}

// attrHref reads the href attribute.
func attrHref(z *html.Tokenizer) (string, bool) {
	return attrValue(z, "href")
}

// attrAlt reads the alt attribute.
func attrAlt(z *html.Tokenizer) (string, bool) {
	return attrValue(z, "alt")
}

// tidyText collapses runs of whitespace within an inline text segment.
func tidyText(s string) string {
	return strings.Join(strings.FieldsFunc(s, unicode.IsSpace), " ")
}

// drop consumes the current tag's descendants (and its close tag), so chrome like <style>/<script>/<nav> bodies never reach the output.
func (st *mdState) drop(z *html.Tokenizer, name string) {
	depth := 1
	for depth > 0 {
		tt := z.Next()
		if tt == html.ErrorToken {
			_ = z.Err()
			return
		}
		switch tt {
		case html.StartTagToken, html.SelfClosingTagToken:
			tn, _ := z.TagName()
			if string(tn) == name {
				depth++
			}
		case html.EndTagToken:
			tn, _ := z.TagName()
			if string(tn) == name {
				depth--
			}
		}
	}
}
